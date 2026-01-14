//go:build integration
// +build integration

package integration

import "time"

// ProvisionRequest represents a volume provisioning request
type ProvisionRequest struct {
	ImageURL      string `json:"image_url"`
	VolumeName    string `json:"volume_name"`
	VolumeSizeGB  int    `json:"volume_size_gb"`
	ImageType     string `json:"image_type,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
}

// ProvisionResponse represents the response to a provisioning request
type ProvisionResponse struct {
	JobID string `json:"job_id"`
}

// StatusResponse represents the response to a status query
type StatusResponse struct {
	JobID         string    `json:"job_id"`
	Status        string    `json:"status"`
	Progress      *Progress `json:"progress,omitempty"`
	Error         string    `json:"error,omitempty"`
	CorrelationID string    `json:"correlation_id,omitempty"`
	CacheHit      *bool     `json:"cache_hit,omitempty"`
	ImagePath     string    `json:"image_path,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Progress represents provisioning progress information
type Progress struct {
	Stage          string  `json:"stage"`
	Percent        float64 `json:"percent"`
	BytesProcessed int64   `json:"bytes_processed"`
	BytesTotal     int64   `json:"bytes_total"`
}
