package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rossigee/libvirt-volume-provisioner/pkg/types"
)

// JobManager interface for job operations
type JobManager interface {
	StartJob(req types.ProvisionRequest) (string, error)
	GetJobStatus(jobID string) (*types.StatusResponse, error)
	CancelJob(jobID string) error
	GetActiveJobs() int
}

// Handler handles HTTP API requests
type Handler struct {
	jobManager JobManager
}

// NewHandler creates a new API handler
func NewHandler(jobManager JobManager) *Handler {
	return &Handler{
		jobManager: jobManager,
	}
}

// SetupRoutes configures the API routes
func SetupRoutes(router *gin.Engine, handler *Handler) {
	api := router.Group("/api/v1")
	{
		api.POST("/provision", handler.ProvisionVolume)
		api.GET("/status/:job_id", handler.GetJobStatus)
		api.DELETE("/cancel/:job_id", handler.CancelJob)
	}

	// Health check endpoint
	router.GET("/health", handler.HealthCheck)
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
		c.JSON(http.StatusInternalServerError, types.ErrorResponse{
			Error:   "failed to start provisioning",
			Message: err.Error(),
			Code:    500,
		})
		return
	}

	response := types.ProvisionResponse{
		JobID:         jobID,
		Status:        "accepted",
		CorrelationID: req.CorrelationID,
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

	if err := h.jobManager.CancelJob(jobID); err != nil {
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
	activeJobs := h.jobManager.GetActiveJobs()

	response := types.HealthResponse{
		Status:    "healthy",
		Timestamp: time.Now(),
		Version:   "1.0.0",
		Uptime:    "unknown", // Could be implemented with start time tracking
	}

	// Return degraded status if too many active jobs
	if activeJobs > 2 {
		response.Status = "degraded"
		c.JSON(http.StatusOK, response)
		return
	}

	c.JSON(http.StatusOK, response)
}
