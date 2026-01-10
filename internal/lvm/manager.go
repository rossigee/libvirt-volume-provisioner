// Package lvm provides functionality for managing LVM (Logical Volume Manager)
// volumes including creation, conversion, and removal operations.
package lvm

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// ProgressUpdater interface for updating job progress
type ProgressUpdater interface {
	UpdateProgress(stage string, percent float64, bytesProcessed, bytesTotal int64)
}

// Manager handles LVM operations
type Manager struct {
	vgName string
}

// NewManager creates a new LVM manager
func NewManager() (*Manager, error) {
	vgName := "data" // Default volume group name

	// Verify LVM commands are available
	if _, err := exec.LookPath("lvcreate"); err != nil {
		return nil, fmt.Errorf("lvcreate command not found: %w", err)
	}
	if _, err := exec.LookPath("qemu-img"); err != nil {
		return nil, fmt.Errorf("qemu-img command not found: %w", err)
	}

	return &Manager{
		vgName: vgName,
	}, nil
}

// CreateVolume creates a new LVM volume
func (m *Manager) CreateVolume(volumeName string, sizeGB int) error {
	// Check if volume already exists
	if m.volumeExists(volumeName) {
		return fmt.Errorf("volume %s already exists", volumeName)
	}

	// Create LVM volume
	//nolint:gosec,noctx // LVM command parameters are validated and controlled internally
	cmd := exec.Command("lvcreate", "-L", fmt.Sprintf("%dG", sizeGB), "-n", volumeName, m.vgName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to create LVM volume: %w, output: %s", err, string(output))
	}

	return nil
}

// PopulateVolume populates an LVM volume with image data
func (m *Manager) PopulateVolume(imagePath, volumeName, imageType string, updater ProgressUpdater) error {
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
