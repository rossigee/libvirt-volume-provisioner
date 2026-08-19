package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	appmetrics "github.com/rossigee/libvirt-volume-provisioner/internal/metrics"
	"github.com/rossigee/libvirt-volume-provisioner/internal/libvirt"
	"github.com/rossigee/libvirt-volume-provisioner/pkg/types"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockJobManager for testing
type MockJobManager struct {
	startJobCalled bool
	lastRequest    types.ProvisionRequest
	activeJobs     int
}

func (m *MockJobManager) StartJob(ctx context.Context, req types.ProvisionRequest) (string, error) {
	m.startJobCalled = true
	m.lastRequest = req
	return "test-job-id", nil
}

func (m *MockJobManager) GetJobStatus(jobID string) (*types.StatusResponse, error) {
	return &types.StatusResponse{
		JobID:     jobID,
		Status:    types.StatusCompleted,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}, nil
}

func (m *MockJobManager) CancelJob(_ context.Context, _ string) error {
	return nil
}

func (m *MockJobManager) GetActiveJobs() int {
	return m.activeJobs
}

func (m *MockJobManager) GetJobCacheInfo(_ string) (bool, string, error) {
	return false, "", nil
}

func (m *MockJobManager) ListCachedImages() ([]*libvirt.ImageCache, error) {
	return []*libvirt.ImageCache{}, nil
}

func (m *MockJobManager) FetchImageToCache(ctx context.Context, req types.FetchImageToCacheRequest) (string, error) {
	return "test-cache-job-id", nil
}

func (m *MockJobManager) DeleteCachedImage(_ string) error { return nil }

type MockVolumeContentManager struct{}

func (m *MockVolumeContentManager) UploadVolumeContent(_ string, _ string, _ io.Reader, _ int64) error {
	return nil
}

func TestNewHandler(t *testing.T) {
	mockManager := &MockJobManager{}
	mockVolumeContentManager := &MockVolumeContentManager{}
	m := appmetrics.NewMetrics()
	handler := NewHandler(mockManager, mockVolumeContentManager, m, "test-version", 2)

	assert.NotNil(t, handler)
	assert.Equal(t, mockManager, handler.jobManager)
	assert.Equal(t, "test-version", handler.version)
}

func TestSetupRoutes(t *testing.T) {
	mockManager := &MockJobManager{}
	handler := NewHandler(mockManager, &MockVolumeContentManager{}, nil, "test-version", 2)

	// Create test router
	router := gin.New()

	// Mock auth middleware
	authMiddleware := func(c *gin.Context) {
		c.Next()
	}

	SetupRoutes(router, handler, authMiddleware)

	// Test that routes are registered
	routes := router.Routes()
	assert.NotEmpty(t, routes)

	// Check specific routes exist
	routePaths := make(map[string]bool)
	for _, route := range routes {
		routePaths[route.Method+" "+route.Path] = true
	}

	assert.True(t, routePaths["POST /api/v1/jobs"])
	assert.True(t, routePaths["GET /api/v1/jobs/:job_id"])
	assert.True(t, routePaths["POST /api/v1/jobs/:job_id/cancel"])
	assert.True(t, routePaths["GET /api/v1/cache/images"])
	assert.True(t, routePaths["POST /api/v1/cache/fetch"])
	assert.True(t, routePaths["DELETE /api/v1/cache/images/:key"])
	assert.True(t, routePaths["GET /health"])
	assert.True(t, routePaths["GET /healthz"])
	assert.True(t, routePaths["GET /livez"])
	assert.True(t, routePaths["GET /metrics"])
}

func setupHealthRouter(handler *Handler) *gin.Engine {
	router := gin.New()
	SetupRoutes(router, handler, func(c *gin.Context) { c.Next() })
	return router
}

func TestHealthCheck(t *testing.T) {
	mockManager := &MockJobManager{}
	handler := NewHandler(mockManager, &MockVolumeContentManager{}, nil, "test-version", 2)
	router := setupHealthRouter(handler)

	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "/health", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "healthy")
	assert.Contains(t, w.Body.String(), "test-version")
}

func TestHealthCheck_ReportsRealUptime(t *testing.T) {
	mockManager := &MockJobManager{}
	handler := NewHandler(mockManager, &MockVolumeContentManager{}, nil, "test-version", 2)
	router := setupHealthRouter(handler)

	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "/health", nil)
	router.ServeHTTP(w, req)

	var resp types.HealthResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp.Uptime)
	assert.NotEqual(t, "unknown", resp.Uptime)
}

func TestHealthCheck_SetsHealthStatusMetric(t *testing.T) {
	mockManager := &MockJobManager{} // GetActiveJobs returns 0 → healthy
	m := appmetrics.NewMetrics()
	handler := NewHandler(mockManager, &MockVolumeContentManager{}, m, "test-version", 2)
	router := setupHealthRouter(handler)

	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "/health", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.InDelta(t, 1.0, testutil.ToFloat64(m.HealthStatus), 0.001)
}

func TestHealthCheck_DegradedSetsMetric(t *testing.T) {
	mockManager := &MockJobManager{activeJobs: 2}
	m := appmetrics.NewMetrics()
	handler := NewHandler(mockManager, &MockVolumeContentManager{}, m, "test-version", 2)
	router := setupHealthRouter(handler)

	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "/health", nil)
	router.ServeHTTP(w, req)

	var resp types.HealthResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "degraded", resp.Status)
	assert.InDelta(t, 0.0, testutil.ToFloat64(m.HealthStatus), 0.001)
}

func TestProvisionVolume_InvalidJSON(t *testing.T) {
	mockManager := &MockJobManager{}
	handler := NewHandler(mockManager, &MockVolumeContentManager{}, nil, "test-version", 2)

	// Create test router
	router := gin.New()

	// Mock auth middleware
	authMiddleware := func(c *gin.Context) {
		c.Next()
	}

	SetupRoutes(router, handler, authMiddleware)

	w := httptest.NewRecorder()
	body := bytes.NewBufferString("invalid json")
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/jobs", body)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid request")
}

func TestProvisionVolume_MissingFields(t *testing.T) {
	mockManager := &MockJobManager{}
	handler := NewHandler(mockManager, &MockVolumeContentManager{}, nil, "test-version", 2)

	// Create test router
	router := gin.New()

	// Mock auth middleware
	authMiddleware := func(c *gin.Context) {
		c.Next()
	}

	SetupRoutes(router, handler, authMiddleware)

	// Test with empty request
	w := httptest.NewRecorder()
	body := bytes.NewBufferString("{}")
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/jobs", body)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "required")
}

func TestProvisionVolume_ValidRequest(t *testing.T) {
	mockManager := &MockJobManager{}
	handler := NewHandler(mockManager, &MockVolumeContentManager{}, nil, "test-version", 2)

	// Create test router
	router := gin.New()

	// Mock auth middleware
	authMiddleware := func(c *gin.Context) {
		c.Next()
	}

	SetupRoutes(router, handler, authMiddleware)

	requestBody := `{
		"image_url": "https://minio.example.com/bucket/image.qcow2",
		"volume_name": "test-volume",
		"volume_size_gb": 10,
		"image_type": "qcow2"
	}`

	w := httptest.NewRecorder()
	body := bytes.NewBufferString(requestBody)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/jobs", body)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusAccepted, w.Code)
	assert.True(t, mockManager.startJobCalled)
	assert.Equal(t, "https://minio.example.com/bucket/image.qcow2", mockManager.lastRequest.ImageURL)
	assert.Equal(t, "test-volume", mockManager.lastRequest.VolumeName)
	assert.Equal(t, 10, mockManager.lastRequest.VolumeSizeGB)
}

func TestListCachedImages(t *testing.T) {
	mockManager := &MockJobManager{}
	handler := NewHandler(mockManager, &MockVolumeContentManager{}, nil, "test-version", 2)

	router := gin.New()
	authMiddleware := func(c *gin.Context) { c.Next() }
	SetupRoutes(router, handler, authMiddleware)

	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/cache/images", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "images")
	assert.Contains(t, w.Body.String(), "count")
}

func TestDeleteCachedImage_InvalidKey(t *testing.T) {
	mockManager := &MockJobManager{}
	handler := NewHandler(mockManager, &MockVolumeContentManager{}, nil, "test-version", 2)

	router := gin.New()
	authMiddleware := func(c *gin.Context) { c.Next() }
	SetupRoutes(router, handler, authMiddleware)

	w := httptest.NewRecorder()
	// Key is shorter than 64 chars — should be rejected
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodDelete,
		"/api/v1/cache/images/tooshort", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid key")
}

func TestDeleteCachedImage_ManagerError(t *testing.T) {
	errMock := &errorDeleteMockJobManager{}
	handler := NewHandler(errMock, &MockVolumeContentManager{}, nil, "test-version", 2)

	router := gin.New()
	authMiddleware := func(c *gin.Context) { c.Next() }
	SetupRoutes(router, handler, authMiddleware)

	key := "a3b4c5d6e7f8a3b4c5d6e7f8a3b4c5d6e7f8a3b4c5d6e7f8a3b4c5d6e7f8a3b4"
	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodDelete,
		"/api/v1/cache/images/"+key, nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "simulated delete error")
}

func TestDeleteCachedImage_ValidKey(t *testing.T) {
	mockManager := &MockJobManager{}
	handler := NewHandler(mockManager, &MockVolumeContentManager{}, nil, "test-version", 2)

	router := gin.New()
	authMiddleware := func(c *gin.Context) { c.Next() }
	SetupRoutes(router, handler, authMiddleware)

	// 64-char hex key
	key := "a3b4c5d6e7f8a3b4c5d6e7f8a3b4c5d6e7f8a3b4c5d6e7f8a3b4c5d6e7f8a3b4"
	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodDelete,
		"/api/v1/cache/images/"+key, nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "deleted")
	assert.Contains(t, w.Body.String(), key)
}

// errorDeleteMockJobManager is a MockJobManager variant whose DeleteCachedImage always errors.
type errorDeleteMockJobManager struct {
	MockJobManager
}

func (m *errorDeleteMockJobManager) DeleteCachedImage(_ string) error {
	return errors.New("simulated delete error")
}

func TestFetchImageToCache_ValidRequest(t *testing.T) {
	mockManager := &MockJobManager{}
	handler := NewHandler(mockManager, &MockVolumeContentManager{}, nil, "test-version", 2)

	// Create test router
	router := gin.New()

	// Mock auth middleware
	authMiddleware := func(c *gin.Context) {
		c.Next()
	}

	SetupRoutes(router, handler, authMiddleware)

	requestBody := `{
		"image_url": "https://minio.example.com/bucket/image.qcow2"
	}`

	w := httptest.NewRecorder()
	body := bytes.NewBufferString(requestBody)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately to prevent goroutines from running
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		"/api/v1/cache/fetch", body)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusAccepted, w.Code)
	assert.Contains(t, w.Body.String(), "test-cache-job-id")
}
