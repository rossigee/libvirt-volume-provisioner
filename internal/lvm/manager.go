// Package lvm provides functionality for managing LVM (Logical Volume Manager)
// volumes including creation, conversion, and removal operations.
package lvm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/rossigee/libvirt-volume-provisioner/internal/retry"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// ProgressUpdater interface for updating job progress
type ProgressUpdater interface {
	UpdateProgress(stage string, percent float64, bytesProcessed, bytesTotal int64)
}

// Manager handles LVM operations
type Manager struct {
	vgName      string
	retryConfig retry.Config
}

// NewManager creates a new LVM manager with configurable volume group
func NewManager(vgName string) (*Manager, error) {
	// Validate volume group name (prevent path traversal)
	if vgName == "" {
		vgName = os.Getenv("LVM_VOLUME_GROUP")
		if vgName == "" {
			vgName = "vg0" // Generic default
		}
	}
	if strings.ContainsAny(vgName, "/\\") {
		return nil, fmt.Errorf("invalid volume group name '%s': must not contain path separators", vgName)
	}

	// Verify the volume group exists
	cmd := exec.CommandContext(context.Background(), "vgs", vgName)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("volume group '%s' does not exist or is not accessible: %w", vgName, err)
	}

	// Verify LVM commands are available
	if _, err := exec.LookPath("lvcreate"); err != nil {
		return nil, fmt.Errorf("lvcreate command not found: %w", err)
	}
	if _, err := exec.LookPath("qemu-img"); err != nil {
		return nil, fmt.Errorf("qemu-img command not found: %w", err)
	}

	// Configure retry logic
	retryConfig := parseLvmRetryConfig(
		os.Getenv("LVM_RETRY_ATTEMPTS"),
		os.Getenv("LVM_RETRY_BACKOFF_MS"),
	)

	return &Manager{
		vgName:      vgName,
		retryConfig: retryConfig,
	}, nil
}

// parseLvmRetryConfig parses retry configuration from environment variables
func parseLvmRetryConfig(attemptsStr, backoffStr string) retry.Config {
	// Default values for LVM (more conservative than MinIO)
	maxAttempts := 2
	delays := []time.Duration{100 * time.Millisecond, 1 * time.Second}

	// Parse max attempts
	if attemptsStr != "" {
		if attempts, err := strconv.Atoi(attemptsStr); err == nil && attempts > 0 {
			maxAttempts = attempts
		}
	}

	// Parse backoff delays
	if backoffStr != "" {
		var parsedDelays []time.Duration
		for _, delayStr := range strings.Split(backoffStr, ",") {
			if ms, err := strconv.Atoi(strings.TrimSpace(delayStr)); err == nil && ms > 0 {
				parsedDelays = append(parsedDelays, time.Duration(ms)*time.Millisecond)
			}
		}
		if len(parsedDelays) > 0 {
			delays = parsedDelays
		}
	}

	return retry.Config{
		MaxAttempts: maxAttempts,
		Delays:      delays,
	}
}

// CreateVolume creates a new LVM volume with exponential backoff retry
// If volume exists, validates it matches requirements and reuses if compatible
func (m *Manager) CreateVolume(ctx context.Context, volumeName string, sizeGB int) error {
	// Start span for LVM volume creation
	tracer := otel.Tracer("libvirt-volume-provisioner")
	ctx, span := tracer.Start(ctx, "CreateVolume",
		trace.WithAttributes(
			attribute.String("lvm.volume_name", volumeName),
			attribute.Int("lvm.size_gb", sizeGB),
			attribute.String("lvm.vg_name", m.vgName)))
	defer span.End()

	// Check if volume already exists
	if m.volumeExists(volumeName) {
		// Validate existing volume
		if err := m.validateExistingVolume(volumeName, sizeGB); err != nil {
			span.SetStatus(codes.Error, err.Error())
			return fmt.Errorf("existing volume %s is incompatible: %w", volumeName, err)
		}
		logrus.WithFields(logrus.Fields{
			"volume_name": volumeName,
			"size_gb":     sizeGB,
		}).Info("Reusing existing compatible volume")
		span.SetStatus(codes.Ok, "reused existing compatible volume")
		return nil
	}

	// Create new volume
	err := retry.WithRetry(ctx, m.retryConfig, func() error {
		return m.createVolumeOnce(volumeName, sizeGB)
	})
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("failed to create volume %s after retries: %w", volumeName, err)
	}
	span.SetStatus(codes.Ok, "volume created successfully")
	return nil
}

// createVolumeOnce performs a single LVM volume creation attempt
func (m *Manager) createVolumeOnce(volumeName string, sizeGB int) error {
	// Create LVM volume
	//nolint:gosec,noctx // LVM command parameters are validated and controlled internally
	cmd := exec.Command("lvcreate", "-L", fmt.Sprintf("%dG", sizeGB), "-n", volumeName, m.vgName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to create LVM volume: %w, output: %s", err, string(output))
	}

	return nil
}

// PopulateVolume populates an LVM volume with image data with exponential backoff retry
func (m *Manager) PopulateVolume(
	ctx context.Context,
	imagePath, volumeName, imageType string,
	updater ProgressUpdater,
) error {
	// Start span for LVM volume population
	tracer := otel.Tracer("libvirt-volume-provisioner")
	ctx, span := tracer.Start(ctx, "PopulateVolume",
		trace.WithAttributes(
			attribute.String("lvm.volume_name", volumeName),
			attribute.String("lvm.image_path", imagePath),
			attribute.String("lvm.image_type", imageType)))
	defer span.End()

	// Wrap with retry logic
	err := retry.WithRetry(ctx, m.retryConfig, func() error {
		return m.populateVolumeOnce(imagePath, volumeName, imageType, updater)
	})
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("failed to populate volume %s after retries: %w", volumeName, err)
	}
	span.SetStatus(codes.Ok, "volume populated successfully")
	return nil
}

// populateVolumeOnce performs a single volume population attempt
func (m *Manager) populateVolumeOnce(imagePath, volumeName, imageType string, updater ProgressUpdater) error {
	// Get the device path for the LVM volume
	devicePath := fmt.Sprintf("/dev/%s/%s", m.vgName, volumeName)

	// Verify the device exists
	//nolint:gosec,noctx // Device path from internal volume name; validation doesn't need context
	if _, err := exec.Command("test", "-b", devicePath).CombinedOutput(); err != nil {
		return fmt.Errorf("LVM volume device does not exist: %s", devicePath)
	}

	// Check if volume already has content by looking for filesystem
	//nolint:gosec,noctx // Device path from internal volume name
	blkidCmd := exec.Command("blkid", "-o", "value", "-s", "TYPE", devicePath)
	blkidOutput, blkidErr := blkidCmd.Output()

	// If blkid finds a filesystem, the volume has content
	if blkidErr == nil && strings.TrimSpace(string(blkidOutput)) != "" {
		filesystemType := strings.TrimSpace(string(blkidOutput))
		logrus.WithFields(logrus.Fields{
			"volume_name":     volumeName,
			"device_path":     devicePath,
			"filesystem_type": filesystemType,
		}).Info("Volume already has filesystem, skipping population")
		return nil
	}

	logrus.WithFields(logrus.Fields{
		"volume_name": volumeName,
		"device_path": devicePath,
	}).Info("Volume has no filesystem, proceeding with population")

	logrus.WithFields(logrus.Fields{
		"volume_name": volumeName,
		"device_path": devicePath,
		"image_path":  imagePath,
		"image_type":  imageType,
	}).Info("Starting volume population")

	// Convert image format if needed and copy to LVM volume
	// For qcow2 images, we use streaming conversion with small buffer to avoid OOM
	var cmd *exec.Cmd
	switch imageType {
	case "qcow2":
		//nolint:gosec,noctx // Image path is provided by caller, device path is internal
		cmd = exec.Command("qemu-img", "convert", "-p", "-f", "qcow2", "-O", "raw", imagePath, "-")
		deviceFile, err := os.OpenFile(devicePath, os.O_WRONLY, 0)
		if err != nil {
			return fmt.Errorf("failed to open device %s: %w", devicePath, err)
		}
		defer func() {
			if err := deviceFile.Close(); err != nil {
				logrus.WithError(err).Warn("failed to close device file after population")
			}
		}()
		cmd.Stdout = deviceFile
	case "raw":
		// Direct copy for raw images
		//nolint:gosec,noctx // Image path is provided by caller, device path is internal
		cmd = exec.Command("dd", "if="+imagePath, "of="+devicePath, "bs=4M", "status=progress", "conv=fdatasync")
	default:
		return fmt.Errorf("unsupported image type: %s", imageType)
	}

	// Execute conversion with progress tracking
	logrus.WithFields(logrus.Fields{
		"volume_name": volumeName,
		"command":     strings.Join(cmd.Args, " "),
	}).Info("Executing volume population command")

	// For qcow2, stdout is already redirected to the device; capture stderr separately.
	// For other types, use CombinedOutput to capture all output for error reporting.
	var output []byte
	var err error
	if cmd.Stdout != nil {
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err = cmd.Run()
		output = stderr.Bytes()
	} else {
		output, err = cmd.CombinedOutput()
	}
	if err != nil {
		exitCode := -1
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		}
		logrus.WithFields(logrus.Fields{
			"volume_name": volumeName,
			"error":       err,
			"exit_code":   exitCode,
			"output":      string(output),
		}).Error("Volume population command failed")
		return fmt.Errorf("failed to populate LVM volume: %w, output: %s", err, string(output))
	}

	logrus.WithFields(logrus.Fields{
		"volume_name": volumeName,
	}).Info("Volume population command completed successfully")

	// Update progress
	if updater != nil {
		updater.UpdateProgress("converting", 95, 0, 0)
	}

	return nil
}

// DeleteVolume deletes an LVM volume
func (m *Manager) DeleteVolume(volumeName string) error {
	// Start span for LVM volume deletion
	_, span := otel.Tracer("libvirt-volume-provisioner").Start(context.Background(), "DeleteVolume",
		trace.WithAttributes(
			attribute.String("lvm.volume_name", volumeName),
			attribute.String("lvm.vg_name", m.vgName)))
	defer span.End()

	if !m.volumeExists(volumeName) {
		span.SetStatus(codes.Error, "volume does not exist")
		return fmt.Errorf("volume %s does not exist", volumeName)
	}

	//nolint:gosec,noctx // Volume name is validated internally
	cmd := exec.Command("lvremove", "-f", fmt.Sprintf("%s/%s", m.vgName, volumeName))
	output, err := cmd.CombinedOutput()
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"volume_name": volumeName,
			"error":       err,
			"output":      string(output),
		}).Error("Failed to delete LVM volume")
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("failed to delete LVM volume %s: %w", volumeName, err)
	}

	span.SetStatus(codes.Ok, "volume deleted successfully")
	return nil
}

// GetVolumeInfo returns information about an LVM volume
func (m *Manager) GetVolumeInfo(volumeName string) (*VolumeInfo, error) {
	if !m.volumeExists(volumeName) {
		return nil, fmt.Errorf("volume %s does not exist", volumeName)
	}

	fullPath := fmt.Sprintf("%s/%s", m.vgName, volumeName)
	//nolint:gosec,noctx // Path constructed from internal volume name
	cmd := exec.Command("lvs", "--units", "b", "--noheadings", "-o", "lv_name,lv_size,lv_attr", fullPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to get volume info: %w, output: %s", err, string(output))
	}

	// Parse output
	fields := strings.Fields(strings.TrimSpace(string(output)))
	if len(fields) < 3 {
		return nil, fmt.Errorf("unexpected lvs output format")
	}

	sizeStr := strings.TrimSuffix(fields[1], "B")
	sizeBytes, err := strconv.ParseInt(sizeStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse volume size: %w", err)
	}

	return &VolumeInfo{
		Name:       fields[0],
		SizeBytes:  sizeBytes,
		Attributes: fields[2],
	}, nil
}

// ListVolumes returns a list of all LVM volumes in the volume group
func (m *Manager) ListVolumes() ([]string, error) {
	//nolint:gosec,noctx // Volume group name is controlled internally
	cmd := exec.Command("lvs", "--noheadings", "-o", "lv_name", m.vgName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to list volumes: %w, output: %s", err, string(output))
	}

	var volumes []string
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		if volume := strings.TrimSpace(line); volume != "" {
			volumes = append(volumes, volume)
		}
	}

	return volumes, nil
}

// volumeExists checks if an LVM volume exists
func (m *Manager) volumeExists(volumeName string) bool {
	//nolint:gosec,noctx // Volume name is validated internally
	cmd := exec.Command("lvs", fmt.Sprintf("%s/%s", m.vgName, volumeName))
	return cmd.Run() == nil
}

// validateExistingVolume checks if an existing volume is compatible for reuse
func (m *Manager) validateExistingVolume(volumeName string, requiredSizeGB int) error {
	info, err := m.GetVolumeInfo(volumeName)
	if err != nil {
		return fmt.Errorf("failed to get volume info: %w", err)
	}

	// Check size (allow some tolerance for filesystem overhead)
	requiredSizeBytes := int64(requiredSizeGB) * 1024 * 1024 * 1024
	actualSizeBytes := info.SizeBytes

	// Allow 5% variance for filesystem/formatting differences
	sizeTolerance := requiredSizeBytes / 20 // 5%
	minSize := requiredSizeBytes - sizeTolerance

	if actualSizeBytes < minSize {
		return fmt.Errorf("existing volume size %d bytes too small, need at least %d bytes",
			actualSizeBytes, minSize)
	}
	// Note: Larger volumes are acceptable - no need to reject them

	// Check if volume is active/available
	if info.Attributes[4] != 'a' && info.Attributes[4] != '-' {
		return fmt.Errorf("volume state '%c' not suitable for reuse", info.Attributes[4])
	}

	return nil
}

// VolumeInfo represents information about an LVM volume
type VolumeInfo struct {
	Name       string
	SizeBytes  int64
	Attributes string
}
