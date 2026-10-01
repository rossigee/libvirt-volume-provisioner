package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
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

// A job's outcome must be attributable to the image it provisioned. Without
// this, once the in-memory job records are gone there is no way to tell which
// artifact a volume was built from, which is exactly what is needed to explain
// a guest booting stale image content.
func TestRecordJobEndWithImageAttributesOutcomeToImage(t *testing.T) {
	m := NewMetrics()

	m.RecordJobEndWithImage("completed", "k8s-node-20261001083428.qcow2", 42.0)
	m.RecordJobEndWithImage("completed", "k8s-node-20261001083428.qcow2", 44.0)
	m.RecordJobEndWithImage("failed", "k8s-node-20260923075129.qcow2", 3.0)

	got := testutil.ToFloat64(m.JobImageTotal.WithLabelValues("completed", "k8s-node-20261001083428.qcow2"))
	assert.Equal(t, 2.0, got, "both completions must be counted against the image")

	failed := testutil.ToFloat64(m.JobImageTotal.WithLabelValues("failed", "k8s-node-20260923075129.qcow2"))
	assert.Equal(t, 1.0, failed)

	// The existing status-only counter must still be incremented, so existing
	// dashboards and alerts keep working.
	assert.Equal(t, 3.0, testutil.ToFloat64(m.JobsTotal.WithLabelValues("completed"))+
		testutil.ToFloat64(m.JobsTotal.WithLabelValues("failed")))
}

// An empty image name must not produce an unbounded set of label values.
func TestRecordJobEndWithImageHandlesEmptyName(t *testing.T) {
	m := NewMetrics()
	assert.NotPanics(t, func() {
		m.RecordJobEndWithImage("completed", "", 1.0)
	})
	assert.Equal(t, 1.0, testutil.ToFloat64(m.JobImageTotal.WithLabelValues("completed", "unknown")))
}
