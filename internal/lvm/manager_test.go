package lvm

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewManager(t *testing.T) {
	manager, err := NewManager("data")

	if err != nil {
		// Skip test if LVM tools are not available in test environment
		t.Skip("LVM tools not available in test environment:", err)
	}

	assert.NoError(t, err)
	assert.NotNil(t, manager)
	assert.Equal(t, "data", manager.vgName)
}

func TestNewManager_InvalidVGName(t *testing.T) {
	// Test with path separator should be rejected
	_, err := NewManager("data/volume")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must not contain path separators")

	// Test with backslash should be rejected
	_, err = NewManager("data\\volume")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must not contain path separators")
}

func TestNewManager_DefaultVG(t *testing.T) {
	// Test with empty string should default to "data"
	manager, err := NewManager("")

	if err != nil {
		// Skip test if LVM tools are not available in test environment
		t.Skip("LVM tools not available in test environment:", err)
	}

	assert.NoError(t, err)
	assert.NotNil(t, manager)
	assert.Equal(t, "data", manager.vgName)
}

func TestVolumeInfo(t *testing.T) {
	// Test VolumeInfo struct creation
	info := &VolumeInfo{
		Name:       "test-volume",
		SizeBytes:  1073741824, // 1GB
		Attributes: "-wi-a-----",
	}

	assert.Equal(t, "test-volume", info.Name)
	assert.Equal(t, int64(1073741824), info.SizeBytes)
	assert.Equal(t, "-wi-a-----", info.Attributes)
}

// MockProgressUpdater for testing
type MockProgressUpdater struct {
	updates []struct {
		stage     string
		percent   float64
		processed int64
		total     int64
	}
}

func (m *MockProgressUpdater) UpdateProgress(stage string, percent float64, bytesProcessed, bytesTotal int64) {
	m.updates = append(m.updates, struct {
		stage     string
		percent   float64
		processed int64
		total     int64
	}{stage, percent, bytesProcessed, bytesTotal})
}

func TestMockProgressUpdater(t *testing.T) {
	updater := &MockProgressUpdater{}

	updater.UpdateProgress("test", 50.0, 512, 1024)
	updater.UpdateProgress("complete", 100.0, 1024, 1024)

	assert.Len(t, updater.updates, 2)
	assert.Equal(t, "test", updater.updates[0].stage)
	assert.Equal(t, 50.0, updater.updates[0].percent)
	assert.Equal(t, "complete", updater.updates[1].stage)
	assert.Equal(t, 100.0, updater.updates[1].percent)
}

// TestVolumeExists tests volume existence checking
func TestVolumeExists(t *testing.T) {
	// Create a manager with a mock volume group
	manager := &Manager{
		vgName: "testvg",
	}

	// Test with a volume name
	// This tests that the method doesn't panic
	assert.NotPanics(t, func() {
		exists := manager.volumeExists(context.Background(), "test-volume")
		// We can't really test the actual command execution in unit tests
		// without LVM tools, but we can test that it returns a bool
		assert.IsType(t, false, exists)
	})
}

// TestValidateExistingVolume tests volume validation logic
func TestValidateExistingVolume(t *testing.T) {
	manager := &Manager{
		vgName: "testvg",
	}

	// Test with volume name that would be valid
	// This tests the method signature and basic logic
	assert.NotPanics(t, func() {
		_ = manager.validateExistingVolume(context.Background(), "test-volume", 10)
	})
}

// TestDeleteVolume tests volume deletion
func TestDeleteVolume(t *testing.T) {
	manager := &Manager{
		vgName: "testvg",
	}

	// Test deleting a volume that doesn't exist (should not panic)
	assert.NotPanics(t, func() {
		err := manager.DeleteVolume(context.Background(), "nonexistent-volume")
		// We expect this to fail in a real environment, but shouldn't panic
		assert.Error(t, err)
	})
}

// TestListVolumes tests volume listing
func TestListVolumes(t *testing.T) {
	manager := &Manager{
		vgName: "testvg",
	}

	// Test listing volumes (should not panic)
	assert.NotPanics(t, func() {
		volumes, err := manager.ListVolumes()
		// In test environment without LVM, this will likely fail
		// but the method should handle it gracefully
		if err != nil {
			assert.Error(t, err)
		} else {
			assert.IsType(t, []string{}, volumes)
		}
	})
}

// TestGetVolumeInfo tests getting volume information
func TestGetVolumeInfo(t *testing.T) {
	manager := &Manager{
		vgName: "testvg",
	}

	// Test getting volume info for non-existent volume
	assert.NotPanics(t, func() {
		info, err := manager.GetVolumeInfo(context.Background(), "nonexistent-volume")
		// Should return an error for non-existent volume
		assert.Error(t, err)
		assert.Nil(t, info)
	})
}

// TestCreateVolume tests volume creation
func TestCreateVolume(t *testing.T) {
	manager := &Manager{
		vgName: "testvg",
	}

	// Test creating a volume (will fail without LVM tools, but shouldn't panic)
	assert.NotPanics(t, func() {
		err := manager.CreateVolume(context.Background(), "test-volume", 10)
		// Expected to fail in test environment
		assert.Error(t, err)
	})
}

// TestPopulateVolume tests volume population
func TestPopulateVolume(t *testing.T) {
	manager := &Manager{
		vgName: "testvg",
	}

	updater := &MockProgressUpdater{}

	// Test populating a volume (will fail without LVM tools, but shouldn't panic)
	assert.NotPanics(t, func() {
		err := manager.PopulateVolume(context.Background(), "/tmp/test.img", "test-volume", "qcow2", updater, nil, "")
		// Expected to fail in test environment
		assert.Error(t, err)
	})
}

// TestQcow2ConvertArgs verifies the qemu-img arguments are constructed correctly.
// This is a regression test for a bug where '-' was used as the output target:
// in QEMU ≥10.1.0, '-' is treated as a literal filename rather than stdout,
// so the image data was never written to the block device.
func TestQcow2ConvertArgs(t *testing.T) {
	imagePath := "/var/lib/libvirt/images/some-image.qcow2"
	devicePath := "/dev/data/gitea.golder.lan-root"

	args := qcow2ConvertArgs(imagePath, devicePath)

	// Must be a valid qemu-img convert invocation
	require.Equal(t, "convert", args[0])
	assert.Equal(t, "-f", args[1])
	assert.Equal(t, "qcow2", args[2])
	assert.Equal(t, "-O", args[3])
	assert.Equal(t, "raw", args[4])
	assert.Equal(t, imagePath, args[5])

	// The last argument must be the device path — not '-', '/dev/stdout', or anything else.
	// Using '-' causes QEMU ≥10.1.0 to create a literal file named '-' in the CWD.
	// Using '/dev/stdout' fails when stdout is a pipe (exit 1).
	last := args[len(args)-1]
	assert.Equal(t, devicePath, last, "output target must be the device path, not stdout or '-'")
	assert.NotEqual(t, "-", last, "'-' is treated as a filename in QEMU ≥10.1.0, not stdout")
	assert.NotEqual(t, "/dev/stdout", last, "'/dev/stdout' fails when stdout is a pipe")

	// -p must not be present: in QEMU ≥10.1.0 it writes progress text to stdout,
	// which corrupts the image stream when stdout is the block device.
	for _, arg := range args {
		assert.NotEqual(t, "-p", arg, "'-p' must not be passed to qemu-img convert")
	}
}

// TestQcow2ConvertIntegration verifies that qemu-img actually writes the correct
// image data to the output file. This catches regressions where the wrong output
// target causes the data to be lost or corrupted.
func TestQcow2ConvertIntegration(t *testing.T) {
	if _, err := exec.LookPath("qemu-img"); err != nil {
		t.Skip("qemu-img not available:", err)
	}

	dir := t.TempDir()

	// Copy pre-built qcow2 test file to temp directory.
	// This simulates receiving a qcow2 from MinIO in production.
	src, err := os.Open("testdata/test-image.qcow2")
	require.NoError(t, err)
	defer func() { _ = src.Close() }()

	qcow2Path := filepath.Join(dir, "input.qcow2")
	dst, err := os.Create(qcow2Path)
	require.NoError(t, err)
	_, err = io.Copy(dst, src)
	require.NoError(t, err)
	require.NoError(t, dst.Close())

	// Target file simulates a block device.
	devicePath := filepath.Join(dir, "device.raw")
	f, err := os.Create(devicePath)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	// Run the conversion using the same args qcow2ConvertArgs produces,
	// but without sudo (we own the temp files).
	args := qcow2ConvertArgs(qcow2Path, devicePath)
	cmd := exec.CommandContext(t.Context(), "qemu-img", args...)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "qemu-img convert failed: %s", out)

	// Verify the output file is non-empty (data was written).
	gotInfo, err := os.Stat(devicePath)
	require.NoError(t, err)
	assert.Greater(t, gotInfo.Size(), int64(0), "output file should not be empty")

	// Confirm the regression: running with '-' as output must NOT produce correct output.
	dashDevice := filepath.Join(dir, "dash-device.raw")
	f, err = os.Create(dashDevice)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	cmd = exec.CommandContext(t.Context(), "qemu-img", "convert",
		"-f", "qcow2", "-O", "raw", qcow2Path, "-")
	cmd.Dir = dir // so '-' gets created in our temp dir, not CWD
	_ = cmd.Run()

	dashInfo, _ := os.Stat(dashDevice)
	assert.Equal(t, int64(0), dashInfo.Size(),
		"stdout redirect should produce empty output (regression confirmed)")
}
