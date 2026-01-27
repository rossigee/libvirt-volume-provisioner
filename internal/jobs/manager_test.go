package jobs

import (
	"testing"

	"github.com/rossigee/libvirt-volume-provisioner/pkg/types"
	"github.com/stretchr/testify/assert"
)

// TestJobUpdateProgress tests the progress update functionality
func TestJobUpdateProgress(t *testing.T) {
	job := &Job{
		ID: "test-job",
		Request: types.ProvisionRequest{
			ImageURL:     "http://example.com/image.qcow2",
			VolumeName:   "test-volume",
			VolumeSizeGB: 10,
			ImageType:    "qcow2",
		},
	}

	// Test that progress can be updated
	job.UpdateProgress("initializing", 25.0, 256, 1024)

	if job.Progress == nil {
		t.Error("Expected progress to be set")
		return
	}

	if job.Progress.Stage != "initializing" {
		t.Errorf("Expected stage 'initializing', got '%s'", job.Progress.Stage)
	}

	if job.Progress.Percent != 25.0 {
		t.Errorf("Expected percent 25.0, got %f", job.Progress.Percent)
	}

	if job.Progress.BytesProcessed != 256 {
		t.Errorf("Expected bytes processed 256, got %d", job.Progress.BytesProcessed)
	}

	if job.Progress.BytesTotal != 1024 {
		t.Errorf("Expected bytes total 1024, got %d", job.Progress.BytesTotal)
	}
}

// TestJobStatusInitialization tests that jobs are initialized with correct default values
func TestJobStatusInitialization(t *testing.T) {
	job := &Job{
		ID: "test-job",
		Request: types.ProvisionRequest{
			ImageURL:     "http://example.com/image.qcow2",
			VolumeName:   "test-volume",
			VolumeSizeGB: 10,
			ImageType:    "qcow2",
		},
	}

	// Test that job has proper defaults
	if job.ID != "test-job" {
		t.Errorf("Expected job ID 'test-job', got '%s'", job.ID)
	}

	if job.Status != "" {
		t.Errorf("Expected empty status, got '%s'", job.Status)
	}

	if job.Progress != nil {
		t.Error("Expected progress to be nil initially")
	}

	if job.Error != nil {
		t.Error("Expected error to be nil initially")
	}

	if job.CacheHit {
		t.Error("Expected cache hit to be false initially")
	}
}

// TestGetJobCacheInfo tests getting cache info for completed jobs
func TestGetJobCacheInfo(t *testing.T) {
	manager := &Manager{
		jobs:      make(map[string]*Job),
		semaphore: make(chan struct{}, 2),
	}

	manager.jobs["completed-job"] = &Job{
		ID:        "completed-job",
		Status:    types.StatusCompleted,
		CacheHit:  true,
		ImagePath: "/var/lib/libvirt/images/ubuntu_image",
	}

	cacheHit, imagePath, err := manager.GetJobCacheInfo("completed-job")

	assert.NoError(t, err)
	assert.True(t, cacheHit)
	assert.Equal(t, "/var/lib/libvirt/images/ubuntu_image", imagePath)
}

// TestGetJobCacheInfoNotCompleted tests that getting cache info for non-completed job fails
func TestGetJobCacheInfoNotCompleted(t *testing.T) {
	manager := &Manager{
		jobs:      make(map[string]*Job),
		semaphore: make(chan struct{}, 2),
	}

	manager.jobs["running-job"] = &Job{
		ID:     "running-job",
		Status: types.StatusRunning,
	}

	_, _, err := manager.GetJobCacheInfo("running-job")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not completed")
}

// TestGetJobCacheInfoNotFound tests that getting cache info for non-existent job fails
func TestGetJobCacheInfoNotFound(t *testing.T) {
	manager := &Manager{
		jobs:      make(map[string]*Job),
		semaphore: make(chan struct{}, 2),
	}

	_, _, err := manager.GetJobCacheInfo("nonexistent-job")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// TestCleanupCompletedJobs removes old completed jobs beyond limit
func TestCleanupCompletedJobs(t *testing.T) {
	manager := &Manager{
		jobs:      make(map[string]*Job),
		semaphore: make(chan struct{}, 2),
	}

	// Add 102 completed jobs (more than the 100 job limit)
	for i := 1; i <= 102; i++ {
		jobID := "job-completed-" + string(rune(i))
		manager.jobs[jobID] = &Job{
			ID:     jobID,
			Status: types.StatusCompleted,
		}
	}

	// Add some non-completed jobs that should not be deleted
	manager.jobs["running-job"] = &Job{
		ID:     "running-job",
		Status: types.StatusRunning,
	}

	assert.Equal(t, 103, len(manager.jobs))

	// Cleanup completed jobs
	manager.CleanupCompletedJobs()

	// Should keep 100 most recent completed jobs + 1 non-completed
	assert.LessOrEqual(t, len(manager.jobs), 102)
	// Verify running job still exists
	assert.NotNil(t, manager.jobs["running-job"])
}

// TestGetActiveJobs returns correct count of active jobs
func TestGetActiveJobs(t *testing.T) {
	manager := &Manager{
		jobs:      make(map[string]*Job),
		semaphore: make(chan struct{}, 2),
	}

	// Add some jobs with different statuses
	manager.jobs["running1"] = &Job{
		ID:     "running1",
		Status: types.StatusRunning,
	}
	manager.jobs["running2"] = &Job{
		ID:     "running2",
		Status: types.StatusRunning,
	}
	manager.jobs["pending1"] = &Job{
		ID:     "pending1",
		Status: types.StatusPending,
	}
	manager.jobs["completed1"] = &Job{
		ID:     "completed1",
		Status: types.StatusCompleted,
	}
	manager.jobs["failed1"] = &Job{
		ID:     "failed1",
		Status: types.StatusFailed,
	}

	activeCount := manager.GetActiveJobs()

	// Should count running and pending jobs only
	assert.Equal(t, 3, activeCount)
}
