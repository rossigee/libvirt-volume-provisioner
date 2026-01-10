// Package lvm provides functionality for managing LVM (Logical Volume Manager)
// volumes including creation, conversion, and removal operations.
package lvm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/rossigee/libvirt-volume-provisioner/internal/retry"
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
		vgName = "data" // Default if not provided
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
func (m *Manager) CreateVolume(ctx context.Context, volumeName string, sizeGB int) error {
	// Check if volume already exists
	if m.volumeExists(volumeName) {
		return fmt.Errorf("volume %s already exists", volumeName)
	}

	// Wrap with retry logic
	err := retry.WithRetry(ctx, m.retryConfig, func() error {
		return m.createVolumeOnce(volumeName, sizeGB)
	})
	if err != nil {
		return fmt.Errorf("failed to create volume %s after retries: %w", volumeName, err)
	}
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
	// Wrap with retry logic
	err := retry.WithRetry(ctx, m.retryConfig, func() error {
		return m.populateVolumeOnce(imagePath, volumeName, imageType, updater)
	})
	if err != nil {
		return fmt.Errorf("failed to populate volume %s after retries: %w", volumeName, err)
	}
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

	// Convert image format if needed and copy to LVM volume
	var cmd *exec.Cmd
	switch imageType {
	case "qcow2":
		// Convert QCOW2 to raw format directly to LVM device
		//nolint:gosec,noctx // Image path is provided by caller, device path is internal
		cmd = exec.Command("qemu-img", "convert", "-f", "qcow2", "-O", "raw", imagePath, devicePath)
	case "raw":
		// Direct copy for raw images
		//nolint:gosec,noctx // Image path is provided by caller, device path is internal
		cmd = exec.Command("dd", "if="+imagePath, "of="+devicePath, "bs=4M", "status=progress", "conv=fdatasync")
	default:
		return fmt.Errorf("unsupported image type: %s", imageType)
	}

	// Execute conversion with progress tracking
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to populate LVM volume: %w, output: %s", err, string(output))
	}

	// Update progress
	if updater != nil {
		updater.UpdateProgress("converting", 90, 0, 0)
	}

	return nil
}

// DeleteVolume deletes an LVM volume
func (m *Manager) DeleteVolume(volumeName string) error {
	if !m.volumeExists(volumeName) {
		return fmt.Errorf("volume %s does not exist", volumeName)
	}

	//nolint:gosec,noctx // Volume name is validated internally
	cmd := exec.Command("lvremove", "-f", fmt.Sprintf("%s/%s", m.vgName, volumeName))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to delete LVM volume: %w, output: %s", err, string(output))
	}

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

// VolumeInfo represents information about an LVM volume
type VolumeInfo struct {
	Name       string
	SizeBytes  int64
	Attributes string
}
