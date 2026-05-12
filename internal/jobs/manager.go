// Package jobs manages the lifecycle of volume provisioning jobs
// including creation, execution tracking, and status reporting.
package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rossigee/libvirt-volume-provisioner/internal/libvirt"
	"github.com/rossigee/libvirt-volume-provisioner/internal/lvm"
	appmetrics "github.com/rossigee/libvirt-volume-provisioner/internal/metrics"
	"github.com/rossigee/libvirt-volume-provisioner/internal/minio"
	"github.com/rossigee/libvirt-volume-provisioner/internal/storage"
	"github.com/rossigee/libvirt-volume-provisioner/internal/timing"
	"github.com/rossigee/libvirt-volume-provisioner/pkg/types"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Job represents a volume provisioning job.
type Job struct {
	mu             sync.RWMutex // protects all fields below
	ID             string
	CorrelationID  string
	Status         types.JobStatus
	Request        types.ProvisionRequest
	Progress       *types.ProgressInfo
	Error          error
	CacheHit       bool
	ImagePath      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	cancelFunc     context.CancelFunc
	downloadWeight float64
	convertWeight  float64
	currentStage   string
	stageStartTime time.Time
}

// UpdateProgress implements the ProgressUpdater interface.
func (j *Job) UpdateProgress(stage string, stagePercent float64, bytesProcessed, bytesTotal int64) {
	now := time.Now()

	j.mu.Lock()
	defer j.mu.Unlock()

	if j.currentStage != stage {
		j.currentStage = stage
		j.stageStartTime = now
	}

	var stageETA *int64
	if stagePercent > 0 && stagePercent < 100 {
		elapsedSec := now.Sub(j.stageStartTime).Seconds()
		if elapsedSec > 0 {
			remainingPercent := 100 - stagePercent
			etaSec := int64((elapsedSec / stagePercent) * remainingPercent)
			stageETA = &etaSec
		}
	}

	var overallPercent float64
	var overallETA *int64
	switch stage {
	case "downloading":
		overallPercent = stagePercent * j.downloadWeight
		if stageETA != nil {
			convertSec := int64(float64(*stageETA) * (j.convertWeight / j.downloadWeight))
			totalETA := *stageETA + convertSec
			overallETA = &totalETA
		}
	case "converting":
		overallPercent = j.downloadWeight*100 + stagePercent*j.convertWeight
		if stageETA != nil {
			overallETA = stageETA
		}
	default:
		overallPercent = stagePercent
	}

	j.Progress = &types.ProgressInfo{
		Stage:          stage,
		StagePercent:   stagePercent,
		OverallPercent: overallPercent,
		BytesProcessed: bytesProcessed,
		BytesTotal:     bytesTotal,
		ETASec:         stageETA,
		OverallETASec:  overallETA,
		StageStartTime: j.stageStartTime,
		JobStartTime:   j.CreatedAt,
	}
	j.UpdatedAt = now
}

// LibvirtPool is the interface Manager uses to interact with the image cache.
type LibvirtPool interface {
	CheckCache(cacheKey string) (*libvirt.ImageCache, error)
	AllocateImageFile(cacheKey string) (string, error)
	CreateCacheEntry(imagePath, checksum string) error
	DeleteImage(imagePath string) error
	ListCachedImages() ([]*libvirt.ImageCache, error)
	EvictExpiredImages(maxAge time.Duration) (int, error)
}

// Manager manages volume provisioning jobs.
type Manager struct {
	minioClient *minio.Client
	jobs        map[string]*Job
	lvmManager  *lvm.Manager
	libvirtPool LibvirtPool
	store       *storage.Store
	metrics     *appmetrics.Metrics
	semaphore   chan struct{}
	mu          sync.RWMutex
	bgCancel    context.CancelFunc
	jobTimeout  time.Duration
}

// NewManager creates a new job manager.
func NewManager(minioClient *minio.Client, lvmManager *lvm.Manager,
	libvirtPool LibvirtPool, store *storage.Store, met *appmetrics.Metrics,
	maxConcurrent int, jobTimeout, cacheMaxAge, cacheEvictionInterval time.Duration) *Manager {
	bgCtx, bgCancel := context.WithCancel(context.Background())
	mgr := &Manager{
		minioClient: minioClient,
		lvmManager:  lvmManager,
		libvirtPool: libvirtPool,
		store:       store,
		metrics:     met,
		jobs:        make(map[string]*Job),
		semaphore:   make(chan struct{}, maxConcurrent),
		bgCancel:    bgCancel,
		jobTimeout:  jobTimeout,
	}
	if met != nil {
		met.UpdateDependencyStatus("minio", minioClient != nil)
		met.UpdateDependencyStatus("lvm", lvmManager != nil)
		met.UpdateDependencyStatus("libvirt", libvirtPool != nil)
		met.UpdateDependencyStatus("storage", store != nil)
	}
	if libvirtPool != nil {
		go mgr.runEvictionLoop(bgCtx, cacheMaxAge, cacheEvictionInterval)
	}
	go mgr.runCleanupLoop(bgCtx)
	return mgr
}

func (m *Manager) runEvictionLoop(ctx context.Context, maxAge, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := m.libvirtPool.EvictExpiredImages(maxAge); err != nil {
				logrus.WithError(err).Error("Cache eviction sweep failed")
			}
		}
	}
}

func (m *Manager) runCleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.CleanupCompletedJobs()
		}
	}
}

// Stop signals all background goroutines to exit.
func (m *Manager) Stop() {
	if m.bgCancel != nil {
		m.bgCancel()
	}
}

// DeleteCachedImage removes a cached image by its cache key.
func (m *Manager) DeleteCachedImage(cacheKey string) error {
	imagePath, err := m.libvirtPool.AllocateImageFile(cacheKey)
	if err != nil {
		return fmt.Errorf("failed to resolve cache path: %w", err)
	}
	if err := m.libvirtPool.DeleteImage(imagePath); err != nil {
		return fmt.Errorf("failed to delete cached image: %w", err)
	}
	return nil
}

// syncToDatabase persists a snapshot of job state to the database.
func (m *Manager) syncToDatabase(ctx context.Context, job *Job) {
	if m.store == nil {
		return
	}

	// Snapshot fields under the job lock
	job.mu.RLock()
	status := job.Status
	request := job.Request
	progress := job.Progress
	jobError := job.Error
	updatedAt := job.UpdatedAt
	createdAt := job.CreatedAt
	jobID := job.ID
	job.mu.RUnlock()

	requestJSON, err := json.Marshal(request)
	if err != nil {
		logrus.WithError(err).Error("Failed to marshal job request for database sync")
		return
	}

	progressJSON := ""
	if progress != nil {
		if data, err := json.Marshal(progress); err == nil {
			progressJSON = string(data)
		}
	}

	errorMessage := ""
	if jobError != nil {
		errorMessage = jobError.Error()
	}

	var completedAt *time.Time
	if status == types.StatusCompleted || status == types.StatusFailed || status == types.StatusCancelled {
		completedAt = &updatedAt
	}

	record := &storage.JobRecord{
		ID:           jobID,
		Status:       string(status),
		RequestJSON:  string(requestJSON),
		ProgressJSON: progressJSON,
		ErrorMessage: errorMessage,
		RetryCount:   0,
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
		CompletedAt:  completedAt,
	}

	if err := m.store.SaveJob(ctx, record); err != nil {
		logrus.WithError(err).WithField("job_id", jobID).Error("Failed to sync job to database")
	}
}

// RecoverJobs marks any in-progress jobs from previous runs as failed.
func (m *Manager) RecoverJobs() error {
	if m.store == nil {
		return nil
	}

	logrus.Info("Recovering jobs from previous run...")
	if err := m.store.MarkInProgressJobsFailed(); err != nil {
		return fmt.Errorf("failed to mark in-progress jobs as failed: %w", err)
	}
	logrus.Info("Job recovery completed")
	return nil
}

// StartJob starts a new volume provisioning job.
func (m *Manager) StartJob(ctx context.Context, req types.ProvisionRequest) (string, error) {
	tracer := otel.Tracer("job-manager")
	_, span := tracer.Start(ctx, "StartJob",
		trace.WithAttributes(
			attribute.String("job.correlation_id", req.CorrelationID),
			attribute.String("job.image_url", req.ImageURL),
			attribute.Int("job.volume_size_gb", req.VolumeSizeGB),
			attribute.String("job.volume_name", req.VolumeName),
		))
	defer span.End()

	jobID := uuid.New().String()
	span.SetAttributes(attribute.String("job.id", jobID))

	// Detached context — the HTTP request context is cancelled when the response is sent.
	jobCtx, cancel := context.WithTimeout(context.Background(), m.jobTimeout)

	job := &Job{
		ID:            jobID,
		CorrelationID: req.CorrelationID,
		Status:        types.StatusPending,
		Request:       req,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		cancelFunc:    cancel,
	}

	m.mu.Lock()
	m.jobs[jobID] = job
	m.mu.Unlock()

	m.syncToDatabase(context.Background(), job) //nolint:contextcheck

	go m.runJob(jobCtx, job) //nolint:contextcheck // intentional: job outlives the HTTP request

	span.SetStatus(codes.Ok, "job started successfully")
	return jobID, nil
}

// GetJobStatus returns the status of a job.
func (m *Manager) GetJobStatus(jobID string) (*types.StatusResponse, error) {
	m.mu.RLock()
	job, exists := m.jobs[jobID]
	m.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("job not found: %s", jobID)
	}

	job.mu.RLock()
	defer job.mu.RUnlock()

	response := &types.StatusResponse{
		JobID:         job.ID,
		Status:        job.Status,
		Progress:      job.Progress,
		CorrelationID: job.CorrelationID,
		CreatedAt:     job.CreatedAt,
		UpdatedAt:     job.UpdatedAt,
	}
	if job.Error != nil {
		response.Error = job.Error.Error()
	}
	if job.Status == types.StatusCompleted {
		cacheHit := job.CacheHit
		response.CacheHit = &cacheHit
		response.ImagePath = job.ImagePath
	}

	return response, nil
}

// CancelJob cancels a running job.
func (m *Manager) CancelJob(ctx context.Context, jobID string) error {
	m.mu.RLock()
	job, exists := m.jobs[jobID]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("job not found: %s", jobID)
	}

	job.mu.Lock()
	if job.Status != types.StatusRunning && job.Status != types.StatusPending {
		status := job.Status
		job.mu.Unlock()
		return fmt.Errorf("job cannot be cancelled: %s", status)
	}
	job.cancelFunc()
	job.Status = types.StatusCancelled
	job.UpdatedAt = time.Now()
	job.mu.Unlock()

	m.syncToDatabase(ctx, job)
	return nil
}

// runJob executes a provisioning job.
func (m *Manager) runJob(ctx context.Context, job *Job) {
	if m.minioClient == nil || m.lvmManager == nil || m.libvirtPool == nil || m.store == nil {
		job.mu.Lock()
		job.Status = types.StatusFailed
		job.Error = fmt.Errorf("job manager dependencies not initialized")
		job.UpdatedAt = time.Now()
		job.mu.Unlock()
		return
	}

	tracer := otel.Tracer("libvirt-volume-provisioner")
	ctx, span := tracer.Start(ctx, "runJob",
		trace.WithAttributes(
			attribute.String("job.id", job.ID),
			attribute.String("job.image_url", job.Request.ImageURL),
			attribute.String("job.volume_name", job.Request.VolumeName),
		))
	defer span.End()

	select {
	case m.semaphore <- struct{}{}:
		defer func() { <-m.semaphore }()
	case <-ctx.Done():
		job.mu.Lock()
		job.Status = types.StatusFailed
		job.UpdatedAt = time.Now()
		job.mu.Unlock()
		m.syncToDatabase(ctx, job)
		span.SetStatus(codes.Error, "job cancelled during semaphore acquisition")
		return
	}

	// Fail fast if context was already cancelled before we acquired the semaphore.
	if err := ctx.Err(); err != nil {
		job.mu.Lock()
		job.Status = types.StatusFailed
		job.Error = err
		job.UpdatedAt = time.Now()
		job.mu.Unlock()
		m.syncToDatabase(ctx, job)
		return
	}

	job.mu.Lock()
	job.Status = types.StatusRunning
	jobStart := job.CreatedAt
	job.UpdatedAt = time.Now()
	job.mu.Unlock()
	m.syncToDatabase(ctx, job)
	if m.metrics != nil {
		m.metrics.RecordJobStart()
	}

	defer func() {
		job.mu.Lock()
		job.UpdatedAt = time.Now()
		status := job.Status
		jobErr := job.Error
		job.mu.Unlock()

		m.syncToDatabase(ctx, job)

		if m.metrics != nil {
			m.metrics.RecordJobEnd(string(status), time.Since(jobStart).Seconds())
		}

		switch status {
		case types.StatusCompleted:
			span.SetStatus(codes.Ok, "job completed successfully")
		case types.StatusFailed:
			if jobErr != nil {
				span.SetStatus(codes.Error, jobErr.Error())
			}
		case types.StatusPending, types.StatusRunning, types.StatusCancelled:
		}
	}()

	if err := m.ProvisionVolume(ctx, job); err != nil {
		job.mu.Lock()
		job.Status = types.StatusFailed
		job.Error = err
		job.mu.Unlock()
		return
	}

	job.mu.Lock()
	job.Status = types.StatusCompleted
	job.mu.Unlock()
}

// ProvisionVolume performs the actual volume provisioning.
func (m *Manager) ProvisionVolume(ctx context.Context, job *Job) error {
	tracer := otel.Tracer("job-manager")

	if m.minioClient == nil || m.lvmManager == nil || m.libvirtPool == nil || m.store == nil {
		return fmt.Errorf("job manager dependencies not initialized")
	}

	job.mu.RLock()
	req := job.Request
	job.mu.RUnlock()

	volumeCreated := false
	provisionFailed := false

	downloadRate := m.store.GetAverageRate(ctx, "download", timing.DefaultDownloadRate)
	convertRate := m.store.GetAverageRate(ctx, "convert", timing.DefaultConvertRate)
	estimator := timing.NewEstimator(downloadRate, convertRate)

	var downloadSize, convertSize int64
	convertSize = int64(req.VolumeSizeGB) * 1000 * 1000 * 1000
	if req.ImageType == "qcow2" {
		downloadSize = convertSize / 5
	} else {
		downloadSize = convertSize
	}

	dlWeight, cvWeight := estimator.EstimateWeights(downloadSize, convertSize)
	job.mu.Lock()
	job.downloadWeight = dlWeight
	job.convertWeight = cvWeight
	job.mu.Unlock()

	job.UpdateProgress("initializing", 0, 0, 0)

	imageCtx, imageSpan := tracer.Start(ctx, "getOrDownloadImage",
		trace.WithAttributes(
			attribute.String("image.url", req.ImageURL),
			attribute.String("image.type", req.ImageType),
		))
	defer imageSpan.End()

	imagePath, err := m.getOrDownloadImage(imageCtx, req, job)
	if err != nil {
		imageSpan.RecordError(err)
		imageSpan.SetStatus(codes.Error, "failed to acquire image")
		return fmt.Errorf("failed to get image: %w", err)
	}
	imageSpan.SetAttributes(attribute.String("image.path", imagePath))
	imageSpan.SetStatus(codes.Ok, "image acquired successfully")

	job.mu.RLock()
	cacheHit := job.CacheHit
	job.mu.RUnlock()
	if cacheHit {
		job.mu.Lock()
		job.downloadWeight = 0
		job.convertWeight = 1
		job.mu.Unlock()
	}

	volumeCtx, volumeSpan := tracer.Start(ctx, "createVolume",
		trace.WithAttributes(
			attribute.String("volume.name", req.VolumeName),
			attribute.Int("volume.size_gb", req.VolumeSizeGB),
		))
	defer volumeSpan.End()

	if err := m.lvmManager.CreateVolume(volumeCtx, req.VolumeName, req.VolumeSizeGB); err != nil {
		volumeSpan.RecordError(err)
		volumeSpan.SetStatus(codes.Error, "failed to create volume")
		provisionFailed = true
		return fmt.Errorf("failed to create volume: %w", err)
	}
	volumeCreated = true
	volumeSpan.SetStatus(codes.Ok, "volume created successfully")

	defer func() {
		if volumeCreated && provisionFailed {
			logrus.WithFields(logrus.Fields{
				"job_id":      job.ID,
				"volume_name": req.VolumeName,
			}).Warn("Rolling back: deleting failed volume")

			if deleteErr := m.lvmManager.DeleteVolume(volumeCtx, req.VolumeName); deleteErr != nil {
				logrus.WithError(deleteErr).WithFields(logrus.Fields{
					"job_id":      job.ID,
					"volume_name": req.VolumeName,
				}).Error("Rollback failed: could not delete volume")

				job.mu.Lock()
				job.Error = fmt.Errorf("provision failed + rollback failed: %w", deleteErr)
				job.mu.Unlock()
			}
		}
	}()

	populateCtx, populateSpan := tracer.Start(ctx, "populateVolume",
		trace.WithAttributes(
			attribute.String("volume.name", req.VolumeName),
			attribute.String("image.path", imagePath),
			attribute.String("image.type", req.ImageType),
		))
	defer populateSpan.End()

	if err := m.lvmManager.PopulateVolume(
		populateCtx, imagePath, req.VolumeName, req.ImageType, job, m.store, job.ID,
	); err != nil {
		populateSpan.RecordError(err)
		populateSpan.SetStatus(codes.Error, "failed to populate volume")
		provisionFailed = true
		return fmt.Errorf("failed to populate volume: %w", err)
	}
	populateSpan.SetStatus(codes.Ok, "volume populated successfully")

	job.UpdateProgress("finalizing", 100, 0, 0)
	return nil
}

// urlCacheKey returns a safe filesystem cache key derived from a URL by SHA256-hashing it.
func urlCacheKey(rawURL string) string {
	h := sha256.Sum256([]byte(rawURL))
	return hex.EncodeToString(h[:])
}

// parseChecksumFile extracts a 64-char hex SHA256 hash from checksum file content.
func parseChecksumFile(data []byte) (string, error) {
	checksum := strings.TrimSpace(string(data))
	if fields := strings.Fields(checksum); len(fields) > 0 {
		checksum = fields[0]
	}
	if len(checksum) != 64 {
		return "", fmt.Errorf("invalid checksum format: expected 64 hex characters, got %d", len(checksum))
	}
	return checksum, nil
}

// getOrDownloadImage checks cache or downloads image and returns the path.
func (m *Manager) getOrDownloadImage(ctx context.Context, req types.ProvisionRequest, job *Job) (string, error) {
	tracer := otel.Tracer("job-manager")

	if m.minioClient == nil || m.lvmManager == nil || m.libvirtPool == nil || m.store == nil {
		return "", fmt.Errorf("job manager dependencies not initialized")
	}

	cacheKey := urlCacheKey(req.ImageURL)

	checksumCtx, checksumSpan := tracer.Start(ctx, "getImageChecksum",
		trace.WithAttributes(attribute.String("image.url", req.ImageURL)))
	remoteChecksum, remoteChecksumErr := m.getImageChecksum(checksumCtx, req.ImageURL)
	hasRemoteChecksum := remoteChecksumErr == nil
	if !hasRemoteChecksum {
		checksumSpan.RecordError(remoteChecksumErr)
		checksumSpan.SetStatus(codes.Error, "remote checksum unavailable")
		logrus.WithError(remoteChecksumErr).Debug("Remote checksum not available from MinIO")
	} else {
		checksumSpan.SetAttributes(attribute.String("image.checksum", remoteChecksum))
		checksumSpan.SetStatus(codes.Ok, "checksum retrieved")
	}
	checksumSpan.End()

	_, cacheSpan := tracer.Start(ctx, "checkImageCache",
		trace.WithAttributes(
			attribute.String("cache.key", cacheKey),
			attribute.String("image.url", req.ImageURL),
		))
	cachedImage, cacheCheckErr := m.libvirtPool.CheckCache(cacheKey)
	if cacheCheckErr != nil {
		cacheSpan.RecordError(cacheCheckErr)
		logrus.WithError(cacheCheckErr).Warn("Failed to check image cache, proceeding with download")
	}

	if cachedImage != nil {
		if !hasRemoteChecksum {
			cacheSpan.SetAttributes(
				attribute.String("cache.result", "hit_unverified"),
				attribute.String("cache.path", cachedImage.Path),
			)
			cacheSpan.SetStatus(codes.Ok, "cache hit (unverified)")
			cacheSpan.End()
			logrus.WithFields(logrus.Fields{
				"job_id":    job.ID,
				"image_url": req.ImageURL,
				"cache_key": cacheKey,
			}).Info("Using cached image (remote checksum unavailable for verification)")
			job.mu.Lock()
			job.CacheHit = true
			job.ImagePath = cachedImage.Path
			job.mu.Unlock()
			if m.metrics != nil {
				m.metrics.RecordCacheHit()
			}
			return cachedImage.Path, nil
		}

		if cachedImage.Checksum == remoteChecksum {
			cacheSpan.SetAttributes(
				attribute.String("cache.result", "hit"),
				attribute.String("cache.path", cachedImage.Path),
				attribute.String("image.checksum", remoteChecksum),
			)
			cacheSpan.SetStatus(codes.Ok, "cache hit (verified)")
			cacheSpan.End()
			logrus.WithFields(logrus.Fields{
				"job_id":      job.ID,
				"image_url":   req.ImageURL,
				"checksum":    remoteChecksum,
				"cached_path": cachedImage.Path,
			}).Info("Using cached image (checksum verified)")
			job.mu.Lock()
			job.CacheHit = true
			job.ImagePath = cachedImage.Path
			job.mu.Unlock()
			if m.metrics != nil {
				m.metrics.RecordCacheHit()
			}
			return cachedImage.Path, nil
		}

		logrus.WithFields(logrus.Fields{
			"job_id":          job.ID,
			"image_url":       req.ImageURL,
			"stored_checksum": cachedImage.Checksum,
			"remote_checksum": remoteChecksum,
		}).Warn("Cached image is stale (checksum mismatch), re-downloading")
		_ = m.libvirtPool.DeleteImage(cachedImage.Path)
	}

	cacheSpan.SetAttributes(attribute.String("cache.result", "miss"))
	cacheSpan.SetStatus(codes.Ok, "cache miss")
	cacheSpan.End()
	if m.metrics != nil {
		m.metrics.RecordCacheMiss()
	}

	logrus.WithFields(logrus.Fields{
		"job_id":    job.ID,
		"image_url": req.ImageURL,
	}).Info("Image not cached, downloading")

	_, allocSpan := tracer.Start(ctx, "allocateImageFile",
		trace.WithAttributes(attribute.String("cache.key", cacheKey)))
	imagePath, err := m.libvirtPool.AllocateImageFile(cacheKey)
	if err != nil {
		allocSpan.RecordError(err)
		allocSpan.SetStatus(codes.Error, "failed to allocate cache file")
		allocSpan.End()
		return "", fmt.Errorf("failed to allocate cache file: %w", err)
	}
	allocSpan.SetAttributes(attribute.String("image.path", imagePath))
	allocSpan.SetStatus(codes.Ok, "cache file allocated")
	allocSpan.End()

	job.UpdateProgress("downloading", 0, 0, 0)

	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("context canceled before download: %w", err)
	}

	downloadStart := time.Now()
	if err := m.minioClient.DownloadImageToPath(ctx, req.ImageURL, imagePath, job); err != nil {
		_ = m.libvirtPool.DeleteImage(imagePath)
		return "", fmt.Errorf("failed to download image: %w", err)
	}
	if downloadDuration := time.Since(downloadStart); downloadDuration > 0 {
		if stat, err := os.Stat(imagePath); err == nil {
			_ = m.store.SaveStageRate(ctx, storage.StageRate{
				Stage:          "download",
				RateBPS:        float64(stat.Size()) / downloadDuration.Seconds(),
				BytesProcessed: stat.Size(),
				DurationMS:     downloadDuration.Milliseconds(),
				JobID:          job.ID,
				CreatedAt:      time.Now(),
			})
			if m.metrics != nil {
				m.metrics.RecordImageDownload(req.ImageType, float64(stat.Size()))
				m.metrics.RecordStageDuration("download", downloadDuration.Seconds())
			}
		}
	}

	localChecksum, checksumErr := libvirt.CalculateChecksum(imagePath)
	if checksumErr != nil {
		logrus.WithError(checksumErr).Warn("Failed to calculate local checksum for downloaded image")
	} else {
		if err := m.libvirtPool.CreateCacheEntry(imagePath, localChecksum); err != nil {
			logrus.WithError(err).Warn("Failed to create cache entry")
		}
	}

	logrus.WithFields(logrus.Fields{
		"job_id":          job.ID,
		"image_path":      imagePath,
		"local_checksum":  localChecksum,
		"remote_checksum": remoteChecksum,
	}).Info("Image downloaded and cached")

	job.mu.Lock()
	job.CacheHit = false
	job.ImagePath = imagePath
	job.mu.Unlock()
	return imagePath, nil
}

// getImageChecksum retrieves the SHA256 checksum from MinIO .sha256 file.
func (m *Manager) getImageChecksum(ctx context.Context, imageURL string) (string, error) {
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

	checksumData, err := m.minioClient.GetObjectContent(ctx, bucketName, checksumObjectName)
	if err != nil {
		return "", fmt.Errorf("checksum file not found or unreadable: %w", err)
	}

	checksum, err := parseChecksumFile(checksumData)
	if err != nil {
		return "", fmt.Errorf("invalid checksum file content: %w", err)
	}

	return checksum, nil
}

// GetActiveJobs returns the count of active jobs.
func (m *Manager) GetActiveJobs() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, job := range m.jobs {
		job.mu.RLock()
		s := job.Status
		job.mu.RUnlock()
		if s == types.StatusRunning || s == types.StatusPending {
			count++
		}
	}
	return count
}

// GetJobCacheInfo returns cache information for a completed job.
func (m *Manager) GetJobCacheInfo(jobID string) (bool, string, error) {
	m.mu.RLock()
	job, exists := m.jobs[jobID]
	m.mu.RUnlock()

	if !exists {
		return false, "", fmt.Errorf("job not found: %s", jobID)
	}

	job.mu.RLock()
	defer job.mu.RUnlock()

	if job.Status != types.StatusCompleted {
		return false, "", fmt.Errorf("job not completed: %s", job.Status)
	}
	return job.CacheHit, job.ImagePath, nil
}

// ListCachedImages returns a list of all cached images.
func (m *Manager) ListCachedImages() ([]*libvirt.ImageCache, error) {
	if m.libvirtPool == nil {
		return nil, fmt.Errorf("job manager dependencies not initialized")
	}
	images, err := m.libvirtPool.ListCachedImages()
	if err != nil {
		return nil, fmt.Errorf("failed to list cached images: %w", err)
	}
	return images, nil
}

// FetchImageToCache starts a job to fetch and cache an image without creating a volume.
func (m *Manager) FetchImageToCache(ctx context.Context, req types.FetchImageToCacheRequest) (string, error) {
	jobID := uuid.New().String()

	// Detached context — the HTTP request context is cancelled when the response is sent.
	jobCtx, cancel := context.WithTimeout(context.Background(), m.jobTimeout)

	job := &Job{
		ID:         jobID,
		Status:     types.StatusPending,
		Request:    types.ProvisionRequest{ImageURL: req.ImageURL},
		Progress:   &types.ProgressInfo{Stage: "initializing", StagePercent: 0, OverallPercent: 0},
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		cancelFunc: cancel,
	}

	m.mu.Lock()
	m.jobs[jobID] = job
	m.mu.Unlock()

	go m.runCacheJob(jobCtx, job) //nolint:contextcheck // intentional: job outlives the HTTP request
	return jobID, nil
}

// runCacheJob executes a cache-only job (download image to cache without volume creation).
func (m *Manager) runCacheJob(ctx context.Context, job *Job) {
	if m.minioClient == nil || m.lvmManager == nil || m.libvirtPool == nil || m.store == nil {
		job.mu.Lock()
		job.Status = types.StatusFailed
		job.Error = fmt.Errorf("job manager dependencies not initialized")
		job.UpdatedAt = time.Now()
		job.mu.Unlock()
		return
	}

	tracer := otel.Tracer("libvirt-volume-provisioner")
	ctx, span := tracer.Start(ctx, "runCacheJob",
		trace.WithAttributes(
			attribute.String("job.id", job.ID),
			attribute.String("job.image_url", job.Request.ImageURL),
		))
	defer span.End()

	select {
	case m.semaphore <- struct{}{}:
		defer func() { <-m.semaphore }()
	case <-ctx.Done():
		job.mu.Lock()
		job.Status = types.StatusFailed
		job.UpdatedAt = time.Now()
		job.mu.Unlock()
		m.syncToDatabase(ctx, job)
		return
	}

	job.mu.Lock()
	job.Status = types.StatusRunning
	jobStart := job.CreatedAt
	job.UpdatedAt = time.Now()
	job.mu.Unlock()
	m.syncToDatabase(ctx, job)
	if m.metrics != nil {
		m.metrics.RecordJobStart()
	}

	defer func() {
		job.mu.Lock()
		job.UpdatedAt = time.Now()
		status := job.Status
		jobErr := job.Error
		job.mu.Unlock()

		m.syncToDatabase(ctx, job)

		if m.metrics != nil {
			m.metrics.RecordJobEnd(string(status), time.Since(jobStart).Seconds())
		}

		switch status {
		case types.StatusCompleted:
			span.SetStatus(codes.Ok, "cache job completed successfully")
		case types.StatusFailed:
			if jobErr != nil {
				span.SetStatus(codes.Error, jobErr.Error())
			}
		case types.StatusPending, types.StatusRunning, types.StatusCancelled:
		}
	}()

	job.mu.RLock()
	req := job.Request
	job.mu.RUnlock()

	imagePath, err := m.getOrDownloadImage(ctx, req, job)
	if err != nil {
		job.mu.Lock()
		job.Status = types.StatusFailed
		job.Error = err
		job.mu.Unlock()
		return
	}

	job.mu.Lock()
	job.ImagePath = imagePath
	job.Status = types.StatusCompleted
	job.mu.Unlock()
}

// CleanupCompletedJobs removes old completed jobs, keeping the 100 most recent.
func (m *Manager) CleanupCompletedJobs() {
	m.mu.Lock()
	defer m.mu.Unlock()

	type entry struct {
		id        string
		createdAt time.Time
	}
	var completed []entry
	for id, job := range m.jobs {
		job.mu.RLock()
		s := job.Status
		t := job.CreatedAt
		job.mu.RUnlock()
		if s == types.StatusCompleted || s == types.StatusFailed || s == types.StatusCancelled {
			completed = append(completed, entry{id: id, createdAt: t})
		}
	}

	if len(completed) > 100 {
		// Sort oldest-first so we delete the oldest entries
		sort.Slice(completed, func(i, j int) bool {
			return completed[i].createdAt.Before(completed[j].createdAt)
		})
		for i := 0; i < len(completed)-100; i++ {
			delete(m.jobs, completed[i].id)
		}
	}
}
