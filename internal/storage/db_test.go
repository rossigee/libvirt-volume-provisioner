package storage

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/rossigee/libvirt-volume-provisioner/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewStore_InMemory(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer func() {
		_ = store.Close() // Ignore error in test
	}()

	assert.NotNil(t, store.db)
}

func TestNewStore_FilePath(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test-*.db")
	require.NoError(t, err)
	func() {
		_ = tmpFile.Close() // Ignore error in test
	}()
	defer func() {
		_ = os.Remove(tmpFile.Name()) // Ignore error in test
	}()

	store, err := NewStore(tmpFile.Name())
	require.NoError(t, err)
	defer func() {
		_ = store.Close() // Ignore error in test
	}()

	assert.NotNil(t, store.db)
}

func TestSaveJob_Insert(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer func() {
		_ = store.Close() // Ignore error in test
	}()

	job := &JobRecord{
		ID:          "test-job-1",
		Status:      string(types.StatusPending),
		RequestJSON: `{"image_url": "test"}`,
		RetryCount:  0,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	err = store.SaveJob(context.Background(), job)
	require.NoError(t, err)

	// Verify job was saved
	retrieved, err := store.GetJob("test-job-1")
	require.NoError(t, err)
	assert.Equal(t, job.ID, retrieved.ID)
	assert.Equal(t, job.Status, retrieved.Status)
	assert.Equal(t, job.RequestJSON, retrieved.RequestJSON)
}

func TestSaveJob_Update(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer func() {
		_ = store.Close() // Ignore error in test
	}()

	// Insert initial job
	job := &JobRecord{
		ID:          "test-job-2",
		Status:      string(types.StatusPending),
		RequestJSON: `{"image_url": "test"}`,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	err = store.SaveJob(context.Background(), job)
	require.NoError(t, err)

	// Update job status
	job.Status = string(types.StatusRunning)
	job.UpdatedAt = time.Now().Add(1 * time.Second)
	err = store.SaveJob(context.Background(), job)
	require.NoError(t, err)

	// Verify update
	retrieved, err := store.GetJob("test-job-2")
	require.NoError(t, err)
	assert.Equal(t, string(types.StatusRunning), retrieved.Status)
}

func TestGetJob_NotFound(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer func() {
		_ = store.Close() // Ignore error in test
	}()

	_, err = store.GetJob("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "job not found")
}

func TestListJobs(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer func() {
		_ = store.Close() // Ignore error in test
	}()

	// Insert multiple jobs
	for i := 0; i < 5; i++ {
		job := &JobRecord{
			ID:          "job-" + string(rune('0'+i)),
			Status:      string(types.StatusCompleted),
			RequestJSON: `{}`,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		err = store.SaveJob(context.Background(), job)
		require.NoError(t, err)
	}

	// List all jobs
	jobs, err := store.ListJobs(ListJobsFilter{})
	require.NoError(t, err)
	assert.Equal(t, 5, len(jobs))

	// List with limit
	jobs, err = store.ListJobs(ListJobsFilter{Limit: 2})
	require.NoError(t, err)
	assert.Equal(t, 2, len(jobs))

	// List with status filter
	jobs, err = store.ListJobs(ListJobsFilter{Status: string(types.StatusCompleted)})
	require.NoError(t, err)
	assert.Equal(t, 5, len(jobs))

	// List with non-existent status
	jobs, err = store.ListJobs(ListJobsFilter{Status: string(types.StatusPending)})
	require.NoError(t, err)
	assert.Equal(t, 0, len(jobs))
}

func TestMarkInProgressJobsFailed(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer func() {
		_ = store.Close() // Ignore error in test
	}()

	// Insert running and pending jobs
	runningJob := &JobRecord{
		ID:          "running-1",
		Status:      string(types.StatusRunning),
		RequestJSON: `{}`,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	err = store.SaveJob(context.Background(), runningJob)
	require.NoError(t, err)

	pendingJob := &JobRecord{
		ID:          "pending-1",
		Status:      string(types.StatusPending),
		RequestJSON: `{}`,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	err = store.SaveJob(context.Background(), pendingJob)
	require.NoError(t, err)

	// Insert completed job (should not be changed)
	completedJob := &JobRecord{
		ID:          "completed-1",
		Status:      string(types.StatusCompleted),
		RequestJSON: `{}`,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	err = store.SaveJob(context.Background(), completedJob)
	require.NoError(t, err)

	// Mark in-progress jobs as failed
	err = store.MarkInProgressJobsFailed()
	require.NoError(t, err)

	// Verify running job is now failed
	retrieved, err := store.GetJob("running-1")
	require.NoError(t, err)
	assert.Equal(t, string(types.StatusFailed), retrieved.Status)
	assert.Contains(t, retrieved.ErrorMessage, "daemon restarted")

	// Verify pending job is now failed
	retrieved, err = store.GetJob("pending-1")
	require.NoError(t, err)
	assert.Equal(t, string(types.StatusFailed), retrieved.Status)

	// Verify completed job is unchanged
	retrieved, err = store.GetJob("completed-1")
	require.NoError(t, err)
	assert.Equal(t, string(types.StatusCompleted), retrieved.Status)
}

func TestGetJobCount(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer func() {
		_ = store.Close() // Ignore error in test
	}()

	// Insert jobs with different statuses
	for i := 0; i < 3; i++ {
		job := &JobRecord{
			ID:          "running-" + string(rune('0'+i)),
			Status:      string(types.StatusRunning),
			RequestJSON: `{}`,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		err = store.SaveJob(context.Background(), job)
		require.NoError(t, err)
	}

	for i := 0; i < 2; i++ {
		job := &JobRecord{
			ID:          "completed-" + string(rune('0'+i)),
			Status:      string(types.StatusCompleted),
			RequestJSON: `{}`,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		err = store.SaveJob(context.Background(), job)
		require.NoError(t, err)
	}

	// Count running jobs
	count, err := store.GetJobCount(string(types.StatusRunning))
	require.NoError(t, err)
	assert.Equal(t, 3, count)

	// Count completed jobs
	count, err = store.GetJobCount(string(types.StatusCompleted))
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	// Count non-existent status
	count, err = store.GetJobCount(string(types.StatusPending))
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestDeleteOldJobs(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer func() {
		_ = store.Close() // Ignore error in test
	}()

	now := time.Now()

	// Insert old completed job
	oldJob := &JobRecord{
		ID:          "old-job",
		Status:      string(types.StatusCompleted),
		RequestJSON: `{}`,
		CreatedAt:   now.Add(-48 * time.Hour),
		UpdatedAt:   now.Add(-48 * time.Hour),
	}
	err = store.SaveJob(context.Background(), oldJob)
	require.NoError(t, err)

	// Insert recent completed job
	recentJob := &JobRecord{
		ID:          "recent-job",
		Status:      string(types.StatusCompleted),
		RequestJSON: `{}`,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	err = store.SaveJob(context.Background(), recentJob)
	require.NoError(t, err)

	// Insert old running job (should not be deleted)
	runningJob := &JobRecord{
		ID:          "running-job",
		Status:      string(types.StatusRunning),
		RequestJSON: `{}`,
		CreatedAt:   now.Add(-48 * time.Hour),
		UpdatedAt:   now.Add(-48 * time.Hour),
	}
	err = store.SaveJob(context.Background(), runningJob)
	require.NoError(t, err)

	// Delete jobs older than 24 hours
	err = store.DeleteOldJobs(24 * time.Hour)
	require.NoError(t, err)

	// Verify old completed job is deleted
	_, err = store.GetJob("old-job")
	assert.Error(t, err)

	// Verify recent completed job still exists
	job, err := store.GetJob("recent-job")
	require.NoError(t, err)
	assert.Equal(t, "recent-job", job.ID)

	// Verify running job still exists (not deleted even if old)
	job, err = store.GetJob("running-job")
	require.NoError(t, err)
	assert.Equal(t, "running-job", job.ID)
}

func TestGetStageWeights_NoHistory(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	// With no history the equal defaults (1,1) should yield 50/50.
	dl, cv := store.GetStageWeights(context.Background(), 1, 1)
	assert.InDelta(t, 0.5, dl, 0.001)
	assert.InDelta(t, 0.5, cv, 0.001)
	assert.InDelta(t, 1.0, dl+cv, 0.001)
}

func TestGetStageWeights_WithHistory(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	now := time.Now()

	// Simulate three past jobs: download took ~20 s, convert took ~40 s each.
	for i := 0; i < 3; i++ {
		jobID := fmt.Sprintf("job-%d", i)
		require.NoError(t, store.SaveStageRate(ctx, StageRate{
			Stage:          "download",
			RateBPS:        50 * 1024 * 1024,
			BytesProcessed: 1024 * 1024 * 1024,
			DurationMS:     20_000,
			JobID:          jobID,
			CreatedAt:      now,
		}))
		require.NoError(t, store.SaveStageRate(ctx, StageRate{
			Stage:          "convert",
			RateBPS:        200 * 1024 * 1024,
			BytesProcessed: 10 * 1024 * 1024 * 1024,
			DurationMS:     40_000,
			JobID:          jobID,
			CreatedAt:      now,
		}))
	}

	dl, cv := store.GetStageWeights(ctx, 1, 1)
	// avgDownload=20000, avgConvert=40000 → dl=1/3, cv=2/3
	assert.InDelta(t, 1.0/3.0, dl, 0.001)
	assert.InDelta(t, 2.0/3.0, cv, 0.001)
	assert.InDelta(t, 1.0, dl+cv, 0.001)
}

func TestGetStageWeights_OnlyDownloadHistory(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	require.NoError(t, store.SaveStageRate(ctx, StageRate{
		Stage:     "download",
		DurationMS: 30_000,
		JobID:     "job-1",
		CreatedAt: time.Now(),
	}))

	// No convert history; default for convert is 1 (same unit as the 1,1 defaults).
	// With actual download avg=30000 and default convert=1: dl ≈ 1.0, cv ≈ 0.
	// Using equal defaults (1,1): dl=30000/(30000+1) ≈ 1, cv≈0.
	// More practically: caller passes meaningful defaults, e.g. 10000,10000.
	dl, cv := store.GetStageWeights(ctx, 10_000, 10_000)
	// avgDownload=30000 beats default 10000; convert falls back to 10000.
	assert.InDelta(t, 30_000.0/40_000.0, dl, 0.001)
	assert.InDelta(t, 10_000.0/40_000.0, cv, 0.001)
	assert.InDelta(t, 1.0, dl+cv, 0.001)
}

func TestGetStageWeights_AlwaysSumToOne(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	now := time.Now()

	// Varied durations to stress the averaging.
	durations := []int64{5_000, 15_000, 25_000, 10_000, 20_000}
	for i, d := range durations {
		jobID := fmt.Sprintf("job-%d", i)
		require.NoError(t, store.SaveStageRate(ctx, StageRate{
			Stage: "download", DurationMS: d, JobID: jobID, CreatedAt: now,
		}))
		require.NoError(t, store.SaveStageRate(ctx, StageRate{
			Stage: "convert", DurationMS: d * 3, JobID: jobID, CreatedAt: now,
		}))
	}

	dl, cv := store.GetStageWeights(ctx, 1, 1)
	assert.InDelta(t, 1.0, dl+cv, 0.001, "weights must sum to 1")
	assert.True(t, dl > 0 && dl < 1, "download weight must be in (0,1)")
	assert.True(t, cv > 0 && cv < 1, "convert weight must be in (0,1)")
}

func TestSaveJob_WithCompletedAt(t *testing.T) {
	store, err := NewStore(":memory:")
	require.NoError(t, err)
	defer func() {
		_ = store.Close() // Ignore error in test
	}()

	completedTime := time.Now()
	job := &JobRecord{
		ID:          "completed-job",
		Status:      string(types.StatusCompleted),
		RequestJSON: `{}`,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		CompletedAt: &completedTime,
	}

	err = store.SaveJob(context.Background(), job)
	require.NoError(t, err)

	retrieved, err := store.GetJob("completed-job")
	require.NoError(t, err)
	assert.NotNil(t, retrieved.CompletedAt)
	assert.Equal(t, completedTime.Unix(), retrieved.CompletedAt.Unix())
}
