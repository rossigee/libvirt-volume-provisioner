// Package api provides HTTP API handlers for the libvirt-volume-provisioner service,
// including REST endpoints for volume provisioning, job status, and health checks.
package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rossigee/libvirt-volume-provisioner/internal/libvirt"
	"github.com/rossigee/libvirt-volume-provisioner/internal/metrics"
	"github.com/rossigee/libvirt-volume-provisioner/pkg/types"
)

// maxConcurrentJobs must match the semaphore size in jobs.Manager.
const maxConcurrentJobs = 2

// maxRequestBodyBytes caps the size of incoming JSON request bodies.
const maxRequestBodyBytes = 1 * 1024 * 1024 // 1 MB

// JobManager interface for job operations
type JobManager interface {
	StartJob(ctx context.Context, req types.ProvisionRequest) (string, error)
	GetJobStatus(jobID string) (*types.StatusResponse, error)
	CancelJob(ctx context.Context, jobID string) error
	GetActiveJobs() int
	GetJobCacheInfo(jobID string) (cacheHit bool, imagePath string, err error)
	ListCachedImages() ([]*libvirt.ImageCache, error)
	FetchImageToCache(ctx context.Context, req types.FetchImageToCacheRequest) (string, error)
	DeleteCachedImage(cacheKey string) error
}

// Handler handles HTTP API requests
type Handler struct {
	jobManager JobManager
	metrics    *metrics.Metrics
	version    string
	startTime  time.Time
}

// NewHandler creates a new API handler
func NewHandler(jobManager JobManager, metrics *metrics.Metrics, version string) *Handler {
	return &Handler{
		jobManager: jobManager,
		metrics:    metrics,
		version:    version,
		startTime:  time.Now(),
	}
}

// metricsMiddleware tracks request metrics
func metricsMiddleware(m *metrics.Metrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		if m != nil {
			duration := time.Since(start).Seconds()
			status := fmt.Sprintf("%d", c.Writer.Status())
			m.RequestsTotal.WithLabelValues(c.Request.Method, c.FullPath(), status).Inc()
			m.RequestDuration.WithLabelValues(c.Request.Method, c.FullPath()).Observe(duration)
		}
	}
}

// bodySizeLimitMiddleware caps incoming request body size to prevent DoS.
func bodySizeLimitMiddleware(limit int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		c.Next()
	}
}

// isHexString reports whether s consists entirely of lowercase hex characters.
func isHexString(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// SetupRoutes configures the API routes
func SetupRoutes(router *gin.Engine, handler *Handler, authMiddleware gin.HandlerFunc) {
	router.Use(bodySizeLimitMiddleware(maxRequestBodyBytes))
	router.Use(metricsMiddleware(handler.metrics))

	// Public endpoints (no auth required)
	metricsHandler := promhttp.Handler()
	if handler.metrics != nil {
		metricsHandler = promhttp.HandlerFor(handler.metrics.Registry, promhttp.HandlerOpts{})
	}
	router.GET("/metrics", gin.WrapH(metricsHandler))
	router.GET("/health", handler.HealthCheck)
	router.GET("/healthz", handler.HealthCheck)
	router.GET("/livez", handler.HealthCheck)

	// API routes (with auth)
	api := router.Group("/api/v1")
	api.Use(authMiddleware)
	{
		api.POST("/jobs", handler.ProvisionVolume)
		api.GET("/jobs/:job_id", handler.GetJobStatus)
		api.POST("/jobs/:job_id/cancel", handler.CancelJob)
		api.GET("/cache/images", handler.ListCachedImages)
		api.POST("/cache/fetch", handler.FetchImageToCache)
		api.DELETE("/cache/images/:key", handler.DeleteCachedImage)
	}

	// Additional provision route for compatibility
	router.POST("/provision", authMiddleware, handler.ProvisionVolume)
}

// ProvisionVolume handles volume provisioning requests
func (h *Handler) ProvisionVolume(c *gin.Context) {
	var req types.ProvisionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{
			Error:   "invalid request",
			Message: err.Error(),
			Code:    400,
		})
		return
	}

	if req.ImageType == "" {
		req.ImageType = "qcow2"
	}

	if req.ImageURL == "" || req.VolumeName == "" {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{
			Error:   "invalid request",
			Message: "image_url and volume_name are required",
			Code:    400,
		})
		return
	}

	jobID, err := h.jobManager.StartJob(c.Request.Context(), req)
	if err != nil {
		if h.metrics != nil {
			h.metrics.JobsTotal.WithLabelValues("failed").Inc()
		}
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{
			Error:   "failed to start provisioning",
			Message: err.Error(),
			Code:    500,
		})
		return
	}

	c.JSON(http.StatusAccepted, types.ProvisionResponse{JobID: jobID})
}

// GetJobStatus returns the status of a provisioning job
func (h *Handler) GetJobStatus(c *gin.Context) {
	jobID := c.Param("job_id")
	if jobID == "" {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{
			Error:   "invalid request",
			Message: "job_id parameter is required",
			Code:    400,
		})
		return
	}

	status, err := h.jobManager.GetJobStatus(jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, types.ErrorResponse{
			Error:   "job not found",
			Message: err.Error(),
			Code:    404,
		})
		return
	}

	c.JSON(http.StatusOK, status)
}

// CancelJob cancels a running provisioning job
func (h *Handler) CancelJob(c *gin.Context) {
	jobID := c.Param("job_id")
	if jobID == "" {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{
			Error:   "invalid request",
			Message: "job_id parameter is required",
			Code:    400,
		})
		return
	}

	err := h.jobManager.CancelJob(c.Request.Context(), jobID)
	if err != nil {
		if strings.HasPrefix(err.Error(), "job not found") {
			c.JSON(http.StatusNotFound, types.ErrorResponse{
				Error:   "job not found",
				Message: err.Error(),
				Code:    404,
			})
			return
		}
		c.JSON(http.StatusBadRequest, types.ErrorResponse{
			Error:   "failed to cancel job",
			Message: err.Error(),
			Code:    400,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "cancelled",
		"job_id": jobID,
	})
}

// ListCachedImages returns a list of all cached images
func (h *Handler) ListCachedImages(c *gin.Context) {
	cachedImages, err := h.jobManager.ListCachedImages()
	if err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{
			Error:   "failed to list cached images",
			Message: err.Error(),
			Code:    500,
		})
		return
	}

	offset := 0
	limit := 100
	if v := c.Query("offset"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed >= 0 {
			offset = parsed
		}
	}
	if v := c.Query("limit"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	allImages := make([]types.CachedImageInfo, len(cachedImages))
	for i, img := range cachedImages {
		allImages[i] = types.CachedImageInfo{
			Path:     img.Path,
			Size:     img.Size,
			Checksum: img.Checksum,
			ModTime:  img.ModTime,
		}
	}

	total := len(allImages)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}

	c.JSON(http.StatusOK, types.ListCachedImagesResponse{
		Images: allImages[offset:end],
		Count:  total,
		Offset: offset,
		Limit:  limit,
	})
}

// FetchImageToCache handles requests to fetch an image to cache
func (h *Handler) FetchImageToCache(c *gin.Context) {
	var req types.FetchImageToCacheRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{
			Error:   "invalid request",
			Message: err.Error(),
			Code:    400,
		})
		return
	}

	if req.ImageURL == "" {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{
			Error:   "invalid request",
			Message: "image_url is required",
			Code:    400,
		})
		return
	}

	jobID, err := h.jobManager.FetchImageToCache(c.Request.Context(), req)
	if err != nil {
		if h.metrics != nil {
			h.metrics.JobsTotal.WithLabelValues("failed").Inc()
		}
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{
			Error:   "failed to start cache fetch",
			Message: err.Error(),
			Code:    500,
		})
		return
	}

	c.JSON(http.StatusAccepted, types.FetchImageToCacheResponse{JobID: jobID})
}

// DeleteCachedImage handles requests to delete a specific cached image by its 64-char hex key.
func (h *Handler) DeleteCachedImage(c *gin.Context) {
	key := c.Param("key")
	if len(key) != 64 || !isHexString(key) {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{
			Error:   "invalid key: must be 64 lowercase hex characters",
			Code:    400,
		})
		return
	}
	if err := h.jobManager.DeleteCachedImage(key); err != nil {
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{Error: err.Error(), Code: 500})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted", "key": key})
}

// HealthCheck provides service health information
func (h *Handler) HealthCheck(c *gin.Context) {
	activeJobsCount := h.jobManager.GetActiveJobs()
	healthy := activeJobsCount < maxConcurrentJobs

	if h.metrics != nil {
		h.metrics.ActiveJobs.Set(float64(activeJobsCount))
		h.metrics.UpdateHealthStatus(healthy)
	}

	status := "healthy"
	if !healthy {
		status = "degraded"
	}

	response := types.HealthResponse{
		Status:    status,
		Timestamp: time.Now(),
		Version:   h.version,
		Uptime:    time.Since(h.startTime).Round(time.Second).String(),
	}

	c.JSON(http.StatusOK, response)
}
