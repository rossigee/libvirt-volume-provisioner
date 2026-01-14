// Package types defines common data structures used throughout the
// libvirt-volume-provisioner application, including API request/response types.
//
//nolint:revive // package name 'types' is standard for data structure definitions
package types

import "time"

// ProvisionRequest represents a volume provisioning request.
type ProvisionRequest struct {
	ImageURL     string `binding:"required"       json:"image_url"`
	VolumeName   string `binding:"required"       json:"volume_name"`
	VolumeSizeGB int    `binding:"required,min=1" json:"volume_size_gb"`
	ImageType    string `json:"image_type"`
}

// ProvisionResponse represents the response to a provisioning request.
type ProvisionResponse struct {
	JobID     string `json:"job_id"`
	CacheHit  bool   `json:"cache_hit,omitempty"`
	ImagePath string `json:"image_path,omitempty"`
}

// JobStatus represents the status of a provisioning job.
type JobStatus string

// Job status constants.
const (
	// StatusPending indicates the job is queued but not yet started.
	StatusPending JobStatus = "pending"
	// StatusRunning indicates the job is currently executing.
	StatusRunning JobStatus = "running"
	// StatusCompleted indicates the job finished successfully.
	StatusCompleted JobStatus = "completed"
	// StatusFailed indicates the job finished with an error.
	StatusFailed JobStatus = "failed"
)

// ProgressInfo represents progress information for a job.
type ProgressInfo struct {
	Stage          string  `json:"stage"`
	Percent        float64 `json:"percent"`
	BytesProcessed int64   `json:"bytes_processed"`
	BytesTotal     int64   `json:"bytes_total"`
}

// StatusResponse represents the response to a status query.
type StatusResponse struct {
	JobID         string        `json:"job_id"`
	Status        JobStatus     `json:"status"`
	Progress      *ProgressInfo `json:"progress,omitempty"`
	Error         string        `json:"error,omitempty"`
	CorrelationID string        `json:"correlation_id,omitempty"`
	CacheHit      *bool         `json:"cache_hit,omitempty"`
	ImagePath     string        `json:"image_path,omitempty"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
}

// ErrorResponse represents an error response.
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	Code    int    `json:"code"`
}

// HealthResponse represents a health check response.
type HealthResponse struct {
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
	Version   string    `json:"version"`
	Uptime    string    `json:"uptime"`
}
