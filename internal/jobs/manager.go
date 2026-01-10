// Package jobs manages the lifecycle of volume provisioning jobs
// including creation, execution tracking, and status reporting.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rossigee/libvirt-volume-provisioner/internal/lvm"
	"github.com/rossigee/libvirt-volume-provisioner/internal/minio"
	"github.com/rossigee/libvirt-volume-provisioner/internal/storage"
	"github.com/rossigee/libvirt-volume-provisioner/pkg/types"
	"github.com/sirupsen/logrus"
)

// Job represents a volume provisioning job.
type Job struct {
	ID         string
	Status     types.JobStatus
	Request    types.ProvisionRequest
	Progress   *types.ProgressInfo
	Error      error
	CreatedAt  time.Time
	UpdatedAt  time.Time
	cancelFunc context.CancelFunc
}

// UpdateProgress implements the ProgressUpdater interface.
func (j *Job) UpdateProgress(stage string, percent float64, bytesProcessed, bytesTotal int64) {
	j.Progress = &types.ProgressInfo{
		Stage:          stage,
		Percent:        percent,
		BytesProcessed: bytesProcessed,
		BytesTotal:     bytesTotal,
	}
	j.UpdatedAt = time.Now()
}

// Manager manages volume provisioning jobs.
type Manager struct {
	minioClient *minio.Client
	lvmManager  *lvm.Manager
	store       *storage.Store
	jobs        map[string]*Job
	semaphore   chan struct{} // Limits concurrent operations
	mu          sync.RWMutex
}

// NewManager creates a new job manager.
func NewManager(minioClient *minio.Client, lvmManager *lvm.Manager, store *storage.Store) *Manager {
	return &Manager{
		minioClient: minioClient,
		lvmManager:  lvmManager,
		store:       store,
		jobs:        make(map[string]*Job),
		semaphore:   make(chan struct{}, 2), // Max 2 concurrent operations
	}
}

// syncToDatabase persists job state to the database
func (m *Manager) syncToDatabase(ctx context.Context, job *Job) {
	if m.store == nil {
		return // Database not available
	}

	requestJSON, err := json.Marshal(job.Request)
	if err != nil {
		logrus.WithError(err).Error("Failed to marshal job request for database sync")
		return
	}
	progressJSON := ""
	if job.Progress != nil {
		if data, err := json.Marshal(job.Progress); err == nil {
			progressJSON = string(data)
		}
	}

	errorMessage := ""
	if job.Error != nil {
		errorMessage = job.Error.Error()
	}

	completedAt := (*time.Time)(nil)
	if job.Status == types.StatusCompleted || job.Status == types.StatusFailed {
		completedAt = &job.UpdatedAt
	}

	record := &storage.JobRecord{
		ID:           job.ID,
		Status:       string(job.Status),
		RequestJSON:  string(requestJSON),
		ProgressJSON: progressJSON,
		ErrorMessage: errorMessage,
		RetryCount:   0, // TODO: Integrate retry count once retry logic is implemented
		CreatedAt:    job.CreatedAt,
		UpdatedAt:    job.UpdatedAt,
		CompletedAt:  completedAt,
	}

	if err := m.store.SaveJob(ctx, record); err != nil {
		logrus.WithError(err).WithField("job_id", job.ID).Error("Failed to sync job to database")
	}
}

// RecoverJobs marks any in-progress jobs from previous runs as failed
// This should be called during startup to clean up jobs interrupted by daemon restart
func (m *Manager) RecoverJobs() error {
	if m.store == nil {
		return nil // Database not available
	}

	logrus.Info("Recovering jobs from previous run...")
	if err := m.store.MarkInProgressJobsFailed(); err != nil {
		return fmt.Errorf("failed to mark in-progress jobs as failed: %w", err)
	}
	logrus.Info("Job recovery completed")
	return nil
}

// StartJob starts a new volume provisioning job.
func (m *Manager) StartJob(req types.ProvisionRequest) (string, error) {
	jobID := uuid.New().String()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute) // 30 minute timeout

	job := &Job{
		ID:         jobID,
		Status:     types.StatusPending,
		Request:    req,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		cancelFunc: cancel,
	}

	m.mu.Lock()
	m.jobs[jobID] = job
	m.mu.Unlock()

	// Persist to database
	m.syncToDatabase(ctx, job)

	// Start job in background
	go m.runJob(ctx, job)

	return jobID, nil
}

// GetJobStatus returns the status of a job
func (m *Manager) GetJobStatus(jobID string) (*types.StatusResponse, error) {
	m.mu.RLock()
	job, exists := m.jobs[jobID]
	m.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("job not found: %s", jobID)
	}

	response := &types.StatusResponse{
		JobID:         job.ID,
		Status:        job.Status,
		Progress:      job.Progress,
		CorrelationID: job.ID, // Use job ID as correlation ID
		CreatedAt:     job.CreatedAt,
		UpdatedAt:     job.UpdatedAt,
	}

	if job.Error != nil {
		response.Error = job.Error.Error()
	}

	return response, nil
}

// CancelJob cancels a running job
func (m *Manager) CancelJob(jobID string) error {
	m.mu.Lock()
	job, exists := m.jobs[jobID]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("job not found: %s", jobID)
	}

	if job.Status != types.StatusRunning && job.Status != types.StatusPending {
		m.mu.Unlock()
		return fmt.Errorf("job cannot be cancelled: %s", job.Status)
	}

	job.cancelFunc()
	job.Status = types.StatusFailed
	job.UpdatedAt = time.Now()
	job.Error = fmt.Errorf("job cancelled by user")
	m.mu.Unlock()

	// Persist cancellation to database
	m.syncToDatabase(context.Background(), job)

	return nil
}

// runJob executes a provisioning job
func (m *Manager) runJob(ctx context.Context, job *Job) {
	// Acquire semaphore (limit concurrent operations)
	select {
	case m.semaphore <- struct{}{}:
		defer func() { <-m.semaphore }()
	case <-ctx.Done():
		job.Status = types.StatusFailed
		job.UpdatedAt = time.Now()
		m.syncToDatabase(ctx, job)
		return
	}

	job.Status = types.StatusRunning
	job.UpdatedAt = time.Now()
	m.syncToDatabase(ctx, job)

	defer func() {
		job.UpdatedAt = time.Now()
		m.syncToDatabase(ctx, job)
	}()

	// Execute provisioning steps
	err := m.provisionVolume(ctx, job)
	if err != nil {
		job.Status = types.StatusFailed
		job.Error = err
		return
	}

	job.Status = types.StatusCompleted
}

// provisionVolume performs the actual volume provisioning
func (m *Manager) provisionVolume(ctx context.Context, job *Job) error {
	req := job.Request

	// Update progress
	job.Progress = &types.ProgressInfo{
		Stage:   "initializing",
		Percent: 0,
	}

	// Step 1: Download image from MinIO
	job.Progress.Stage = "downloading"
	job.Progress.Percent = 10

	tempPath, err := m.minioClient.DownloadImage(ctx, req.ImageURL, job)
	if err != nil {
		return fmt.Errorf("failed to download image: %w", err)
	}
	defer func() {
		_ = m.minioClient.Cleanup(tempPath) // Cleanup errors are not critical
	}()

	// Step 2: Create LVM volume
	job.Progress.Stage = "creating_volume"
	job.Progress.Percent = 50

	if err := m.lvmManager.CreateVolume(ctx, req.VolumeName, req.VolumeSizeGB); err != nil {
		return fmt.Errorf("failed to create volume: %w", err)
	}
	volumeCreated := true

	// Rollback defer: Delete volume if provisioning fails after creation
	defer func() {
		if volumeCreated && job.Status == types.StatusFailed {
			logrus.WithFields(logrus.Fields{
				"job_id":      job.ID,
				"volume_name": req.VolumeName,
			}).Warn("Rolling back: deleting failed volume")

			if deleteErr := m.lvmManager.DeleteVolume(req.VolumeName); deleteErr != nil {
				logrus.WithError(deleteErr).WithFields(logrus.Fields{
					"job_id":      job.ID,
					"volume_name": req.VolumeName,
				}).Error("Rollback failed: could not delete volume")

				// Combine errors: original error + rollback failure
				job.Error = fmt.Errorf("provision failed + rollback failed: %w", deleteErr)
			}
		}
	}()

	// Step 3: Convert and populate volume
	job.Progress.Stage = "converting"
	job.Progress.Percent = 75

	if err := m.lvmManager.PopulateVolume(ctx, tempPath, req.VolumeName, req.ImageType, job); err != nil {
		return fmt.Errorf("failed to populate volume: %w", err)
	}

	// Step 4: Finalize
	job.Progress.Stage = "finalizing"
	job.Progress.Percent = 100

	return nil
}

// GetActiveJobs returns the count of active jobs
func (m *Manager) GetActiveJobs() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, job := range m.jobs {
		if job.Status == types.StatusRunning || job.Status == types.StatusPending {
			count++
		}
	}
	return count
}

// CleanupCompletedJobs removes old completed jobs (keep last 100)
func (m *Manager) CleanupCompletedJobs() {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Keep only recent jobs
	completed := make([]string, 0)
	for id, job := range m.jobs {
		if job.Status == types.StatusCompleted || job.Status == types.StatusFailed {
			completed = append(completed, id)
		}
	}

	// Keep only the most recent 100 completed jobs
	if len(completed) > 100 {
		for i := 0; i < len(completed)-100; i++ {
			delete(m.jobs, completed[i])
		}
	}
}
