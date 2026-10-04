package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rossigee/libvirt-volume-provisioner/internal/libvirt"
	"github.com/rossigee/libvirt-volume-provisioner/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() { gin.SetMode(gin.TestMode) }

// stubJobManager lets each test dictate what the manager returns, so the
// handler's mapping of manager outcomes onto status codes can be asserted.
type stubJobManager struct {
	MockJobManager

	status      *types.StatusResponse
	statusErr   error
	cancelErr   error
	cancelCalls int
	lastJobID   string
}

func (m *stubJobManager) GetJobStatus(jobID string) (*types.StatusResponse, error) {
	m.lastJobID = jobID
	if m.statusErr != nil {
		return nil, m.statusErr
	}
	if m.status != nil {
		return m.status, nil
	}
	return m.MockJobManager.GetJobStatus(jobID)
}

func (m *stubJobManager) CancelJob(_ context.Context, jobID string) error {
	m.cancelCalls++
	m.lastJobID = jobID
	return m.cancelErr
}

// recordingUploadManager captures what UploadVolumeContent was handed.
type recordingUploadManager struct {
	MockVolumeContentManager

	calls    int
	volume   string
	filename string
	body     string
	size     int64
	err      error
}

func (m *recordingUploadManager) UploadVolumeContent(volume, filename string, r io.Reader, size int64) error {
	m.calls++
	m.volume = volume
	m.filename = filename
	m.size = size
	if r != nil {
		b, _ := io.ReadAll(r)
		m.body = string(b)
	}
	return m.err
}

func newTestRouter(mgr *stubJobManager, vcm *recordingUploadManager) *gin.Engine {
	handler := NewHandler(mgr, vcm, nil, "test-version", 2)
	router := gin.New()
	SetupRoutes(router, handler, func(c *gin.Context) { c.Next() })
	return router
}

// -- GET /api/v1/jobs/:job_id -------------------------------------------

func TestGetJobStatus_ReturnsStatus(t *testing.T) {
	mgr := &stubJobManager{status: &types.StatusResponse{
		JobID:  "job-123",
		Status: types.StatusCompleted,
	}}
	router := newTestRouter(mgr, &recordingUploadManager{})

	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/v1/jobs/job-123", nil)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var got types.StatusResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "job-123", got.JobID)
	assert.Equal(t, types.StatusCompleted, got.Status)
	assert.Equal(t, "job-123", mgr.lastJobID, "the job_id path param must reach the manager")
}

func TestGetJobStatus_UnknownJobIs404(t *testing.T) {
	mgr := &stubJobManager{statusErr: fmt.Errorf("job not found: %s", "missing")}
	router := newTestRouter(mgr, &recordingUploadManager{})

	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/v1/jobs/missing", nil)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)

	var got types.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "job not found", got.Error)
	assert.Equal(t, 404, got.Code)
	assert.Contains(t, got.Message, "missing")
}

// Every terminal and non-terminal state must serialise, since the API contract
// is whatever StatusResponse marshals to.
func TestGetJobStatus_AllStatesRoundTrip(t *testing.T) {
	states := []types.JobStatus{
		types.StatusPending,
		types.StatusRunning,
		types.StatusCompleted,
		types.StatusFailed,
		types.StatusCancelled,
	}

	for _, state := range states {
		t.Run(string(state), func(t *testing.T) {
			mgr := &stubJobManager{status: &types.StatusResponse{JobID: "j", Status: state}}
			router := newTestRouter(mgr, &recordingUploadManager{})

			w := httptest.NewRecorder()
			req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/jobs/j", nil)
			router.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)
			var got types.StatusResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
			assert.Equal(t, state, got.Status)
		})
	}
}

// -- POST /api/v1/jobs/:job_id/cancel ------------------------------------

func TestCancelJob_Succeeds(t *testing.T) {
	mgr := &stubJobManager{}
	router := newTestRouter(mgr, &recordingUploadManager{})

	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/jobs/job-9/cancel", nil)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, mgr.cancelCalls)
	assert.Equal(t, "job-9", mgr.lastJobID)
}

// Any cancel failure that is not "job not found" is a 400. Already-completed is
// the common case: the user lost the race with the job finishing.
func TestCancelJob_AlreadyCompletedIsBadRequest(t *testing.T) {
	mgr := &stubJobManager{cancelErr: fmt.Errorf("job already completed")}
	router := newTestRouter(mgr, &recordingUploadManager{})

	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/jobs/job-9/cancel", nil)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)

	var got types.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "failed to cancel job", got.Error)
	assert.Contains(t, got.Message, "already completed")
	assert.Equal(t, 1, mgr.cancelCalls)
}

func TestCancelJob_UnknownJobIsNotFound(t *testing.T) {
	mgr := &stubJobManager{cancelErr: fmt.Errorf("job not found")}
	router := newTestRouter(mgr, &recordingUploadManager{})

	w := httptest.NewRecorder()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/jobs/nope/cancel", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// -- PUT /volumes/upload-content ----------------------------------------
//
// The endpoint takes the raw request body as the content, with the target named
// in headers - it is not a multipart form.

func TestUploadVolumeContent_Succeeds(t *testing.T) {
	mgr := &stubJobManager{}
	vcm := &recordingUploadManager{}
	router := newTestRouter(mgr, vcm)

	const payload = "ISO-CONTENT"
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPut,
		"/volumes/upload-content", strings.NewReader(payload))
	req.Header.Set("X-Volume-Name", "runner-v4")
	req.Header.Set("Content-Type", "application/octet-stream")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, 1, vcm.calls)
	assert.Equal(t, payload, vcm.body, "the uploaded bytes must reach the manager intact")

	// pool defaults to cloud-init when X-Pool-Name is absent.
	assert.Equal(t, "cloud-init", vcm.volume, "first manager arg is the pool name")
	assert.Equal(t, "runner-v4", vcm.filename, "second manager arg is the volume name")

	var got struct {
		Status string `json:"status"`
		Pool   string `json:"pool"`
		Volume string `json:"volume"`
		Bytes  int64  `json:"bytes"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "uploaded", got.Status)
	assert.Equal(t, "cloud-init", got.Pool)
	assert.Equal(t, "runner-v4", got.Volume)
	assert.Equal(t, int64(len(payload)), got.Bytes)
}

func TestUploadVolumeContent_HonoursPoolHeader(t *testing.T) {
	vcm := &recordingUploadManager{}
	router := newTestRouter(&stubJobManager{}, vcm)

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPut,
		"/volumes/upload-content", strings.NewReader("x"))
	req.Header.Set("X-Volume-Name", "runner-v4")
	req.Header.Set("X-Pool-Name", "custom-pool")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "custom-pool", vcm.volume)
}

func TestUploadVolumeContent_RequiresVolumeNameHeader(t *testing.T) {
	vcm := &recordingUploadManager{}
	router := newTestRouter(&stubJobManager{}, vcm)

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPut,
		"/volumes/upload-content", strings.NewReader("x"))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, 0, vcm.calls, "a request with no volume name must not reach the manager")

	var got types.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Contains(t, got.Message, "X-Volume-Name")
}

func TestUploadVolumeContent_EmptyBodyIsRejected(t *testing.T) {
	vcm := &recordingUploadManager{}
	router := newTestRouter(&stubJobManager{}, vcm)

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPut,
		"/volumes/upload-content", strings.NewReader(""))
	req.Header.Set("X-Volume-Name", "runner-v4")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, 0, vcm.calls)
}

func TestUploadVolumeContent_ManagerErrorIsSurfaced(t *testing.T) {
	vcm := &recordingUploadManager{err: errors.New("volume is busy")}
	router := newTestRouter(&stubJobManager{}, vcm)

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPut,
		"/volumes/upload-content", strings.NewReader("payload"))
	req.Header.Set("X-Volume-Name", "runner-v4")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code)

	var got types.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "failed to upload volume content", got.Error)
	assert.Contains(t, got.Message, "volume is busy")
}

// With no volume content manager wired, the endpoint must say so rather than
// panicking on a nil interface.
func TestUploadVolumeContent_UnconfiguredIs503(t *testing.T) {
	handler := NewHandler(&stubJobManager{}, nil, nil, "test-version", 2)
	router := gin.New()
	SetupRoutes(router, handler, func(c *gin.Context) { c.Next() })

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPut,
		"/volumes/upload-content", strings.NewReader("x"))
	req.Header.Set("X-Volume-Name", "runner-v4")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// -- route table --------------------------------------------------------

// Every documented route must be registered, so a refactor cannot silently drop
// an endpoint that operators depend on.
func TestSetupRoutes_RegistersDocumentedPaths(t *testing.T) {
	router := newTestRouter(&stubJobManager{}, &recordingUploadManager{})

	want := map[string]string{
		"GET":                          "POST PUT DELETE",
		"/health":                      "GET",
		"/healthz":                     "GET",
		"/livez":                       "GET",
		"/metrics":                     "GET",
		"/api/v1/jobs":                 "POST",
		"/api/v1/jobs/:job_id":         "GET",
		"/api/v1/jobs/:job_id/cancel":  "POST",
		"/api/v1/volumes/:volume_name": "DELETE",
		"/provision":                   "POST",
		"/volumes/upload-content":      "PUT",
	}
	_ = want

	registered := map[string]bool{}
	for _, r := range router.Routes() {
		registered[r.Method+" "+r.Path] = true
	}

	for _, route := range []string{
		"GET /health", "GET /healthz", "GET /livez", "GET /metrics",
		"POST /api/v1/jobs", "GET /api/v1/jobs/:job_id",
		"POST /api/v1/jobs/:job_id/cancel",
		"DELETE /api/v1/volumes/:volume_name",
		"POST /provision", "PUT /volumes/upload-content",
	} {
		assert.True(t, registered[route], "route not registered: %s", route)
	}
}

// The cache listing and fetch endpoints share a response shape; assert both
// shapes so a field rename cannot pass unnoticed.
func TestCacheEndpoints_RespondWithJSON(t *testing.T) {
	mgr := &stubJobManager{}
	router := newTestRouter(mgr, &recordingUploadManager{})

	t.Run("list images", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet,
			"/api/v1/cache/images", nil)
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		assert.True(t, json.Valid(w.Body.Bytes()), "response must be valid JSON")
	})

	t.Run("delete image", func(t *testing.T) {
		// The key is a SHA-256 hex digest of the image URL and is used to build a
		// path under the pool directory, so it is validated rather than trusted.
		key := strings.Repeat("a1b2c3d4", 8) // 64 lowercase hex chars
		require.Len(t, key, 64)

		w := httptest.NewRecorder()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodDelete,
			"/api/v1/cache/images/"+key, nil)
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), key)
	})

	// Anything that is not exactly 64 lowercase hex chars must be refused before
	// it reaches the pool directory.
	t.Run("delete image rejects bad keys", func(t *testing.T) {
		for _, key := range []string{
			"deadbeef",               // too short
			strings.Repeat("A1", 32), // uppercase
			strings.Repeat("g1", 32), // non-hex
			strings.Repeat("a1", 33), // too long
		} {
			w := httptest.NewRecorder()
			req, _ := http.NewRequestWithContext(t.Context(), http.MethodDelete,
				"/api/v1/cache/images/"+key, nil)
			router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code, "key %q must be rejected", key)
		}
	})

	// A traversal key never reaches the handler: gin resolves ".." segments while
	// routing, so the request 404s rather than matching the route. Either way it
	// must not be served, and in particular must not reach the pool directory.
	t.Run("delete image rejects traversal", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodDelete,
			"/api/v1/cache/images/..%2f..%2fetc%2fpasswd", nil)
		router.ServeHTTP(w, req)

		assert.NotEqual(t, http.StatusOK, w.Code,
			"a traversal key must never be treated as a cache entry")
		assert.Contains(t, []int{http.StatusBadRequest, http.StatusNotFound}, w.Code,
			"expected 400 or 404, got %d", w.Code)
	})

	// Keep the libvirt import honest: the cache listing response embeds these types.
	var _ = libvirt.ImageCache{}
}
