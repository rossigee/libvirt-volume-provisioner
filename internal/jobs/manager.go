// Package jobs manages the lifecycle of volume provisioning jobs
// including creation, execution tracking, and status reporting.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rossigee/libvirt-volume-provisioner/internal/libvirt"
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
	CacheHit   bool
	ImagePath  string
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
	jobs        map[string]*Job
	lvmManager  *lvm.Manager
	libvirtPool *libvirt.PoolManager
	store       *storage.Store
	semaphore   chan struct{}
	mu          sync.RWMutex
}

// NewManager creates a new job manager.
func NewManager(minioClient *minio.Client, lvmManager *lvm.Manager,
	libvirtPool *libvirt.PoolManager, store *storage.Store) *Manager {
	return &Manager{
		minioClient: minioClient,
		lvmManager:  lvmManager,
		libvirtPool: libvirtPool,
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

	// Include cache information for completed jobs
	if job.Status == types.StatusCompleted {
		response.CacheHit = &job.CacheHit
		response.ImagePath = job.ImagePath
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
	err := m.ProvisionVolume(ctx, job)
	if err != nil {
		job.Status = types.StatusFailed
		job.Error = err
		return
	}

	job.Status = types.StatusCompleted
}

// ProvisionVolume performs the actual volume provisioning
func (m *Manager) ProvisionVolume(ctx context.Context, job *Job) error {
	req := job.Request

	// Track provisioning state for rollback
	volumeCreated := false
	provisionFailed := false

	// Update progress
	job.Progress = &types.ProgressInfo{
		Stage:   "initializing",
		Percent: 0,
	}

	// Step 1: Check image cache or download
	job.Progress.Stage = "checking_cache"
	job.Progress.Percent = 5

	imagePath, err := m.getOrDownloadImage(ctx, req, job)
	if err != nil {
		return fmt.Errorf("failed to get image: %w", err)
	}

	// Step 2: Create LVM volume
	job.Progress.Stage = "creating_volume"
	job.Progress.Percent = 50

	if err := m.lvmManager.CreateVolume(ctx, req.VolumeName, req.VolumeSizeGB); err != nil {
		provisionFailed = true
		return fmt.Errorf("failed to create volume: %w", err)
	}
	volumeCreated = true

	// Rollback defer: Delete volume if provisioning fails after creation
	defer func() {
		if volumeCreated && provisionFailed {
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

	if err := m.lvmManager.PopulateVolume(ctx, imagePath, req.VolumeName, req.ImageType, job); err != nil {
		provisionFailed = true
		return fmt.Errorf("failed to populate volume: %w", err)
	}

	// Step 4: Finalize
	job.Progress.Stage = "finalizing"
	job.Progress.Percent = 100

	return nil
}

// getOrDownloadImage checks cache or downloads image and returns the path
func (m *Manager) getOrDownloadImage(ctx context.Context, req types.ProvisionRequest, job *Job) (string, error) {
	// Get checksum from MinIO .sha256 file
	checksum, err := m.getImageChecksum(ctx, req.ImageURL)
	if err != nil {
		logrus.WithError(err).Warn("Failed to get image checksum from MinIO, using URL as cache key")
		checksum = req.ImageURL // Fallback to URL
	}

	// Check if image is cached using checksum as key
	cachedImage, err := m.libvirtPool.CheckCache(checksum)
	if err != nil {
		logrus.WithError(err).Warn("Failed to check image cache, proceeding with download")
	}

	if cachedImage != nil {
		logrus.WithFields(logrus.Fields{
			"job_id":      job.ID,
			"image_url":   req.ImageURL,
			"checksum":    checksum,
			"cached_path": cachedImage.Path,
			"cache_hit":   true,
		}).Info("Using cached image")
		job.CacheHit = true
		job.ImagePath = cachedImage.Path
		return cachedImage.Path, nil
	}

	// Image not cached, need to download
	logrus.WithFields(logrus.Fields{
		"job_id":    job.ID,
		"image_url": req.ImageURL,
		"cache_hit": false,
	}).Info("Image not cached, downloading")

	// Generate image name from URL
	imageName := libvirt.GetImageNameFromURL(req.ImageURL)

	// Allocate file path in cache directory (no libvirt volume allocation).
	// This preserves compression for QCOW2 images by storing them as plain files.
	imagePath, err := m.libvirtPool.AllocateImageFile(imageName)
	if err != nil {
		return "", fmt.Errorf("failed to allocate cache file: %w", err)
	}

	// Download image to cache path
	job.Progress.Stage = "downloading"
	job.Progress.Percent = 10

	if err := m.minioClient.DownloadImageToPath(ctx, req.ImageURL, imagePath, job); err != nil {
		// Cleanup failed download
		_ = m.libvirtPool.DeleteImage(imagePath)
		return "", fmt.Errorf("failed to download image: %w", err)
	}

	// If we don't have a checksum from MinIO, calculate it locally
	if checksum == "" {
		var err error
		checksum, err = libvirt.CalculateChecksum(imagePath)
		if err != nil {
			logrus.WithError(err).Warn("Failed to calculate checksum, cache may not work properly")
			checksum = req.ImageURL // Fallback to URL as cache key
		}
	}

	if err := m.libvirtPool.CreateCacheEntry(imagePath, checksum); err != nil {
		logrus.WithError(err).Warn("Failed to create cache entry")
	}

	logrus.WithFields(logrus.Fields{
		"job_id":     job.ID,
		"image_path": imagePath,
		"checksum":   checksum,
	}).Info("Image downloaded and cached")

	job.CacheHit = false
	job.ImagePath = imagePath
	return imagePath, nil
}

// getImageChecksum retrieves the SHA256 checksum from MinIO .sha256 file
func (m *Manager) getImageChecksum(ctx context.Context, imageURL string) (string, error) {
	// Parse the image URL to extract bucket and object
	u, err := url.Parse(imageURL)
	if err != nil {
		return "", fmt.Errorf("invalid image URL: %w", err)
	}

	pathParts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(pathParts) < 2 {
		return "", fmt.Errorf("invalid image URL path: %s", u.Path)
	}

	bucketName := pathParts[0]
	imageObjectName := strings.Join(pathParts[1:], "/")
	checksumObjectName := imageObjectName + ".sha256"

	// Try to get the checksum file content
	checksumData, err := m.minioClient.GetObjectContent(ctx, bucketName, checksumObjectName)
	if err != nil {
		return "", fmt.Errorf("checksum file not found or unreadable: %w", err)
	}

	checksum := strings.TrimSpace(string(checksumData))
	if len(checksum) != 64 {
		return "", fmt.Errorf("invalid checksum format: expected 64 characters, got %d", len(checksum))
	}

	return checksum, nil
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

// GetJobCacheInfo returns cache information for a completed job
func (m *Manager) GetJobCacheInfo(jobID string) (bool, string, error) {
	m.mu.RLock()
	job, exists := m.jobs[jobID]
	m.mu.RUnlock()

	if !exists {
		return false, "", fmt.Errorf("job not found: %s", jobID)
	}

	if job.Status != types.StatusCompleted {
		return false, "", fmt.Errorf("job not completed: %s", job.Status)
	}

	return job.CacheHit, job.ImagePath, nil
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
