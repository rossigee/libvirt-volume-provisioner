package minio

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/rossigee/libvirt-volume-provisioner/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeS3 answers just enough of the S3 API for minio-go v7.
//
// StatObject is not a HEAD in this client: it issues GET /<bucket>/ and parses a
// ListBucketResult, so the fake has to return real S3 XML for that. GetObject is
// GET /<bucket>/<key> and must honour a Range header for the resume path to be
// observable rather than assumed.
type fakeS3 struct {
	mu        sync.Mutex
	body      []byte
	key       string
	getCount  int
	headCount int
	ranges    []string // Range header of each object GET, empty when absent
	listCode  int
	getCode   int
}

func newFakeS3(t *testing.T, body []byte) (*fakeS3, string) {
	t.Helper()
	f := &fakeS3{body: body, key: testObject, listCode: http.StatusOK, getCode: http.StatusOK}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()

		// Service root: ListBuckets, used only by the client construction probe.
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>` +
				`<ListAllMyBucketsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">` +
				`<Buckets></Buckets></ListAllMyBucketsResult>`))
			return
		}

		// Bucket listing, which is how this client implements StatObject.
		if isBucketPath(r.URL.Path) {
			if f.listCode != http.StatusOK {
				w.WriteHeader(f.listCode)
				return
			}
			w.Header().Set("Content-Type", "application/xml")
			w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
			_, _ = fmt.Fprintf(w,
				`<?xml version="1.0" encoding="UTF-8"?>`+
					`<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`+
					`<Name>bucket</Name><IsTruncated>false</IsTruncated>`+
					`<Contents><Key>%s</Key><Size>%d</Size>`+
					`<LastModified>Mon, 02 Jan 2006 15:04:05 GMT</LastModified>`+
					`<ETag>&quot;d41d8cd98f00b204e9800998ecf8427e&quot;</ETag>`+
					`</Contents></ListBucketResult>`, f.key, len(f.body))
			return
		}

		// Object metadata or body. StatObject in this client costs a HEAD as
		// well as the bucket listing, so the two are counted separately -
		// only a GET may carry a Range header.
		if r.Method == http.MethodHead {
			f.headCount++
		} else {
			f.getCount++
		}
		rng := r.Header.Get("Range")
		if rng != "" {
			f.ranges = append(f.ranges, rng)
		}

		if f.getCode != http.StatusOK {
			w.WriteHeader(f.getCode)
			return
		}

		start, end := 0, len(f.body)-1
		if rng != "" {
			var err error
			start, end, err = parseRange(rng, len(f.body))
			if err != nil {
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
		}
		chunk := f.body[start : end+1]
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(len(chunk)))
		w.Header().Set("Accept-Ranges", "bytes")
		// ObjectInfo can be built from response headers as well as from listing
		// XML, and this client reads Last-Modified from here, so it has to be
		// present in RFC1123 or StatObject fails to parse it.
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		w.Header().Set("ETag", `"d41d8cd98f00b204e9800998ecf8427e"`)
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodHead {
			// net/http discards the body for HEAD; Content-Length above stands.
			return
		}
		_, _ = w.Write(chunk)
	}))
	t.Cleanup(srv.Close)

	return f, srv.URL
}

// isBucketPath reports whether the path addresses the bucket itself, i.e. the
// ListObjects call, rather than an object within it.
func isBucketPath(p string) bool {
	trimmed := strings.Trim(p, "/")
	return trimmed != "" && !strings.Contains(trimmed, "/")
}

// parseRange understands the "bytes=start-end" form minio-go emits.
func parseRange(header string, size int) (int, int, error) {
	spec := strings.TrimPrefix(header, "bytes=")
	startStr, endStr, ok := strings.Cut(spec, "-")
	if !ok {
		return 0, 0, fmt.Errorf("malformed Range %q", header)
	}
	start, err := strconv.Atoi(startStr)
	if err != nil {
		return 0, 0, err
	}
	end := size - 1
	if endStr != "" {
		if end, err = strconv.Atoi(endStr); err != nil {
			return 0, 0, err
		}
	}
	if start < 0 || start >= size || end < start {
		return 0, 0, fmt.Errorf("Range %q out of bounds for size %d", header, size)
	}
	return start, end, nil
}

func (f *fakeS3) gets() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.getCount
}

func (f *fakeS3) lastRange() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.ranges) == 0 {
		return ""
	}
	return f.ranges[len(f.ranges)-1]
}

// recordingUpdater captures the progress stream for assertions.
type recordingUpdater struct {
	mu       sync.Mutex
	stages   []string
	percents []float64
	bytes    []int64
	totals   []int64
}

func (r *recordingUpdater) UpdateProgress(stage string, percent float64, bytesProcessed, bytesTotal int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stages = append(r.stages, stage)
	r.percents = append(r.percents, percent)
	r.bytes = append(r.bytes, bytesProcessed)
	r.totals = append(r.totals, bytesTotal)
}

func (r *recordingUpdater) maxPercent() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	highest := 0.0
	for _, p := range r.percents {
		if p > highest {
			highest = p
		}
	}
	return highest
}

func (r *recordingUpdater) sawTotal(total int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range r.totals {
		if t == total {
			return true
		}
	}
	return false
}

func newTestClient(t *testing.T, endpoint string) *Client {
	t.Helper()
	c, err := NewClient(config.MinIOConfig{
		Endpoint:       endpoint,
		AccessKey:      "test-access",
		SecretKey:      "test-secret",
		RetryAttempts:  1,
		RetryBackoffMS: []int{1},
	})
	require.NoError(t, err)
	return c
}

func payload(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i % 251)
	}
	return b
}

const (
	testBucket = "images"
	testObject = "base/ubuntu-22.04.qcow2"
)

func imageURLFor(endpoint string) string {
	return endpoint + "/" + testBucket + "/" + testObject
}

// A fresh download writes the whole object.
func TestDownloadImageToPath_FreshDownload(t *testing.T) {
	body := payload(64 * 1024)
	f, endpoint := newFakeS3(t, body)
	c := newTestClient(t, endpoint)

	dest := filepath.Join(t.TempDir(), "cachekey")
	up := &recordingUpdater{}

	require.NoError(t, c.DownloadImageToPath(t.Context(), imageURLFor(endpoint), dest, up))

	got, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, body, got, "full object must be written")

	assert.Equal(t, 1, f.gets())
	assert.Empty(t, f.lastRange(), "a fresh download must not send a Range header")
	assert.Equal(t, float64(100), up.maxPercent())
	assert.True(t, up.sawTotal(int64(len(body))))
}

// The core of the resume change: a partial file is continued, not restarted.
// Only the missing bytes may cross the wire.
func TestDownloadImageToPath_ResumesFromPartialFile(t *testing.T) {
	body := payload(64 * 1024)
	f, endpoint := newFakeS3(t, body)
	c := newTestClient(t, endpoint)

	const prefix = 20 * 1024
	dest := filepath.Join(t.TempDir(), "cachekey")
	require.NoError(t, os.WriteFile(dest, body[:prefix], 0o644))

	up := &recordingUpdater{}
	require.NoError(t, c.DownloadImageToPath(t.Context(), imageURLFor(endpoint), dest, up))

	got, err := os.ReadFile(dest)
	require.NoError(t, err)
	require.Len(t, got, len(body), "resumed file must end up the full size")
	assert.Equal(t, body, got, "resumed bytes must match the object exactly")

	assert.Equal(t, 1, f.gets())
	assert.Equal(t, fmt.Sprintf("bytes=%d-%d", prefix, len(body)-1), f.lastRange(),
		"must request only the missing tail, not the whole object")

	// Progress must be reported against the full size, starting from the offset.
	assert.True(t, up.sawTotal(int64(len(body))))
	assert.Equal(t, float64(100), up.maxPercent())
}

// A file already the right size means the transfer is skipped entirely.
func TestDownloadImageToPath_SkipsWhenAlreadyComplete(t *testing.T) {
	body := payload(8 * 1024)
	f, endpoint := newFakeS3(t, body)
	c := newTestClient(t, endpoint)

	dest := filepath.Join(t.TempDir(), "cachekey")
	require.NoError(t, os.WriteFile(dest, body, 0o644))

	up := &recordingUpdater{}
	require.NoError(t, c.DownloadImageToPath(t.Context(), imageURLFor(endpoint), dest, up))

	assert.Equal(t, 0, f.gets(), "a complete file must not trigger a GET")

	got, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, body, got, "existing content must be left untouched")

	assert.Equal(t, float64(100), up.maxPercent())
}

// A file larger than the object is a stale copy, not a resumable prefix, and is
// discarded rather than appended to.
func TestDownloadImageToPath_DiscardsOversizeFile(t *testing.T) {
	body := payload(4 * 1024)
	f, endpoint := newFakeS3(t, body)
	c := newTestClient(t, endpoint)

	dest := filepath.Join(t.TempDir(), "cachekey")
	stale := append(payload(8*1024), []byte("trailing garbage from an older image")...)
	require.NoError(t, os.WriteFile(dest, stale, 0o644))

	require.NoError(t, c.DownloadImageToPath(t.Context(), imageURLFor(endpoint), dest, nil))

	got, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, body, got, "oversize file must be replaced, not appended to")

	assert.Equal(t, 1, f.gets())
	assert.Empty(t, f.lastRange(), "a stale file must be discarded, so no Range is sent")
}

// Resuming byte-by-byte must still produce a byte-exact object.
func TestDownloadImageToPath_ResumeAtEveryOffset(t *testing.T) {
	body := payload(2048)

	for prefix := 1; prefix < len(body); prefix++ {
		t.Run(fmt.Sprintf("offset_%d", prefix), func(t *testing.T) {
			f, endpoint := newFakeS3(t, body)
			c := newTestClient(t, endpoint)

			dest := filepath.Join(t.TempDir(), "cachekey")
			require.NoError(t, os.WriteFile(dest, body[:prefix], 0o644))

			require.NoError(t, c.DownloadImageToPath(t.Context(), imageURLFor(endpoint), dest, nil))

			got, err := os.ReadFile(dest)
			require.NoError(t, err)
			require.Equal(t, body, got, "resume from %d produced a corrupt file", prefix)
			assert.Equal(t, fmt.Sprintf("bytes=%d-%d", prefix, len(body)-1), f.lastRange())
		})
	}
}

func TestDownloadImageToPath_RejectsBadURLs(t *testing.T) {
	_, endpoint := newFakeS3(t, payload(1024))
	c := newTestClient(t, endpoint)
	dest := filepath.Join(t.TempDir(), "cachekey")

	cases := []struct {
		name    string
		url     string
		wantErr string
	}{
		{"no bucket or object", endpoint + "/", "invalid image URL path"},
		{"single path segment", endpoint + "/onlybucket", "invalid image URL path"},
		{"unparseable url", "://nope", "invalid image URL"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := c.DownloadImageToPath(t.Context(), tc.url, dest, nil)
			require.Error(t, err)
			// Wrapped by the retry helper and DownloadImageToPath both times.
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// destPath is built from a hex cache key upstream, but traversal must be refused
// here too rather than trusted.
func TestDownloadImageToPath_RejectsTraversalInDestPath(t *testing.T) {
	_, endpoint := newFakeS3(t, payload(1024))
	c := newTestClient(t, endpoint)

	// filepath.Join would clean the ".." away, so build the path by hand:
	// the guard is a literal substring check on destPath.
	dest := t.TempDir() + "/../escape"
	require.Contains(t, dest, "..")
	err := c.DownloadImageToPath(t.Context(), imageURLFor(endpoint), dest, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid destination path")
}

func TestDownloadImageToPath_MissingObjectFails(t *testing.T) {
	f, endpoint := newFakeS3(t, payload(1024))
	f.listCode = http.StatusNotFound
	c := newTestClient(t, endpoint)

	dest := filepath.Join(t.TempDir(), "cachekey")
	err := c.DownloadImageToPath(t.Context(), imageURLFor(endpoint), dest, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to stat object")
	assert.Equal(t, 0, f.gets(), "must not attempt a GET when the object is absent")
}

// A GET failure must surface as an error rather than a silently short file.
func TestDownloadImageToPath_GetFailureIsReported(t *testing.T) {
	f, endpoint := newFakeS3(t, payload(4096))
	f.getCode = http.StatusInternalServerError
	c := newTestClient(t, endpoint)

	dest := filepath.Join(t.TempDir(), "cachekey")
	err := c.DownloadImageToPath(t.Context(), imageURLFor(endpoint), dest, nil)
	require.Error(t, err)
}

// Nested object keys must be preserved: the destination is per-image, so a key
// with slashes has to survive the bucket/object split.
func TestDownloadImageToPath_NestedObjectKey(t *testing.T) {
	body := payload(1024)
	f, endpoint := newFakeS3(t, body)
	c := newTestClient(t, endpoint)

	dest := filepath.Join(t.TempDir(), "cachekey")
	url := endpoint + "/" + testBucket + "/" + testObject
	require.NoError(t, c.DownloadImageToPath(t.Context(), url, dest, nil))

	got, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, body, got)
	assert.Equal(t, 1, f.gets())
}
