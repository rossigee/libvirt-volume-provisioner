package types

import "time"

// ProvisionRequest represents a volume provisioning request
type ProvisionRequest struct {
	ImageURL      string `json:"image_url" binding:"required"`
	VolumeName    string `json:"volume_name" binding:"required"`
	VolumeSizeGB  int    `json:"volume_size_gb" binding:"required,min=1"`
	ImageType     string `json:"image_type" binding:"required,oneof=qcow2 raw"`
	CorrelationID string `json:"correlation_id,omitempty"`
}

// ProvisionResponse represents the response to a provisioning request
type ProvisionResponse struct {
	JobID         string `json:"job_id"`
	Status        string `json:"status"`
	CorrelationID string `json:"correlation_id,omitempty"`
}

// JobStatus represents the status of a provisioning job
type JobStatus string

const (
	StatusPending   JobStatus = "pending"
	StatusRunning   JobStatus = "running"
	StatusCompleted JobStatus = "completed"
	StatusFailed    JobStatus = "failed"
	StatusCancelled JobStatus = "cancelled"
)

// ProgressInfo represents progress information for a job
type ProgressInfo struct {
	Stage          string  `json:"stage"`
	Percent        float64 `json:"percent"`
	BytesProcessed int64   `json:"bytes_processed"`
	BytesTotal     int64   `json:"bytes_total"`
}

// StatusResponse represents the response to a status query
type StatusResponse struct {
	JobID         string        `json:"job_id"`
	Status        JobStatus     `json:"status"`
	Progress      *ProgressInfo `json:"progress,omitempty"`
	Error         string        `json:"error,omitempty"`
	CorrelationID string        `json:"correlation_id,omitempty"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
}

// ErrorResponse represents an error response
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
	Code    int    `json:"code,omitempty"`
}

// HealthResponse represents a health check response
type HealthResponse struct {
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
	Version   string    `json:"version"`
	Uptime    string    `json:"uptime"`
}
