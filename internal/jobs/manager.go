package jobs

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rossigee/libvirt-volume-provisioner/internal/lvm"
	"github.com/rossigee/libvirt-volume-provisioner/internal/minio"
	"github.com/rossigee/libvirt-volume-provisioner/pkg/types"
)

// Job represents a volume provisioning job
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

// UpdateProgress implements the ProgressUpdater interface
func (j *Job) UpdateProgress(stage string, percent float64, bytesProcessed, bytesTotal int64) {
	j.Progress = &types.ProgressInfo{
		Stage:          stage,
		Percent:        percent,
		BytesProcessed: bytesProcessed,
		BytesTotal:     bytesTotal,
	}
	j.UpdatedAt = time.Now()
}

// Manager manages volume provisioning jobs
type Manager struct {
	minioClient *minio.Client
	lvmManager  *lvm.Manager
	jobs        map[string]*Job
	semaphore   chan struct{} // Limits concurrent operations
	mu          sync.RWMutex
}

// NewManager creates a new job manager
func NewManager(minioClient *minio.Client, lvmManager *lvm.Manager) *Manager {
	return &Manager{
		minioClient: minioClient,
		lvmManager:  lvmManager,
		jobs:        make(map[string]*Job),
		semaphore:   make(chan struct{}, 2), // Max 2 concurrent operations
	}
}

// StartJob starts a new volume provisioning job
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
		CorrelationID: job.Request.CorrelationID,
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
	defer m.mu.Unlock()

	job, exists := m.jobs[jobID]
	if !exists {
		return fmt.Errorf("job not found: %s", jobID)
	}

	if job.Status != types.StatusRunning && job.Status != types.StatusPending {
		return fmt.Errorf("job cannot be cancelled: %s", job.Status)
	}

	job.cancelFunc()
	job.Status = types.StatusCancelled
	job.UpdatedAt = time.Now()

	return nil
}

// runJob executes a provisioning job
func (m *Manager) runJob(ctx context.Context, job *Job) {
	// Acquire semaphore (limit concurrent operations)
	select {
	case m.semaphore <- struct{}{}:
		defer func() { <-m.semaphore }()
	case <-ctx.Done():
		job.Status = types.StatusCancelled
		job.UpdatedAt = time.Now()
		return
	}

	job.Status = types.StatusRunning
	job.UpdatedAt = time.Now()

	defer func() {
		job.UpdatedAt = time.Now()
	}()

	// Execute provisioning steps
	if err := m.provisionVolume(ctx, job); err != nil {
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

	if err := m.lvmManager.CreateVolume(req.VolumeName, req.VolumeSizeGB); err != nil {
		return fmt.Errorf("failed to create volume: %w", err)
	}

	// Step 3: Convert and populate volume
	job.Progress.Stage = "converting"
	job.Progress.Percent = 75

	if err := m.lvmManager.PopulateVolume(tempPath, req.VolumeName, req.ImageType, job); err != nil {
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
		if job.Status == types.StatusCompleted || job.Status == types.StatusFailed || job.Status == types.StatusCancelled {
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
