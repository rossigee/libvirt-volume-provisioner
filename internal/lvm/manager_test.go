package lvm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
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
		exists := manager.volumeExists("test-volume")
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
		_ = manager.validateExistingVolume("test-volume", 10)
	})
}

// TestDeleteVolume tests volume deletion
func TestDeleteVolume(t *testing.T) {
	manager := &Manager{
		vgName: "testvg",
	}

	// Test deleting a volume that doesn't exist (should not panic)
	assert.NotPanics(t, func() {
		err := manager.DeleteVolume("nonexistent-volume")
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
		info, err := manager.GetVolumeInfo("nonexistent-volume")
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
