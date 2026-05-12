package metrics

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// createMockMetrics creates metrics without actual Prometheus registration for testing
func createMockMetrics() *Metrics {
	return &Metrics{}
}

func TestCacheHitRatio(t *testing.T) {
	metrics := createMockMetrics()

	// Test initial state
	assert.Equal(t, int64(0), metrics.cacheHits.Load())
	assert.Equal(t, int64(0), metrics.cacheMisses.Load())

	// Test cache hit
	metrics.RecordCacheHit()
	assert.Equal(t, int64(1), metrics.cacheHits.Load())
	assert.Equal(t, int64(0), metrics.cacheMisses.Load())

	// Test cache miss
	metrics.RecordCacheMiss()
	assert.Equal(t, int64(1), metrics.cacheHits.Load())
	assert.Equal(t, int64(1), metrics.cacheMisses.Load())

	// Test another hit
	metrics.RecordCacheHit()
	assert.Equal(t, int64(2), metrics.cacheHits.Load())
	assert.Equal(t, int64(1), metrics.cacheMisses.Load())
}

func TestRecordImageDownload(t *testing.T) {
	metrics := createMockMetrics()

	// Test image download recording doesn't panic
	assert.NotPanics(t, func() {
		metrics.RecordImageDownload("qcow2", 1024*1024*100) // 100MB
		metrics.RecordImageDownload("raw", 1024*1024*50)    // 50MB
	})
}

func TestRecordImageError(t *testing.T) {
	metrics := createMockMetrics()

	// Test error recording doesn't panic
	assert.NotPanics(t, func() {
		metrics.RecordImageError("download", "timeout")
		metrics.RecordImageError("checksum", "mismatch")
	})
}

func TestRecordStorageOperation(t *testing.T) {
	metrics := createMockMetrics()

	// Test storage operation recording doesn't panic
	assert.NotPanics(t, func() {
		metrics.RecordStorageOperation("create", "success")
		metrics.RecordStorageOperation("delete", "error")
	})
}

func TestRecordJobMetrics(t *testing.T) {
	metrics := createMockMetrics()

	// Test job lifecycle doesn't panic
	assert.NotPanics(t, func() {
		metrics.RecordJobStart()
		metrics.RecordJobEnd("completed", 30.5)
		metrics.RecordJobStart()
		metrics.RecordJobEnd("failed", 15.2)
	})
}

func TestUpdateHealthStatus(t *testing.T) {
	metrics := createMockMetrics()

	// Test health status updates don't panic
	assert.NotPanics(t, func() {
		metrics.UpdateHealthStatus(true)
		metrics.UpdateHealthStatus(false)
		metrics.UpdateDependencyStatus("minio", true)
		metrics.UpdateDependencyStatus("lvm", false)
	})
}
