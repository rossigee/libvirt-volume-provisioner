package jobs

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rossigee/libvirt-volume-provisioner/internal/libvirt"
	"github.com/rossigee/libvirt-volume-provisioner/internal/storage"
	"github.com/rossigee/libvirt-volume-provisioner/pkg/types"
	"github.com/stretchr/testify/assert"
)

// mockLibvirtPool implements LibvirtPool for unit tests.
type mockLibvirtPool struct {
	listErr      error
	evictErr     error
	evictedCount int
	deletedPaths []string
}

//nolint:nilnil // matches real CheckCache "not found" semantic
func (m *mockLibvirtPool) CheckCache(_ string) (*libvirt.ImageCache, error) { return nil, nil }
func (m *mockLibvirtPool) AllocateImageFile(cacheKey string) (string, error) {
	return "/tmp/cache/" + cacheKey, nil
}
func (m *mockLibvirtPool) CreateCacheEntry(_, _ string) error { return nil }
func (m *mockLibvirtPool) DeleteImage(imagePath string) error {
	m.deletedPaths = append(m.deletedPaths, imagePath)
	return nil
}
func (m *mockLibvirtPool) ListCachedImages() ([]*libvirt.ImageCache, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return []*libvirt.ImageCache{}, nil
}
func (m *mockLibvirtPool) EvictExpiredImages(_ time.Duration) (int, error) {
	return m.evictedCount, m.evictErr
}

// TestJobUpdateProgress tests the weighted progress mapping in UpdateProgress.
// downloadWeight and convertWeight are fractions (0–1) that together sum to 1.
// "downloading" maps stagePercent into [0, downloadWeight*100].
// "converting"  maps stagePercent into [downloadWeight*100, 100].
// All other stages pass stagePercent through unchanged.
func TestJobUpdateProgress(t *testing.T) {
	job := &Job{
		ID:             "test-job",
		downloadWeight: 0.6,
		convertWeight:  0.4,
	}

	// Default stage: overall == stagePercent unchanged.
	job.UpdateProgress("initializing", 0, 0, 0)
	assert.NotNil(t, job.Progress)
	assert.Equal(t, "initializing", job.Progress.Stage)
	assert.Equal(t, 0.0, job.Progress.OverallPercent)

	// Download start: 0% of stage → 0% overall.
	job.UpdateProgress("downloading", 0, 0, 1024)
	assert.Equal(t, "downloading", job.Progress.Stage)
	assert.InDelta(t, 0.0, job.Progress.OverallPercent, 0.001)

	// Download mid: 50% of stage → 30% overall (50 * 0.6).
	job.UpdateProgress("downloading", 50.0, 512, 1024)
	assert.InDelta(t, 30.0, job.Progress.OverallPercent, 0.001)
	assert.Equal(t, int64(512), job.Progress.BytesProcessed)
	assert.Equal(t, int64(1024), job.Progress.BytesTotal)

	// Download complete: 100% of stage → 60% overall (100 * 0.6).
	job.UpdateProgress("downloading", 100.0, 1024, 1024)
	assert.InDelta(t, 60.0, job.Progress.OverallPercent, 0.001)

	// Convert start: 0% of stage → 60% overall (downloadWeight*100 + 0*0.4).
	job.UpdateProgress("converting", 0, 0, 1024)
	assert.Equal(t, "converting", job.Progress.Stage)
	assert.InDelta(t, 60.0, job.Progress.OverallPercent, 0.001)

	// Convert mid: 50% of stage → 80% overall (60 + 50*0.4).
	job.UpdateProgress("converting", 50.0, 512, 1024)
	assert.InDelta(t, 80.0, job.Progress.OverallPercent, 0.001)

	// Convert complete: 100% of stage → 100% overall (60 + 100*0.4).
	job.UpdateProgress("converting", 100.0, 1024, 1024)
	assert.InDelta(t, 100.0, job.Progress.OverallPercent, 0.001)

	// Finalizing (default): 100 passed through unchanged.
	job.UpdateProgress("finalizing", 100.0, 0, 0)
	assert.Equal(t, "finalizing", job.Progress.Stage)
	assert.Equal(t, 100.0, job.Progress.OverallPercent)
}

// TestJobUpdateProgress_CacheHit verifies that when downloadWeight=0 and
// convertWeight=1 (set after a cache hit), converting spans the full 0–100% range.
func TestJobUpdateProgress_CacheHit(t *testing.T) {
	job := &Job{
		ID:             "test-job",
		downloadWeight: 0,
		convertWeight:  1,
	}

	job.UpdateProgress("converting", 0, 0, 1024)
	assert.InDelta(t, 0.0, job.Progress.OverallPercent, 0.001)

	job.UpdateProgress("converting", 50.0, 512, 1024)
	assert.InDelta(t, 50.0, job.Progress.OverallPercent, 0.001)

	job.UpdateProgress("converting", 100.0, 1024, 1024)
	assert.InDelta(t, 100.0, job.Progress.OverallPercent, 0.001)
}

// TestJobUpdateProgress_EqualWeights verifies the 50/50 fallback used when no
// rate history is available.
func TestJobUpdateProgress_EqualWeights(t *testing.T) {
	job := &Job{
		ID:             "test-job",
		downloadWeight: 0.5,
		convertWeight:  0.5,
	}

	job.UpdateProgress("downloading", 100.0, 1024, 1024)
	assert.InDelta(t, 50.0, job.Progress.OverallPercent, 0.001)

	job.UpdateProgress("converting", 100.0, 1024, 1024)
	assert.InDelta(t, 100.0, job.Progress.OverallPercent, 0.001)
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

// TestStartJob tests starting a new provisioning job
func TestStartJob(t *testing.T) {
	manager := &Manager{
		jobs:      make(map[string]*Job),
		semaphore: make(chan struct{}, 2),
		// Note: In a real implementation, minioClient, lvmManager, etc. would be mocked
		// For this unit test, we're only testing the job creation logic
	}

	req := types.ProvisionRequest{
		ImageURL:      "https://minio.example.com/images/ubuntu.qcow2",
		VolumeName:    "test-volume",
		VolumeSizeGB:  10,
		ImageType:     "qcow2",
		CorrelationID: "test-correlation-id",
	}

	jobID, err := manager.StartJob(context.Background(), req)

	assert.NoError(t, err)
	assert.NotEmpty(t, jobID)
	assert.Contains(t, manager.jobs, jobID)

	job := manager.jobs[jobID]
	assert.Equal(t, jobID, job.ID)
	assert.Equal(t, types.StatusPending, job.Status)
	assert.Equal(t, req, job.Request)
	assert.Equal(t, "test-correlation-id", job.CorrelationID)
	assert.NotZero(t, job.CreatedAt)
	assert.NotZero(t, job.UpdatedAt)
	assert.NotNil(t, job.cancelFunc)

	// Cancel the job immediately to prevent the goroutine from panicking due to nil dependencies
	job.cancelFunc()
}

// TestStartJob_MultipleJobs tests starting multiple jobs
func TestStartJob_MultipleJobs(t *testing.T) {
	manager := &Manager{
		jobs:      make(map[string]*Job),
		semaphore: make(chan struct{}, 2),
	}

	req1 := types.ProvisionRequest{
		ImageURL:     "https://minio.example.com/images/ubuntu.qcow2",
		VolumeName:   "test-volume-1",
		VolumeSizeGB: 10,
	}

	req2 := types.ProvisionRequest{
		ImageURL:     "https://minio.example.com/images/centos.qcow2",
		VolumeName:   "test-volume-2",
		VolumeSizeGB: 20,
	}

	jobID1, err1 := manager.StartJob(context.Background(), req1)
	jobID2, err2 := manager.StartJob(context.Background(), req2)

	assert.NoError(t, err1)
	assert.NoError(t, err2)
	assert.NotEqual(t, jobID1, jobID2)
	assert.Len(t, manager.jobs, 2)

	// Cancel jobs immediately to prevent goroutine panics due to nil dependencies
	manager.jobs[jobID1].cancelFunc()
	manager.jobs[jobID2].cancelFunc()
}

// TestGetJobStatus tests retrieving job status
func TestGetJobStatus(t *testing.T) {
	manager := &Manager{
		jobs:      make(map[string]*Job),
		semaphore: make(chan struct{}, 2),
	}

	job := &Job{
		ID:     "test-job",
		Status: types.StatusCompleted,
		Request: types.ProvisionRequest{
			ImageURL:     "https://minio.example.com/images/ubuntu.qcow2",
			VolumeName:   "test-volume",
			VolumeSizeGB: 10,
		},
		Progress: &types.ProgressInfo{
			Stage:          "completed",
			StagePercent:   100.0,
			OverallPercent: 100.0,
			BytesProcessed: 1024,
			BytesTotal:     1024,
		},
		CreatedAt: time.Now().Add(-time.Hour),
		UpdatedAt: time.Now(),
	}

	manager.jobs["test-job"] = job

	status, err := manager.GetJobStatus("test-job")

	assert.NoError(t, err)
	assert.Equal(t, "test-job", status.JobID)
	assert.Equal(t, types.StatusCompleted, status.Status)
	assert.Equal(t, job.Progress, status.Progress)
	assert.Equal(t, job.CreatedAt, status.CreatedAt)
	assert.Equal(t, job.UpdatedAt, status.UpdatedAt)
}

// TestGetJobStatus_NotFound tests getting status for non-existent job
func TestGetJobStatus_NotFound(t *testing.T) {
	manager := &Manager{
		jobs:      make(map[string]*Job),
		semaphore: make(chan struct{}, 2),
	}

	_, err := manager.GetJobStatus("nonexistent-job")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// TestCancelJob tests canceling a running job
func TestCancelJob(t *testing.T) {
	manager := &Manager{
		jobs:      make(map[string]*Job),
		semaphore: make(chan struct{}, 2),
	}

	cancelled := false
	cancelFunc := func() {
		cancelled = true
	}

	job := &Job{
		ID:         "test-job",
		Status:     types.StatusRunning,
		cancelFunc: cancelFunc,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	manager.jobs["test-job"] = job

	err := manager.CancelJob(context.Background(), "test-job")

	assert.NoError(t, err)
	assert.True(t, cancelled)
}

// TestCancelJob_NotFound tests canceling non-existent job
func TestCancelJob_NotFound(t *testing.T) {
	manager := &Manager{
		jobs:      make(map[string]*Job),
		semaphore: make(chan struct{}, 2),
	}

	err := manager.CancelJob(context.Background(), "nonexistent-job")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// TestCancelJob_AlreadyCompleted tests canceling already completed job
func TestCancelJob_AlreadyCompleted(t *testing.T) {
	manager := &Manager{
		jobs:      make(map[string]*Job),
		semaphore: make(chan struct{}, 2),
	}

	job := &Job{
		ID:     "test-job",
		Status: types.StatusCompleted,
	}

	manager.jobs["test-job"] = job

	err := manager.CancelJob(context.Background(), "test-job")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be cancelled")
}

// TestFetchImageToCache tests starting an image cache job
func TestFetchImageToCache(t *testing.T) {
	manager := &Manager{
		jobs:      make(map[string]*Job),
		semaphore: make(chan struct{}, 2),
	}

	req := types.FetchImageToCacheRequest{
		ImageURL: "https://minio.example.com/images/cache-image.qcow2",
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately to prevent goroutine panics
	jobID, err := manager.FetchImageToCache(ctx, req)

	assert.NoError(t, err)
	assert.NotEmpty(t, jobID)
	assert.Contains(t, manager.jobs, jobID)

	job := manager.jobs[jobID]
	assert.Equal(t, jobID, job.ID)
	assert.Equal(t, types.StatusPending, job.Status)
	assert.Equal(t, req.ImageURL, job.Request.ImageURL)
	assert.NotZero(t, job.CreatedAt)
	assert.NotZero(t, job.UpdatedAt)
}

// TestRecoverJobs tests the job recovery functionality
func TestRecoverJobs(t *testing.T) {
	// Create a manager with a working database
	store, err := storage.NewStore(":memory:")
	assert.NoError(t, err)

	manager := &Manager{
		jobs:  make(map[string]*Job),
		store: store,
	}

	// Create a job in the database that appears to be running
	runningJob := &Job{
		ID:     "test-recovery-job",
		Status: types.StatusRunning,
		Request: types.ProvisionRequest{
			ImageURL:     "http://example.com/test.qcow2",
			VolumeName:   "test-vol",
			VolumeSizeGB: 10,
		},
		CreatedAt: time.Now().Add(-time.Hour),
		UpdatedAt: time.Now().Add(-time.Hour),
	}

	err = store.SaveJob(context.Background(), &storage.JobRecord{
		ID:           runningJob.ID,
		Status:       string(runningJob.Status),
		RequestJSON:  `{"image_url":"http://example.com/test.qcow2","volume_name":"test-vol","volume_size_gb":10}`,
		ErrorMessage: "",
		CreatedAt:    runningJob.CreatedAt,
		UpdatedAt:    runningJob.UpdatedAt,
	})
	assert.NoError(t, err)

	// Recover jobs
	err = manager.RecoverJobs()
	assert.NoError(t, err)

	// Check that the job was marked as failed in the database
	record, err := store.GetJob(runningJob.ID)
	assert.NoError(t, err)
	assert.Equal(t, "failed", record.Status)
	assert.Contains(t, record.ErrorMessage, "daemon restarted")
}

// TestProvisionVolume_NoDependencies tests ProvisionVolume with missing dependencies
func TestProvisionVolume_NoDependencies(t *testing.T) {
	manager := &Manager{
		jobs: make(map[string]*Job),
		// Intentionally leave minioClient, lvmManager, etc. as nil
	}

	job := &Job{
		ID: "test-job",
		Request: types.ProvisionRequest{
			ImageURL:     "http://example.com/test.qcow2",
			VolumeName:   "test-volume",
			VolumeSizeGB: 10,
		},
	}

	err := manager.ProvisionVolume(context.Background(), job)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "dependencies not initialized")
}

// TestURLCacheKey verifies that urlCacheKey returns a stable, filesystem-safe hex string
// and never embeds the raw URL (which would produce invalid paths when used as a filename).
func TestURLCacheKey(t *testing.T) {
	urls := []string{
		"https://backups.golder.lan:9000/vmnode-images/runner.qcow2",
		"http://minio:9000/bucket/image.qcow2",
		"https://example.com/path/to/image.qcow2",
	}
	for _, u := range urls {
		key := urlCacheKey(u)
		// Must be a 64-char hex string (SHA256)
		assert.Len(t, key, 64)
		assert.Regexp(t, `^[0-9a-f]+$`, key)
		// Must not contain URL-unsafe characters
		assert.NotContains(t, key, ":")
		assert.NotContains(t, key, "/")
		assert.NotContains(t, key, "https")
		// Must be stable
		assert.Equal(t, key, urlCacheKey(u))
	}
	// Different URLs must produce different keys
	assert.NotEqual(t, urlCacheKey(urls[0]), urlCacheKey(urls[1]))
}

// TestParseChecksumFile verifies that both raw-hash and sha256sum(1) formats are accepted,
// and that invalid content is rejected. This is the regression test for the bug where
// sha256sum-format files were silently rejected (len > 64), causing fallback to URL hash.
func TestParseChecksumFile(t *testing.T) {
	validHash := "a1b2c3d4e5f6789012345678901234567890123456789012345678901234abcd"

	tests := []struct {
		name        string
		input       []byte
		expectHash  string
		expectError bool
	}{
		{
			name:       "raw hash",
			input:      []byte(validHash),
			expectHash: validHash,
		},
		{
			name:       "raw hash with trailing newline",
			input:      []byte(validHash + "\n"),
			expectHash: validHash,
		},
		{
			name:       "sha256sum format (hash  filename)",
			input:      []byte(validHash + "  image.qcow2\n"),
			expectHash: validHash,
		},
		{
			name:       "sha256sum format with star (hash *filename)",
			input:      []byte(validHash + " *image.qcow2\n"),
			expectHash: validHash,
		},
		{
			name:        "empty content",
			input:       []byte(""),
			expectError: true,
		},
		{
			name:        "too short",
			input:       []byte("abc123"),
			expectError: true,
		},
		{
			name:        "too long bare hash",
			input:       []byte(validHash + "extra"),
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseChecksumFile(tt.input)
			if tt.expectError {
				assert.Error(t, err)
				assert.Empty(t, got)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectHash, got)
			}
		})
	}
}

// TestGetImageChecksum_NoMinIO tests getImageChecksum with no MinIO client
func TestGetImageChecksum_NoMinIO(t *testing.T) {
	manager := &Manager{
		// No minioClient
	}

	checksum, err := manager.getImageChecksum(context.Background(), "http://example.com/test.qcow2")
	assert.Error(t, err)
	assert.Empty(t, checksum)
}

// TestGetOrDownloadImage_NoDependencies tests getOrDownloadImage with missing dependencies
func TestGetOrDownloadImage_NoDependencies(t *testing.T) {
	manager := &Manager{
		jobs: make(map[string]*Job),
		// Missing dependencies
	}

	req := types.ProvisionRequest{
		ImageURL:     "http://example.com/test.qcow2",
		VolumeName:   "test-volume",
		VolumeSizeGB: 10,
	}

	job := &Job{
		ID:      "test-job",
		Request: req,
	}

	_, err := manager.getOrDownloadImage(context.Background(), req, job)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "dependencies not initialized")
}

// TestRunJob_NoDependencies tests runJob with missing dependencies in test environment
func TestRunJob_NoDependencies_TestEnv(t *testing.T) {
	manager := &Manager{
		jobs: make(map[string]*Job),
		// Missing dependencies
	}

	job := &Job{
		ID: "test-job",
		Request: types.ProvisionRequest{
			ImageURL:     "http://example.com/test.qcow2",
			VolumeName:   "test-volume",
			VolumeSizeGB: 10,
		},
		Status: types.StatusPending,
	}

	// This should complete successfully in test environment
	manager.runJob(context.Background(), job)

	// In test environment, job should be marked as completed
	assert.Equal(t, types.StatusCompleted, job.Status)
}

// TestCleanupCompletedJobs_Enhanced tests the job cleanup functionality
func TestCleanupCompletedJobs_Enhanced(t *testing.T) {
	manager := &Manager{
		jobs: make(map[string]*Job),
	}

	// Create some completed jobs
	for i := 0; i < 105; i++ {
		jobID := fmt.Sprintf("completed-job-%d", i)
		job := &Job{
			ID:        jobID,
			Status:    types.StatusCompleted,
			CreatedAt: time.Now().Add(-time.Hour * time.Duration(i)),
			UpdatedAt: time.Now().Add(-time.Hour * time.Duration(i)),
		}
		manager.jobs[jobID] = job
	}

	// Create one running job
	runningJob := &Job{
		ID:        "running-job",
		Status:    types.StatusRunning,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	manager.jobs["running-job"] = runningJob

	initialCount := len(manager.jobs)
	assert.True(t, initialCount > 100) // Should have more than 100 jobs

	manager.CleanupCompletedJobs()

	// Should keep only the most recent 100 completed jobs + the running job
	assert.True(t, len(manager.jobs) <= 101) // 100 completed + 1 running
	assert.Contains(t, manager.jobs, "running-job")
}

// TestGetActiveJobs tests counting active jobs
func TestGetActiveJobs_Enhanced(t *testing.T) {
	manager := &Manager{
		jobs: make(map[string]*Job),
	}

	// Create jobs with different statuses
	statuses := []types.JobStatus{
		types.StatusPending,
		types.StatusRunning,
		types.StatusCompleted,
		types.StatusFailed,
		types.StatusCancelled,
	}

	for i, status := range statuses {
		jobID := fmt.Sprintf("job-%d", i)
		job := &Job{
			ID:     jobID,
			Status: status,
		}
		manager.jobs[jobID] = job
	}

	activeCount := manager.GetActiveJobs()

	// Only pending and running jobs should be considered active
	assert.Equal(t, 2, activeCount) // 1 pending + 1 running
}

// TestListCachedImages_NoLibvirt tests ListCachedImages with no libvirt pool
func TestListCachedImages_NoLibvirt(t *testing.T) {
	manager := &Manager{
		// No libvirtPool
	}

	images, err := manager.ListCachedImages()
	assert.Error(t, err)
	assert.Nil(t, images)
}

// TestParseDurationEnv tests the parseDurationEnv helper
func TestParseDurationEnv(t *testing.T) {
	t.Run("returns default when env var not set", func(t *testing.T) {
		t.Setenv("TEST_DURATION_UNSET", "")
		d := parseDurationEnv("TEST_DURATION_UNSET", 5*time.Hour)
		assert.Equal(t, 5*time.Hour, d)
	})

	t.Run("returns parsed value when env var is valid", func(t *testing.T) {
		t.Setenv("TEST_DURATION_VALID", "24h")
		d := parseDurationEnv("TEST_DURATION_VALID", 5*time.Hour)
		assert.Equal(t, 24*time.Hour, d)
	})

	t.Run("returns default when env var is invalid", func(t *testing.T) {
		t.Setenv("TEST_DURATION_INVALID", "notaduration")
		d := parseDurationEnv("TEST_DURATION_INVALID", 5*time.Hour)
		assert.Equal(t, 5*time.Hour, d)
	})
}

// TestStop tests that Stop does not panic and signals the eviction goroutine
func TestStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		jobs:        make(map[string]*Job),
		semaphore:   make(chan struct{}, 2),
		evictCancel: cancel,
	}

	// Should not panic
	m.Stop()

	// Context should be cancelled
	select {
	case <-ctx.Done():
		// expected
	default:
		t.Error("expected context to be cancelled after Stop()")
	}
}

// TestStop_NilCancel tests that Stop handles a nil evictCancel gracefully
func TestStop_NilCancel(t *testing.T) {
	m := &Manager{
		jobs:      make(map[string]*Job),
		semaphore: make(chan struct{}, 2),
	}
	// Should not panic
	m.Stop()
}

// TestRunEvictionLoop_ContextCancel tests that runEvictionLoop exits when context is cancelled.
func TestRunEvictionLoop_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	pool := &mockLibvirtPool{}
	m := &Manager{
		jobs:        make(map[string]*Job),
		semaphore:   make(chan struct{}, 2),
		libvirtPool: pool,
	}

	done := make(chan struct{})
	go func() {
		m.runEvictionLoop(ctx, time.Hour, time.Hour) // long interval — only ctx.Done() fires
		close(done)
	}()

	cancel()

	select {
	case <-done:
		// expected
	case <-time.After(2 * time.Second):
		t.Error("runEvictionLoop did not exit after context cancellation")
	}
}

// TestRunEvictionLoop_TickerFires tests that runEvictionLoop calls EvictExpiredImages on each tick.
func TestRunEvictionLoop_TickerFires(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	evictCalled := make(chan struct{}, 1)
	called := false
	triggerPool := &callbackLibvirtPool{
		onEvict: func() (int, error) {
			if !called {
				called = true
				evictCalled <- struct{}{}
			}
			return 0, nil
		},
	}

	m := &Manager{
		jobs:        make(map[string]*Job),
		semaphore:   make(chan struct{}, 2),
		libvirtPool: triggerPool,
	}

	go m.runEvictionLoop(ctx, time.Hour, 50*time.Millisecond)

	select {
	case <-evictCalled:
		// EvictExpiredImages was called on a tick
	case <-time.After(2 * time.Second):
		t.Error("runEvictionLoop did not call EvictExpiredImages on tick")
	}
}

// TestRunEvictionLoop_EvictError tests that eviction errors are logged but non-fatal.
func TestRunEvictionLoop_EvictError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	evictCalled := make(chan struct{}, 1)
	called := false
	triggerPool := &callbackLibvirtPool{
		onEvict: func() (int, error) {
			if !called {
				called = true
				evictCalled <- struct{}{}
			}
			return 0, fmt.Errorf("eviction failed")
		},
	}

	m := &Manager{
		jobs:        make(map[string]*Job),
		semaphore:   make(chan struct{}, 2),
		libvirtPool: triggerPool,
	}

	go m.runEvictionLoop(ctx, time.Hour, 50*time.Millisecond)

	select {
	case <-evictCalled:
		// error was returned but goroutine kept running (non-fatal)
	case <-time.After(2 * time.Second):
		t.Error("runEvictionLoop stopped after eviction error")
	}
}

// TestNewManager_StartsEvictionLoop tests that NewManager starts the eviction goroutine when pool is non-nil.
func TestNewManager_StartsEvictionLoop(t *testing.T) {
	evictCalled := make(chan struct{}, 1)
	called := false
	pool := &callbackLibvirtPool{
		onEvict: func() (int, error) {
			if !called {
				called = true
				evictCalled <- struct{}{}
			}
			return 0, nil
		},
	}

	t.Setenv("CACHE_EVICTION_INTERVAL", "50ms")
	m := NewManager(nil, nil, pool, nil)
	defer m.Stop()

	select {
	case <-evictCalled:
		// eviction goroutine started and fired
	case <-time.After(2 * time.Second):
		t.Error("NewManager did not start eviction loop")
	}
}

// TestNewManager_NilPool tests that NewManager does not start the eviction goroutine when pool is nil.
func TestNewManager_NilPool(t *testing.T) {
	m := NewManager(nil, nil, nil, nil)
	defer m.Stop()
	assert.NotNil(t, m)
	assert.NotNil(t, m.evictCancel)
}

// TestDeleteCachedImage_Manager tests DeleteCachedImage resolves the path via the pool and deletes it.
func TestDeleteCachedImage_Manager(t *testing.T) {
	pool := &mockLibvirtPool{}
	m := &Manager{
		jobs:        make(map[string]*Job),
		semaphore:   make(chan struct{}, 2),
		libvirtPool: pool,
	}

	key := "a3b4c5d6e7f8a3b4c5d6e7f8a3b4c5d6e7f8a3b4c5d6e7f8a3b4c5d6e7f8a3b4"
	err := m.DeleteCachedImage(key)
	assert.NoError(t, err)
	assert.Equal(t, []string{"/tmp/cache/" + key}, pool.deletedPaths)
}

// callbackLibvirtPool is a LibvirtPool whose EvictExpiredImages calls a callback.
type callbackLibvirtPool struct {
	onEvict func() (int, error)
}

//nolint:nilnil // matches real CheckCache "not found" semantic
func (c *callbackLibvirtPool) CheckCache(_ string) (*libvirt.ImageCache, error) { return nil, nil }
func (c *callbackLibvirtPool) AllocateImageFile(k string) (string, error)       { return "/tmp/" + k, nil }
func (c *callbackLibvirtPool) CreateCacheEntry(_, _ string) error               { return nil }
func (c *callbackLibvirtPool) DeleteImage(_ string) error                       { return nil }
func (c *callbackLibvirtPool) ListCachedImages() ([]*libvirt.ImageCache, error) {
	return nil, nil
}
func (c *callbackLibvirtPool) EvictExpiredImages(_ time.Duration) (int, error) {
	return c.onEvict()
}

// TestGetJobCacheInfo tests cache info retrieval
func TestGetJobCacheInfo_Enhanced(t *testing.T) {
	store, err := storage.NewStore(":memory:")
	assert.NoError(t, err)

	manager := &Manager{
		jobs:  make(map[string]*Job),
		store: store,
	}

	// Create a completed job with cache info
	job := &Job{
		ID:     "test-job",
		Status: types.StatusCompleted,
		Request: types.ProvisionRequest{
			ImageURL:   "http://example.com/image.qcow2",
			VolumeName: "test-volume",
		},
	}

	// Save job to database
	record := &storage.JobRecord{
		ID:          job.ID,
		Status:      string(job.Status),
		RequestJSON: `{"image_url":"http://example.com/image.qcow2","volume_name":"test-volume"}`,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	err = store.SaveJob(context.Background(), record)
	assert.NoError(t, err)

	manager.jobs[job.ID] = job

	// Test cache info retrieval
	cacheHit, imagePath, err := manager.GetJobCacheInfo(job.ID)
	assert.NoError(t, err)
	assert.False(t, cacheHit)
	assert.Empty(t, imagePath)
}
