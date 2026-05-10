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

	qcow2reader "github.com/lima-vm/go-qcow2reader"
	"github.com/lima-vm/go-qcow2reader/image/qcow2"
	"github.com/rossigee/libvirt-volume-provisioner/internal/config"
	"github.com/rossigee/libvirt-volume-provisioner/internal/retry"
	"github.com/rossigee/libvirt-volume-provisioner/internal/storage"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// v0.9: sudoCmd removed - using direct exec.Command
// ProgressUpdater interface for updating job progress
type ProgressUpdater interface {
	UpdateProgress(stage string, percent float64, bytesProcessed, bytesTotal int64)
}

// Manager handles LVM operations
type Manager struct {
	vgName      string
	retryConfig retry.Config
}

// NewManager creates a new LVM manager from the provided configuration.
func NewManager(cfg config.LVMConfig) (*Manager, error) {
	if strings.ContainsAny(cfg.VolumeGroup, "/\\") {
		return nil, fmt.Errorf("invalid volume group name %q: must not contain path separators", cfg.VolumeGroup)
	}

	cmd := exec.CommandContext(context.Background(), "vgs", cfg.VolumeGroup)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("volume group %q does not exist or is not accessible: %w", cfg.VolumeGroup, err)
	}

	if _, err := exec.LookPath("lvcreate"); err != nil {
		return nil, fmt.Errorf("lvcreate command not found: %w", err)
	}
	if _, err := exec.LookPath("qemu-img"); err != nil {
		return nil, fmt.Errorf("qemu-img command not found: %w", err)
	}

	delays := make([]time.Duration, len(cfg.RetryBackoffMS))
	for i, ms := range cfg.RetryBackoffMS {
		delays[i] = time.Duration(ms) * time.Millisecond
	}

	return &Manager{
		vgName: cfg.VolumeGroup,
		retryConfig: retry.Config{
			MaxAttempts: cfg.RetryAttempts,
			Delays:      delays,
		},
	}, nil
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
	if m.volumeExists(ctx, volumeName) {
		// Validate existing volume
		if err := m.validateExistingVolume(ctx, volumeName, sizeGB); err != nil {
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
		return m.createVolumeOnce(ctx, volumeName, sizeGB)
	})
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("failed to create volume %s after retries: %w", volumeName, err)
	}
	span.SetStatus(codes.Ok, "volume created successfully")
	return nil
}

// createVolumeOnce performs a single LVM volume creation attempt
func (m *Manager) createVolumeOnce(ctx context.Context, volumeName string, sizeGB int) error {
	// Create LVM volume
	cmd := exec.CommandContext(ctx, "lvcreate",
		"-L", fmt.Sprintf("%dG", sizeGB),
		"-n", volumeName, m.vgName)
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
	store *storage.Store,
	jobID string,
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
		return m.populateVolumeOnce(ctx, imagePath, volumeName, imageType, updater, store, jobID)
	})
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("failed to populate volume %s after retries: %w", volumeName, err)
	}
	span.SetStatus(codes.Ok, "volume populated successfully")
	return nil
}

// qcow2ConvertArgs returns the qemu-img arguments to convert a qcow2 image
// to raw and write it directly to devicePath. Extracted so tests can verify
// the correct output target is used without running the command.
func qcow2ConvertArgs(imagePath, devicePath string) []string {
	return []string{"convert", "-f", "qcow2", "-O", "raw", imagePath, devicePath}
}

// populateVolumeOnce performs a single volume population attempt
func (m *Manager) populateVolumeOnce(
	ctx context.Context,
	imagePath, volumeName, imageType string,
	updater ProgressUpdater, store *storage.Store, jobID string,
) error {
	// Get the device path for the LVM volume
	devicePath := fmt.Sprintf("/dev/%s/%s", m.vgName, volumeName)

	// Verify the block device exists
	if fi, err := os.Stat(devicePath); err != nil || fi.Mode()&os.ModeDevice == 0 {
		return fmt.Errorf("LVM volume device does not exist: %s", devicePath)
	}

	// Check if volume already has content by looking for filesystem
	blkidCmd := exec.CommandContext(ctx, "blkid", "-o", "value", "-s", "TYPE", devicePath)
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
	var imageSize int64
	switch imageType {
	case "qcow2":
		// Read virtual size from qcow2 header — this is the uncompressed bytes that
		// will be written to the block device, not the compressed on-disk file size.
		if err := func() error {
			imgFile, err := os.Open(imagePath)
			if err != nil {
				return fmt.Errorf("failed to open image file %s: %w", imagePath, err)
			}
			defer func() {
				if err := imgFile.Close(); err != nil {
					logrus.WithError(err).Warn("failed to close image file after header read")
				}
			}()
			qimg, err := qcow2reader.OpenWithType(imgFile, qcow2.Type)
			if err != nil {
				return fmt.Errorf("failed to read qcow2 header from %s: %w", imagePath, err)
			}
			defer func() {
				if err := qimg.Close(); err != nil {
					logrus.WithError(err).Warn("failed to close qcow2 image after header read")
				}
			}()
			imageSize = qimg.Size()
			return nil
		}(); err != nil {
			return err
		}

		// Write directly to the device path. Avoid using '-' (stdout) as the output
		// target: in QEMU ≥10.1.0, stdout-based conversion is broken — '-p' progress
		// goes to stdout corrupting the stream, and without '-p' qemu-img writes
		// 0 bytes. Writing directly to the block device path avoids both issues.
		cmd = exec.CommandContext(ctx, "qemu-img", qcow2ConvertArgs(imagePath, devicePath)...)
	case "raw":
		// Direct copy for raw images
		cmd = exec.CommandContext(ctx, "dd", "if="+imagePath, "of="+devicePath, "bs=4M", "status=progress", "conv=fdatasync")
		rawStat, statErr := os.Stat(imagePath)
		if statErr != nil {
			return fmt.Errorf("failed to stat image file: %w", statErr)
		}
		imageSize = rawStat.Size()
	default:
		return fmt.Errorf("unsupported image type: %s", imageType)
	}

	// Execute conversion with time-based progress tracking.
	logrus.WithFields(logrus.Fields{
		"volume_name": volumeName,
		"command":     strings.Join(cmd.Args, " "),
	}).Info("Executing volume population command")

	startTime := time.Now()

	// Capture stderr for error reporting; for non-qcow2 types also capture stdout.
	var outBuf bytes.Buffer
	if cmd.Stdout == nil {
		cmd.Stdout = &outBuf
	}
	cmd.Stderr = &outBuf

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start command: %w", err)
	}

	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()

	// Use stored convert rate to estimate duration for incremental progress ticks.
	// Use conservative 50 MB/s default (SSD write speed for large sequential writes)
	const defaultConvertRate float64 = 50 * 1024 * 1024 // 50 MB/s
	var estimatedSeconds float64

	// Guard against zero/empty image - no progress to report if there's no data
	if imageSize > 0 {
		estimatedSeconds = float64(imageSize) / defaultConvertRate // default estimate
		if store != nil {
			if rate := store.GetAverageRate(ctx, "convert", defaultConvertRate); rate > 0 {
				estimatedSeconds = float64(imageSize) / rate // use historical rate if available
			}
		}
	}
	// If imageSize is 0 or estimate is somehow invalid, use minimum 1 second
	if estimatedSeconds <= 0 {
		estimatedSeconds = 1
	}

	logrus.WithFields(logrus.Fields{
		"volume_name":   volumeName,
		"image_size":    imageSize,
		"estimated_sec": estimatedSeconds,
		"default_rate":  defaultConvertRate,
	}).Info("Starting volume conversion with progress tracking")

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var executionErr error
loop:
	for {
		select {
		case executionErr = <-waitDone:
			break loop
		case <-ticker.C:
			if updater != nil {
				elapsed := time.Since(startTime).Seconds()
				pct := min(elapsed/estimatedSeconds*100, 99.0)
				processed := int64(float64(imageSize) * pct / 100)
				// Log progress at INFO level every 5 seconds to diagnose 0% issue
				if int(elapsed)%5 == 0 {
					logrus.WithFields(logrus.Fields{
						"elapsed_sec":   elapsed,
						"estimated_sec": estimatedSeconds,
						"percent":       pct,
						"image_size":    imageSize,
					}).Info("Convert progress debug")
				}
				updater.UpdateProgress("converting", pct, processed, imageSize)
			}
		}
	}

	output := outBuf.Bytes()
	if executionErr != nil {
		exitCode := -1
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		}
		logrus.WithFields(logrus.Fields{
			"volume_name": volumeName,
			"error":       executionErr,
			"exit_code":   exitCode,
			"output":      string(output),
		}).Error("Volume population command failed")
		return fmt.Errorf("failed to populate LVM volume: %w, output: %s", executionErr, string(output))
	}

	logrus.WithFields(logrus.Fields{
		"volume_name": volumeName,
	}).Info("Volume population command completed successfully")

	// Calculate and save rate.
	duration := time.Since(startTime)
	rateBPS := float64(imageSize) / duration.Seconds()
	if store != nil {
		saveErr := store.SaveStageRate(ctx, storage.StageRate{
			Stage:          "convert",
			RateBPS:        rateBPS,
			BytesProcessed: imageSize,
			DurationMS:     duration.Milliseconds(),
			JobID:          jobID,
			CreatedAt:      time.Now(),
		})
		if saveErr != nil {
			logrus.WithError(saveErr).Warn("failed to save stage rate")
		}
	}

	if updater != nil {
		updater.UpdateProgress("converting", 100, imageSize, imageSize)
	}

	return nil
}

// DeleteVolume deletes an LVM volume
func (m *Manager) DeleteVolume(ctx context.Context, volumeName string) error {
	// Start span for LVM volume deletion
	_, span := otel.Tracer("libvirt-volume-provisioner").Start(ctx, "DeleteVolume",
		trace.WithAttributes(
			attribute.String("lvm.volume_name", volumeName),
			attribute.String("lvm.vg_name", m.vgName)))
	defer span.End()

	if !m.volumeExists(ctx, volumeName) {
		span.SetStatus(codes.Error, "volume does not exist")
		return fmt.Errorf("volume %s does not exist", volumeName)
	}

	cmd := exec.CommandContext(ctx, "lvremove", "-f", fmt.Sprintf("%s/%s", m.vgName, volumeName))
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
func (m *Manager) GetVolumeInfo(ctx context.Context, volumeName string) (*VolumeInfo, error) {
	if !m.volumeExists(ctx, volumeName) {
		return nil, fmt.Errorf("volume %s does not exist", volumeName)
	}

	fullPath := fmt.Sprintf("%s/%s", m.vgName, volumeName)
	cmd := exec.CommandContext(ctx, "lvs", "--units", "b", "--noheadings",
		"-o", "lv_name,lv_size,lv_attr", fullPath)
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
	cmd := exec.CommandContext(context.Background(), "lvs", "--noheadings", "-o", "lv_name", m.vgName)
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
func (m *Manager) volumeExists(ctx context.Context, volumeName string) bool {
	cmd := exec.CommandContext(ctx, "lvs", fmt.Sprintf("%s/%s", m.vgName, volumeName))
	return cmd.Run() == nil
}

// validateExistingVolume checks if an existing volume is compatible for reuse
func (m *Manager) validateExistingVolume(ctx context.Context, volumeName string, requiredSizeGB int) error {
	info, err := m.GetVolumeInfo(ctx, volumeName)
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

	// Check if volume is active/available (field 4 of lvs attribute string)
	if len(info.Attributes) < 5 {
		return fmt.Errorf("unexpected lvs attribute format %q", info.Attributes)
	}
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
