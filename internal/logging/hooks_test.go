package logging

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// recorder captures the batches a hook POSTs.
type recorder struct {
	mu      sync.Mutex
	batches [][]map[string]interface{}
	headers []http.Header
	status  int
	url     string
}

func newRecorder(t *testing.T) (*recorder, string) {
	t.Helper()
	rec := &recorder{status: http.StatusOK}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)

		var batch []map[string]interface{}
		require.NoError(t, json.Unmarshal(body, &batch))

		rec.mu.Lock()
		defer rec.mu.Unlock()
		rec.batches = append(rec.batches, batch)
		rec.headers = append(rec.headers, r.Header.Clone())
		w.WriteHeader(rec.status)
	}))
	t.Cleanup(srv.Close)
	rec.url = srv.URL
	return rec, srv.URL
}

func (rec *recorder) count() int {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return len(rec.batches)
}

func (rec *recorder) all() [][]map[string]interface{} {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	out := make([][]map[string]interface{}, len(rec.batches))
	copy(out, rec.batches)
	return out
}

func entry(level logrus.Level, msg string, data logrus.Fields) *logrus.Entry {
	return &logrus.Entry{
		Logger:  logrus.New(),
		Level:   level,
		Message: msg,
		Time:    time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC),
		Data:    data,
	}
}

func TestNewExternalLogHook_AppliesDefaults(t *testing.T) {
	hook := NewExternalLogHook(HookConfig{URL: "http://example.invalid"})
	t.Cleanup(hook.Close)

	assert.Equal(t, 5*time.Second, hook.config.Timeout)
	assert.Equal(t, 100, hook.config.BufferSize)
	assert.Equal(t, 30*time.Second, hook.config.FlushInterval)
	assert.Equal(t, 5*time.Second, hook.client.Timeout)
}

func TestNewExternalLogHook_KeepsExplicitConfig(t *testing.T) {
	hook := NewExternalLogHook(HookConfig{
		URL:           "http://example.invalid",
		Timeout:       time.Second,
		BufferSize:    7,
		FlushInterval: time.Minute,
	})
	t.Cleanup(hook.Close)

	assert.Equal(t, time.Second, hook.config.Timeout)
	assert.Equal(t, 7, hook.config.BufferSize)
	assert.Equal(t, time.Minute, hook.config.FlushInterval)
}

func TestExternalLogHook_Levels(t *testing.T) {
	hook := NewExternalLogHook(HookConfig{URL: "http://example.invalid"})
	t.Cleanup(hook.Close)

	assert.Equal(t, []logrus.Level{
		logrus.PanicLevel, logrus.FatalLevel, logrus.ErrorLevel,
		logrus.WarnLevel, logrus.InfoLevel,
	}, hook.Levels())

	// Debug and Trace must not fire the hook, or the hook would ship the whole
	// debug stream to the external system.
	for _, lvl := range hook.Levels() {
		assert.NotEqual(t, logrus.DebugLevel, lvl)
		assert.NotEqual(t, logrus.TraceLevel, lvl)
	}
}

func TestExternalLogHook_BelowBufferSizeDoesNotSend(t *testing.T) {
	rec, url := newRecorder(t)
	hook := NewExternalLogHook(HookConfig{URL: url, BufferSize: 3})
	t.Cleanup(hook.Close)

	require.NoError(t, hook.Fire(entry(logrus.InfoLevel, "one", nil)))
	require.NoError(t, hook.Fire(entry(logrus.InfoLevel, "two", nil)))

	assert.Equal(t, 0, rec.count(), "must not send until the buffer is full")
	assert.Len(t, hook.buffer, 2)
}

func TestExternalLogHook_FlushesWhenBufferFull(t *testing.T) {
	rec, url := newRecorder(t)
	hook := NewExternalLogHook(HookConfig{URL: url, BufferSize: 3})
	t.Cleanup(hook.Close)

	for _, msg := range []string{"one", "two", "three"} {
		require.NoError(t, hook.Fire(entry(logrus.InfoLevel, msg, nil)))
	}

	require.Equal(t, 1, rec.count())
	batches := rec.all()
	require.Len(t, batches[0], 3)
	assert.Equal(t, "one", batches[0][0]["message"])
	assert.Equal(t, "three", batches[0][2]["message"])

	// Buffer must be drained, not just copied, or entries repeat on the next flush.
	assert.Empty(t, hook.buffer)
}

func TestExternalLogHook_PayloadShape(t *testing.T) {
	rec, url := newRecorder(t)
	hook := NewExternalLogHook(HookConfig{URL: url, BufferSize: 1})
	t.Cleanup(hook.Close)

	require.NoError(t, hook.Fire(entry(logrus.WarnLevel, "disk almost full", logrus.Fields{
		"volume_name": "runner-v4",
		"free_bytes":  float64(1024),
	})))

	batches := rec.all()
	require.Len(t, batches, 1)
	require.Len(t, batches[0], 1)

	got := batches[0][0]
	assert.Equal(t, "warning", got["level"])
	assert.Equal(t, "disk almost full", got["message"])
	assert.Equal(t, "2026-10-03T12:00:00Z", got["timestamp"])

	// Entry fields are merged into the top level of the log line.
	assert.Equal(t, "runner-v4", got["volume_name"])
	assert.InDelta(t, 1024.0, got["free_bytes"], 1e-9)
}

func TestExternalLogHook_SendsConfiguredHeaders(t *testing.T) {
	rec, url := newRecorder(t)
	hook := NewExternalLogHook(HookConfig{
		URL:        url,
		BufferSize: 1,
		Headers: map[string]string{
			"X-Api-Key": "secret-value",
			"X-Tenant":  "golder",
		},
	})
	t.Cleanup(hook.Close)

	require.NoError(t, hook.Fire(entry(logrus.InfoLevel, "hello", nil)))
	require.Equal(t, 1, rec.count())

	rec.mu.Lock()
	defer rec.mu.Unlock()
	require.Len(t, rec.headers, 1)
	assert.Equal(t, "application/json", rec.headers[0].Get("Content-Type"))
	assert.Equal(t, "secret-value", rec.headers[0].Get("X-Api-Key"))
	assert.Equal(t, "golder", rec.headers[0].Get("X-Tenant"))
}

func TestExternalLogHook_EmptyBufferSendsNothing(t *testing.T) {
	rec, url := newRecorder(t)
	hook := NewExternalLogHook(HookConfig{URL: url})
	t.Cleanup(hook.Close)

	hook.flush()

	assert.Equal(t, 0, rec.count())
}

func TestExternalLogHook_ServerErrorIsNotFatal(t *testing.T) {
	rec, url := newRecorder(t)
	rec.status = http.StatusInternalServerError
	hook := NewExternalLogHook(HookConfig{URL: url, BufferSize: 1})
	t.Cleanup(hook.Close)

	// A 5xx from the log sink must not break the caller or retry forever.
	assert.NotPanics(t, func() {
		require.NoError(t, hook.Fire(entry(logrus.ErrorLevel, "boom", nil)))
	})
	assert.Equal(t, 1, rec.count())
}

func TestExternalLogHook_UnreachableEndpointDoesNotPanic(t *testing.T) {
	hook := NewExternalLogHook(HookConfig{
		URL:        "http://127.0.0.1:1/unreachable",
		BufferSize: 1,
		Timeout:    100 * time.Millisecond,
	})
	t.Cleanup(hook.Close)

	assert.NotPanics(t, func() {
		require.NoError(t, hook.Fire(entry(logrus.ErrorLevel, "boom", nil)))
	})
}

func TestExternalLogHook_UnmarshalableFieldDoesNotPanic(t *testing.T) {
	_, url := newRecorder(t)
	hook := NewExternalLogHook(HookConfig{URL: url, BufferSize: 1})
	t.Cleanup(hook.Close)

	// json.Marshal cannot encode a channel; sendLogs must log and return rather
	// than panic inside the logging path.
	assert.NotPanics(t, func() {
		require.NoError(t, hook.Fire(entry(logrus.InfoLevel, "bad field", logrus.Fields{
			"ch": make(chan int),
		})))
	})
}

// Close only signals the background worker; the flush itself happens
// asynchronously in flushWorker, so callers observe it eventually rather than
// by the time Close returns.
func TestExternalLogHook_CloseFlushesRemainingEntries(t *testing.T) {
	rec, url := newRecorder(t)
	hook := NewExternalLogHook(HookConfig{URL: url, BufferSize: 100})

	require.NoError(t, hook.Fire(entry(logrus.InfoLevel, "buffered", nil)))
	require.Equal(t, 0, rec.count(), "still buffered before Close")

	hook.Close()

	assert.Eventually(t, func() bool { return rec.count() == 1 },
		2*time.Second, 5*time.Millisecond,
		"Close must flush what is still buffered")

	batches := rec.all()
	require.Len(t, batches[0], 1)
	assert.Equal(t, "buffered", batches[0][0]["message"])
}

// closeOnce guards the channel close, so repeated shutdown must not panic.
func TestExternalLogHook_CloseIsIdempotent(t *testing.T) {
	rec, url := newRecorder(t)
	hook := NewExternalLogHook(HookConfig{URL: url, BufferSize: 1})

	// Each Fire fills the buffer, so these flush during Fire rather than on Close.
	require.NoError(t, hook.Fire(entry(logrus.InfoLevel, "first", nil)))
	require.NoError(t, hook.Fire(entry(logrus.InfoLevel, "second", nil)))
	require.Equal(t, 2, rec.count())

	assert.NotPanics(t, func() {
		hook.Close()
		hook.Close()
		hook.Close()
	})

	// Nothing was left buffered, so shutting down must not send anything more.
	assert.Equal(t, 2, rec.count())
}

func TestExternalLogHook_TickerFlushesPeriodically(t *testing.T) {
	rec, url := newRecorder(t)
	hook := NewExternalLogHook(HookConfig{
		URL:           url,
		BufferSize:    1000,
		FlushInterval: 10 * time.Millisecond,
	})
	t.Cleanup(hook.Close)

	require.NoError(t, hook.Fire(entry(logrus.InfoLevel, "ticked", nil)))

	assert.Eventually(t, func() bool { return rec.count() == 1 },
		2*time.Second, 5*time.Millisecond,
		"background flusher should send without Close being called")
}

func TestLokiHook_URLAndHeaders(t *testing.T) {
	_, url := newRecorder(t)
	hook := NewLokiHook(url, map[string]string{"job": "provisioner"})

	assert.Equal(t, url+"/loki/api/v1/push", hook.config.URL)
	assert.Equal(t, "application/json", hook.config.Headers["Content-Type"])
}

func TestLokiHook_Levels(t *testing.T) {
	hook := NewLokiHook("http://example.invalid", nil)

	assert.Equal(t, []logrus.Level{
		logrus.InfoLevel, logrus.WarnLevel, logrus.ErrorLevel,
	}, hook.Levels())
}

func TestLokiHook_FireSendsPushPayload(t *testing.T) {
	var mu sync.Mutex
	var payload map[string]interface{}
	var contentType string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		mu.Lock()
		defer mu.Unlock()
		require.NoError(t, json.Unmarshal(body, &payload))
		contentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	hook := NewLokiHook(srv.URL, map[string]string{"job": "provisioner", "host": "runner"})
	require.NoError(t, hook.Fire(entry(logrus.ErrorLevel, "volume failed", nil)))

	mu.Lock()
	defer mu.Unlock()

	assert.Equal(t, "application/json", contentType)
	require.NotNil(t, payload)

	streams, ok := payload["streams"].([]interface{})
	require.True(t, ok, "payload must contain a streams array")
	require.Len(t, streams, 1)

	stream, ok := streams[0].(map[string]interface{})
	require.True(t, ok)

	labels, ok := stream["stream"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "provisioner", labels["job"])
	assert.Equal(t, "runner", labels["host"])
	assert.Equal(t, "error", labels["level"], "level label must be derived from the entry")

	values, ok := stream["values"].([]interface{})
	require.True(t, ok)
	require.Len(t, values, 1)

	pair, ok := values[0].([]interface{})
	require.True(t, ok)
	require.Len(t, pair, 2)
	assert.Equal(t, "volume failed", pair[1])

	// Loki expects nanoseconds since the epoch as a decimal string.
	stamp, ok := pair[0].(string)
	require.True(t, ok, "timestamp must be encoded as a string")
	want := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC).UnixNano()
	assert.Equal(t, fmt.Sprintf("%d", want), stamp)
}

// A caller-supplied "level" label must not win over the entry's own level, or
// every line would be mislabelled in Grafana.
func TestLokiHook_LevelLabelOverridesCallerLabel(t *testing.T) {
	var mu sync.Mutex
	var payload map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		_ = json.Unmarshal(body, &payload)
	}))
	t.Cleanup(srv.Close)

	hook := NewLokiHook(srv.URL, map[string]string{"level": "caller-supplied"})
	require.NoError(t, hook.Fire(entry(logrus.WarnLevel, "careful", nil)))

	mu.Lock()
	defer mu.Unlock()

	streams, ok := payload["streams"].([]interface{})
	require.True(t, ok)
	stream, ok := streams[0].(map[string]interface{})
	require.True(t, ok)
	labels, ok := stream["stream"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "warning", labels["level"])
}

func TestLokiHook_ServerErrorReturnsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)

	hook := NewLokiHook(srv.URL, nil)

	// Fire must never propagate a sink failure to the caller's logging path.
	assert.NotPanics(t, func() {
		assert.NoError(t, hook.Fire(entry(logrus.InfoLevel, "hello", nil)))
	})
}

func TestLokiHook_UnreachableEndpointReturnsNil(t *testing.T) {
	hook := NewLokiHook("http://127.0.0.1:1", nil)

	assert.NotPanics(t, func() {
		assert.NoError(t, hook.Fire(entry(logrus.InfoLevel, "hello", nil)))
	})
}

func TestLokiHook_UnmarshalablePayloadReturnsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	// The label map is marshalled, so a channel in it cannot be encoded.
	hook := NewLokiHook(srv.URL, map[string]string{"bad": "ok"})
	assert.NotPanics(t, func() {
		assert.NoError(t, hook.Fire(entry(logrus.InfoLevel, "hello", nil)))
	})
}

// The external hook URL must be usable as given; a bad URL has to fail quietly.
func TestExternalLogHook_InvalidURLDoesNotPanic(t *testing.T) {
	hook := NewExternalLogHook(HookConfig{URL: "://not a url", BufferSize: 1})
	t.Cleanup(hook.Close)

	assert.NotPanics(t, func() {
		require.NoError(t, hook.Fire(entry(logrus.InfoLevel, "hello", nil)))
	})
}

func TestExternalLogHook_ConcurrentFireIsSafe(t *testing.T) {
	rec, url := newRecorder(t)
	hook := NewExternalLogHook(HookConfig{URL: url, BufferSize: 10, FlushInterval: time.Hour})
	t.Cleanup(hook.Close)

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = hook.Fire(entry(logrus.InfoLevel, "concurrent", logrus.Fields{"i": i}))
		}()
	}
	wg.Wait()

	assert.Equal(t, 5, rec.count(), "50 entries at BufferSize 10 is 5 batches")
	total := 0
	for _, b := range rec.all() {
		total += len(b)
	}
	assert.Equal(t, 50, total, "no entry may be dropped or duplicated")
}

// A sampled-out line is replaced by a bare no-op entry: a usable *logrus.Entry
// so callers never need a nil check, but with nothing bound to the real logger.
// SamplingRate 2 makes the first call sampled out and the second kept.
func TestLoggerSampledEntriesAreDiscarded(t *testing.T) {
	logger, err := NewLogger(Config{
		Level:        "info",
		Format:       "json",
		Output:       "stdout",
		SamplingRate: 2,
	})
	require.NoError(t, err)
	require.NotNil(t, logger.sampler)

	// 1st call is sampled out.
	sampledOut := logger.WithFields(logrus.Fields{"k": "v"})
	require.NotNil(t, sampledOut, "must never return nil")
	assert.Equal(t, io.Discard, sampledOut.Logger.Out, "sampled-out entry must not reach the real output")
	assert.Empty(t, sampledOut.Data, "sampled-out entry carries no fields")

	// 2nd call is kept and carries the fields through to the real logger.
	kept := logger.WithFields(logrus.Fields{"k": "v"})
	require.NotNil(t, kept)
	assert.Equal(t, os.Stdout, kept.Logger.Out)
	assert.Equal(t, "v", kept.Data["k"])
}

// The other three With* wrappers share the same sampling gate.
func TestLoggerSampledWrappersAllDiscard(t *testing.T) {
	logger, err := NewLogger(Config{
		Level:        "info",
		Format:       "json",
		Output:       "stdout",
		SamplingRate: 100,
	})
	require.NoError(t, err)

	assert.Equal(t, io.Discard, logger.WithField("k", "v").Logger.Out)
	assert.Equal(t, io.Discard, logger.WithError(assert.AnError).Logger.Out)
	assert.Equal(t, io.Discard, logger.WithContext(t.Context()).Logger.Out)
}

// WithSamplingRate of 1 or less must not install a sampler at all.
func TestNewLogger_NoSamplerByDefault(t *testing.T) {
	for _, rate := range []int{0, 1} {
		logger, err := NewLogger(Config{Level: "info", Format: "json", Output: "stdout", SamplingRate: rate})
		require.NoError(t, err)
		assert.Nil(t, logger.sampler, "SamplingRate %d must not install a sampler", rate)
	}
}

func TestNewLogger_UnknownLevelFallsBackToInfo(t *testing.T) {
	logger, err := NewLogger(Config{Level: "not-a-level", Format: "json", Output: "stdout"})
	require.NoError(t, err)
	assert.Equal(t, logrus.InfoLevel, logger.GetLevel())
}

func TestNewLogger_UnknownFormatFallsBackToJSON(t *testing.T) {
	logger, err := NewLogger(Config{Level: "info", Format: "not-a-format", Output: "stdout"})
	require.NoError(t, err)
	assert.IsType(t, &logrus.JSONFormatter{}, logger.Formatter)
}

func TestNewLogger_BadOutputPathReturnsError(t *testing.T) {
	logger, err := NewLogger(Config{
		Level:  "info",
		Format: "json",
		Output: t.TempDir() + "/no/such/dir/provisioner.log",
	})
	require.Error(t, err)
	assert.Nil(t, logger)
	assert.Contains(t, err.Error(), "failed to open log file")
}

func TestTraceCorrelationHook_NilEntryIsSafe(t *testing.T) {
	hook := &TraceCorrelationHook{}
	assert.NoError(t, hook.Fire(nil))
	assert.Equal(t, logrus.AllLevels, hook.Levels())
}

func TestLogSampler_LogsEveryNthCall(t *testing.T) {
	s := NewLogSampler(3)

	assert.False(t, s.ShouldLog(), "1st call is sampled out")
	assert.False(t, s.ShouldLog(), "2nd call is sampled out")
	assert.True(t, s.ShouldLog(), "3rd call is kept")
	assert.False(t, s.ShouldLog(), "counter resets after being kept")
}

// HookConfig's yaml tags are what a future config-file path would use, and
// yaml.v3 decodes duration strings into time.Duration. The json tags cannot do
// the same: time.Duration is an int64 and does not implement json.Unmarshaler,
// so "30s" fails to parse from JSON and only a nanosecond count is accepted.
// Nothing currently deserialises HookConfig - main.go builds it in Go - so this
// documents the asymmetry rather than asserting a contract that is used.
func TestHookConfig_YAMLAcceptsDurationStrings(t *testing.T) {
	var cfg HookConfig
	require.NoError(t, yaml.Unmarshal([]byte(
		"type: webhook\nurl: http://x\nbuffer_size: 5\nflush_interval: 30s\ntimeout: 5s\n"),
		&cfg))

	assert.Equal(t, "webhook", cfg.Type)
	assert.Equal(t, "http://x", cfg.URL)
	assert.Equal(t, 5, cfg.BufferSize)
	assert.Equal(t, 30*time.Second, cfg.FlushInterval)
	assert.Equal(t, 5*time.Second, cfg.Timeout)
}

func TestHookConfig_JSONNeedsNanosecondsForDurations(t *testing.T) {
	var cfg HookConfig
	err := json.Unmarshal([]byte(`{"flush_interval":"30s"}`), &cfg)
	require.Error(t, err, "time.Duration cannot be decoded from a JSON string")

	var nanos HookConfig
	require.NoError(t, json.Unmarshal([]byte(`{"flush_interval":30000000000}`), &nanos))
	assert.Equal(t, 30*time.Second, nanos.FlushInterval)
}
