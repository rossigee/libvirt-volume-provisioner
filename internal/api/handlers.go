// Package api provides HTTP API handlers for the libvirt-volume-provisioner service,
// including REST endpoints for volume provisioning, job status, and health checks.
package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rossigee/libvirt-volume-provisioner/pkg/types"
)

// JobManager interface for job operations
type JobManager interface {
	StartJob(req types.ProvisionRequest) (string, error)
	GetJobStatus(jobID string) (*types.StatusResponse, error)
	CancelJob(jobID string) error
	GetActiveJobs() int
	GetJobCacheInfo(jobID string) (cacheHit bool, imagePath string, err error)
}

// Handler handles HTTP API requests
type Handler struct {
	jobManager JobManager
}

// Metrics
var (
	requestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "libvirt_volume_provisioner_requests_total",
			Help: "Total number of requests by endpoint and method",
		},
		[]string{"method", "endpoint", "status"},
	)

	activeJobsGauge = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "libvirt_volume_provisioner_active_jobs",
			Help: "Number of currently active jobs",
		},
	)

	jobsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "libvirt_volume_provisioner_jobs_total",
			Help: "Total number of jobs by status",
		},
		[]string{"status"},
	)
)

func init() {
	// Register metrics
	prometheus.MustRegister(requestsTotal)
	prometheus.MustRegister(activeJobsGauge)
	prometheus.MustRegister(jobsTotal)
}

// NewHandler creates a new API handler
func NewHandler(jobManager JobManager) *Handler {
	return &Handler{
		jobManager: jobManager,
	}
}

// metricsMiddleware tracks request metrics
func metricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		// Track request metrics
		status := c.Writer.Status()
		requestsTotal.WithLabelValues(c.Request.Method, c.FullPath(), fmt.Sprintf("%d", status)).Inc()
	}
}

// SetupRoutes configures the API routes
func SetupRoutes(router *gin.Engine, handler *Handler, authMiddleware gin.HandlerFunc) {
	// Add metrics middleware to all routes
	router.Use(metricsMiddleware())

	// Public endpoints (no auth required)
	router.GET("/metrics", gin.WrapH(promhttp.Handler()))
	router.GET("/health", handler.HealthCheck)
	router.GET("/healthz", handler.HealthCheck)
	router.GET("/livez", handler.HealthCheck)

	// API routes (with auth)
	api := router.Group("/api/v1")
	api.Use(authMiddleware)
	{
		api.POST("/provision", handler.ProvisionVolume)
		api.GET("/status/:job_id", handler.GetJobStatus)
		api.DELETE("/cancel/:job_id", handler.CancelJob)
	}
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

	// Validate image URL format
	if req.ImageURL == "" || req.VolumeName == "" {
		c.JSON(http.StatusBadRequest, types.ErrorResponse{
			Error:   "invalid request",
			Message: "image_url and volume_name are required",
			Code:    400,
		})
		return
	}

	// Start provisioning job
	jobID, err := h.jobManager.StartJob(req)
	if err != nil {
		jobsTotal.WithLabelValues("failed").Inc()
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{
			Error:   "failed to start provisioning",
			Message: err.Error(),
			Code:    500,
		})
		return
	}

	// Update metrics
	jobsTotal.WithLabelValues("started").Inc()

	response := types.ProvisionResponse{
		JobID: jobID,
	}

	c.JSON(http.StatusAccepted, response)
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

	err := h.jobManager.CancelJob(jobID)
	if err != nil {
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

// HealthCheck provides service health information
func (h *Handler) HealthCheck(c *gin.Context) {
	activeJobsCount := h.jobManager.GetActiveJobs()

	// Update metrics
	activeJobsGauge.Set(float64(activeJobsCount))

	response := types.HealthResponse{
		Status:    "healthy",
		Timestamp: time.Now(),
		Version:   "1.0.0",
		Uptime:    "unknown", // Could be implemented with start time tracking
	}

	// Return degraded status if too many active jobs
	if activeJobsCount > 2 {
		response.Status = "degraded"
		c.JSON(http.StatusOK, response)
		return
	}

	c.JSON(http.StatusOK, response)
}
