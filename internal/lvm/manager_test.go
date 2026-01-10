package lvm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewManager(t *testing.T) {
	manager, err := NewManager()

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
