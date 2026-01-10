package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rossigee/libvirt-volume-provisioner/pkg/types"
	"github.com/stretchr/testify/assert"
)

// MockJobManager for testing
type MockJobManager struct {
	startJobCalled bool
	lastRequest    types.ProvisionRequest
}

func (m *MockJobManager) StartJob(req types.ProvisionRequest) (string, error) {
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

func (m *MockJobManager) CancelJob(jobID string) error {
	return nil
}

func (m *MockJobManager) GetActiveJobs() int {
	return 0
}

func TestNewHandler(t *testing.T) {
	mockManager := &MockJobManager{}
	handler := NewHandler(mockManager)

	assert.NotNil(t, handler)
	assert.Equal(t, mockManager, handler.jobManager)
}

func TestSetupRoutes(t *testing.T) {
	router := gin.New()
	mockManager := &MockJobManager{}
	handler := NewHandler(mockManager)

	SetupRoutes(router, handler)

	// Test that routes are registered
	routes := router.Routes()
	assert.NotEmpty(t, routes)

	// Check specific routes exist
	routePaths := make(map[string]bool)
	for _, route := range routes {
		routePaths[route.Method+" "+route.Path] = true
	}

	assert.True(t, routePaths["POST /api/v1/provision"])
	assert.True(t, routePaths["GET /api/v1/status/:job_id"])
	assert.True(t, routePaths["DELETE /api/v1/cancel/:job_id"])
	assert.True(t, routePaths["GET /health"])
}

func TestHealthCheck(t *testing.T) {
	router := gin.New()
	mockManager := &MockJobManager{}
	handler := NewHandler(mockManager)

	SetupRoutes(router, handler)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/health", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "healthy")
}

func TestProvisionVolume_InvalidJSON(t *testing.T) {
	router := gin.New()
	mockManager := &MockJobManager{}
	handler := NewHandler(mockManager)

	SetupRoutes(router, handler)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/provision", bytes.NewBufferString("invalid json"))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid request")
}

func TestProvisionVolume_MissingFields(t *testing.T) {
	router := gin.New()
	mockManager := &MockJobManager{}
	handler := NewHandler(mockManager)

	SetupRoutes(router, handler)

	// Test with empty request
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/provision", bytes.NewBufferString("{}"))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "required")
}

func TestProvisionVolume_ValidRequest(t *testing.T) {
	router := gin.New()
	mockManager := &MockJobManager{}
	handler := NewHandler(mockManager)

	SetupRoutes(router, handler)

	requestBody := `{
		"image_url": "https://minio.example.com/bucket/image.qcow2",
		"volume_name": "test-volume",
		"volume_size_gb": 10,
		"image_type": "qcow2"
	}`

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/provision", bytes.NewBufferString(requestBody))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusAccepted, w.Code)
	assert.True(t, mockManager.startJobCalled)
	assert.Equal(t, "https://minio.example.com/bucket/image.qcow2", mockManager.lastRequest.ImageURL)
	assert.Equal(t, "test-volume", mockManager.lastRequest.VolumeName)
	assert.Equal(t, 10, mockManager.lastRequest.VolumeSizeGB)
}
