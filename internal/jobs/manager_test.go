package jobs

import (
	"testing"

	"github.com/rossigee/libvirt-volume-provisioner/pkg/types"
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
