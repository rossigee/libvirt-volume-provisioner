package metrics

import (
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds all application metrics
type Metrics struct {
	// Registry is the custom prometheus registry for this instance
	Registry *prometheus.Registry

	// Request metrics
	RequestsTotal   *prometheus.CounterVec
	RequestDuration *prometheus.HistogramVec

	// Job metrics
	ActiveJobs  prometheus.Gauge
	JobsTotal   *prometheus.CounterVec
	JobDuration *prometheus.HistogramVec

	// Cache metrics
	CacheHits     prometheus.Counter
	CacheMisses   prometheus.Counter
	CacheHitRatio prometheus.Gauge
	cacheHits     atomic.Int64
	cacheMisses   atomic.Int64

	// Image metrics
	ImagesDownloaded  prometheus.Counter
	ImageDownloadSize *prometheus.HistogramVec
	ImageErrors       *prometheus.CounterVec

	// Storage metrics
	StorageOperations *prometheus.CounterVec
	StorageErrors     *prometheus.CounterVec

	// Stage timing metrics
	StageDuration   *prometheus.HistogramVec
	StageThroughput *prometheus.GaugeVec

	// Health metrics
	HealthStatus   prometheus.Gauge
	DependenciesUp *prometheus.GaugeVec
}

// NewMetrics creates and registers all metrics
func NewMetrics() *Metrics {
	// Create a custom registry to avoid conflicts with global registry
	reg := prometheus.NewRegistry()

	m := &Metrics{
		Registry: reg,
		// Request metrics
		RequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "libvirt_volume_provisioner_requests_total",
				Help: "Total number of HTTP requests by method, endpoint, and status",
			},
			[]string{"method", "endpoint", "status"},
		),

		RequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "libvirt_volume_provisioner_request_duration_seconds",
				Help:    "HTTP request duration in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"method", "endpoint"},
		),

		// Job metrics
		ActiveJobs: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "libvirt_volume_provisioner_active_jobs",
				Help: "Number of currently active jobs",
			},
		),

		JobsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "libvirt_volume_provisioner_jobs_total",
				Help: "Total number of jobs by status",
			},
			[]string{"status"},
		),

		JobDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "libvirt_volume_provisioner_job_duration_seconds",
				Help:    "Job execution duration in seconds",
				Buckets: []float64{1, 5, 10, 30, 60, 120, 300, 600},
			},
			[]string{"status"},
		),

		// Cache metrics
		CacheHits: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "libvirt_volume_provisioner_cache_hits_total",
				Help: "Total number of cache hits",
			},
		),

		CacheMisses: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "libvirt_volume_provisioner_cache_misses_total",
				Help: "Total number of cache misses",
			},
		),

		CacheHitRatio: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "libvirt_volume_provisioner_cache_hit_ratio",
				Help: "Cache hit ratio (0.0 to 1.0)",
			},
		),

		// Image metrics
		ImagesDownloaded: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "libvirt_volume_provisioner_images_downloaded_total",
				Help: "Total number of images downloaded",
			},
		),

		ImageDownloadSize: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "libvirt_volume_provisioner_image_download_size_bytes",
				Help:    "Size of downloaded images in bytes",
				Buckets: []float64{1024, 1024 * 1024, 10 * 1024 * 1024, 100 * 1024 * 1024, 1024 * 1024 * 1024},
			},
			[]string{"image_type"},
		),

		ImageErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "libvirt_volume_provisioner_image_errors_total",
				Help: "Total number of image operation errors",
			},
			[]string{"operation", "error_type"},
		),

		// Storage metrics
		StorageOperations: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "libvirt_volume_provisioner_storage_operations_total",
				Help: "Total number of storage operations",
			},
			[]string{"operation", "result"},
		),

		StorageErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "libvirt_volume_provisioner_storage_errors_total",
				Help: "Total number of storage operation errors",
			},
			[]string{"operation", "error_type"},
		),

		// Stage timing metrics
		StageDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "libvirt_volume_provisioner_stage_duration_seconds",
				Help:    "Stage execution duration in seconds",
				Buckets: []float64{0.1, 0.5, 1, 5, 10, 30, 60, 120, 300, 600},
			},
			[]string{"stage"},
		),

		StageThroughput: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "libvirt_volume_provisioner_stage_throughput_bytes_per_second",
				Help: "Stage throughput in bytes per second (moving average)",
			},
			[]string{"stage"},
		),

		// Health metrics
		HealthStatus: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "libvirt_volume_provisioner_health_status",
				Help: "Overall health status (1 = healthy, 0 = unhealthy)",
			},
		),

		DependenciesUp: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "libvirt_volume_provisioner_dependencies_up",
				Help: "Status of external dependencies (1 = up, 0 = down)",
			},
			[]string{"dependency"},
		),
	}

	// Register all metrics with the custom registry
	reg.MustRegister(
		m.RequestsTotal,
		m.RequestDuration,
		m.ActiveJobs,
		m.JobsTotal,
		m.JobDuration,
		m.CacheHits,
		m.CacheMisses,
		m.CacheHitRatio,
		m.ImagesDownloaded,
		m.ImageDownloadSize,
		m.ImageErrors,
		m.StorageOperations,
		m.StorageErrors,
		m.StageDuration,
		m.StageThroughput,
		m.HealthStatus,
		m.DependenciesUp,
	)

	return m
}

// RecordCacheHit records a cache hit
func (m *Metrics) RecordCacheHit() {
	if m.CacheHits != nil {
		m.CacheHits.Inc()
	}
	m.cacheHits.Add(1)
	m.updateCacheRatio()
}

// RecordCacheMiss records a cache miss
func (m *Metrics) RecordCacheMiss() {
	if m.CacheMisses != nil {
		m.CacheMisses.Inc()
	}
	m.cacheMisses.Add(1)
	m.updateCacheRatio()
}

// updateCacheRatio calculates and updates the cache hit ratio
func (m *Metrics) updateCacheRatio() {
	if m.CacheHitRatio != nil {
		hits := m.cacheHits.Load()
		misses := m.cacheMisses.Load()
		total := hits + misses
		if total > 0 {
			m.CacheHitRatio.Set(float64(hits) / float64(total))
		}
	}
}

// RecordImageDownload records a successful image download
func (m *Metrics) RecordImageDownload(imageType string, sizeBytes float64) {
	if m.ImagesDownloaded != nil {
		m.ImagesDownloaded.Inc()
	}
	if m.ImageDownloadSize != nil {
		m.ImageDownloadSize.WithLabelValues(imageType).Observe(sizeBytes)
	}
}

// RecordImageError records an image operation error
func (m *Metrics) RecordImageError(operation, errorType string) {
	if m.ImageErrors != nil {
		m.ImageErrors.WithLabelValues(operation, errorType).Inc()
	}
}

// RecordStorageOperation records a storage operation
func (m *Metrics) RecordStorageOperation(operation, result string) {
	if m.StorageOperations != nil {
		m.StorageOperations.WithLabelValues(operation, result).Inc()
	}
}

// RecordStorageError records a storage operation error
func (m *Metrics) RecordStorageError(operation, errorType string) {
	if m.StorageErrors != nil {
		m.StorageErrors.WithLabelValues(operation, errorType).Inc()
	}
}

// RecordJobStart records when a job starts
func (m *Metrics) RecordJobStart() {
	if m.ActiveJobs != nil {
		m.ActiveJobs.Inc()
	}
}

// RecordJobEnd records when a job ends
func (m *Metrics) RecordJobEnd(status string, duration float64) {
	if m.ActiveJobs != nil {
		m.ActiveJobs.Dec()
	}
	if m.JobsTotal != nil {
		m.JobsTotal.WithLabelValues(status).Inc()
	}
	if m.JobDuration != nil {
		m.JobDuration.WithLabelValues(status).Observe(duration)
	}
}

// UpdateHealthStatus updates the overall health status
func (m *Metrics) UpdateHealthStatus(healthy bool) {
	if m.HealthStatus != nil {
		if healthy {
			m.HealthStatus.Set(1)
		} else {
			m.HealthStatus.Set(0)
		}
	}
}

// UpdateDependencyStatus updates the status of an external dependency
func (m *Metrics) UpdateDependencyStatus(dependency string, up bool) {
	if m.DependenciesUp != nil {
		if up {
			m.DependenciesUp.WithLabelValues(dependency).Set(1)
		} else {
			m.DependenciesUp.WithLabelValues(dependency).Set(0)
		}
	}
}

// RecordStageDuration records the duration of a stage (download/convert)
func (m *Metrics) RecordStageDuration(stage string, durationSeconds float64) {
	if m.StageDuration != nil {
		m.StageDuration.WithLabelValues(stage).Observe(durationSeconds)
	}
}

// UpdateStageThroughput updates the throughput gauge for a stage
func (m *Metrics) UpdateStageThroughput(stage string, bytesPerSecond float64) {
	if m.StageThroughput != nil {
		m.StageThroughput.WithLabelValues(stage).Set(bytesPerSecond)
	}
}
