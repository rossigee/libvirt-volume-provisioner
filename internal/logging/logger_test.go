package logging

import (
	"bytes"
	"os"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewLogger_DefaultConfig(t *testing.T) {
	logger, err := NewLogger(DefaultConfig())
	require.NoError(t, err)
	assert.NotNil(t, logger)

	// Test basic logging
	var buf bytes.Buffer
	logger.SetOutput(&buf)

	logger.Info("test message")
	output := buf.String()
	assert.Contains(t, output, "test message")
	assert.Contains(t, output, `"level":"info"`)
}

func TestNewLogger_JSONFormat(t *testing.T) {
	config := Config{
		Level:        "info",
		Format:       "json",
		Output:       "stdout",
		SamplingRate: 0,
	}

	logger, err := NewLogger(config)
	require.NoError(t, err)

	var buf bytes.Buffer
	logger.SetOutput(&buf)

	entry := logger.WithField("test_key", "test_value")
	entry.Info("test message")

	output := buf.String()
	assert.Contains(t, output, "test message")
	assert.Contains(t, output, "test_key")
	assert.Contains(t, output, "test_value")
	assert.Contains(t, output, `"level":"info"`)
}

func TestNewLogger_TextFormat(t *testing.T) {
	config := Config{
		Level:        "info",
		Format:       "text",
		Output:       "stdout",
		SamplingRate: 0,
	}

	logger, err := NewLogger(config)
	require.NoError(t, err)

	var buf bytes.Buffer
	logger.SetOutput(&buf)

	logger.Info("test message")
	output := buf.String()
	assert.Contains(t, output, "test message")
	assert.Contains(t, output, "info")
	assert.NotContains(t, output, `"level":"info"`)
}

func TestNewLogger_LogSampling(t *testing.T) {
	config := Config{
		Level:        "info",
		Format:       "json",
		Output:       "stdout",
		SamplingRate: 2, // Sample every 2nd log
	}

	logger, err := NewLogger(config)
	require.NoError(t, err)

	var buf bytes.Buffer
	logger.SetOutput(&buf)

	// Log multiple messages - should not panic
	assert.NotPanics(t, func() {
		for i := 0; i < 10; i++ {
			logger.Info("test message")
		}
	})

	// With sampling enabled, some logs may be discarded
	// We just verify the logger doesn't crash
	output := buf.String()
	assert.Contains(t, output, "test message")
}

func TestNewLogger_FileOutput(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test_log_*.log")
	require.NoError(t, err)
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	config := Config{
		Level:        "info",
		Format:       "json",
		Output:       tmpFile.Name(),
		SamplingRate: 0,
	}

	logger, err := NewLogger(config)
	require.NoError(t, err)

	logger.Info("test message to file")

	content, err := os.ReadFile(tmpFile.Name())
	_ = os.Remove(tmpFile.Name()) // cleanup
	require.NoError(t, err)

	output := string(content)
	assert.Contains(t, output, "test message to file")
	assert.Contains(t, output, `"level":"info"`)
}

func TestLogSampler(t *testing.T) {
	sampler := NewLogSampler(3)

	// First 2 calls should return false (sampling)
	assert.False(t, sampler.ShouldLog())
	assert.False(t, sampler.ShouldLog())

	// 3rd call should return true
	assert.True(t, sampler.ShouldLog())

	// Next 2 should be false again
	assert.False(t, sampler.ShouldLog())
	assert.False(t, sampler.ShouldLog())

	// 6th call should return true
	assert.True(t, sampler.ShouldLog())
}

func TestTraceCorrelationHook(t *testing.T) {
	hook := &TraceCorrelationHook{}

	// Should return all levels
	levels := hook.Levels()
	assert.Len(t, levels, 7)

	// Should handle nil entry gracefully
	assert.NotPanics(t, func() {
		_ = hook.Fire(nil)
	})

	// Should handle entry without context
	entry := &logrus.Entry{
		Data: make(logrus.Fields),
	}
	assert.NotPanics(t, func() {
		_ = hook.Fire(entry)
	})

	// Should handle entry with nil context
	entryWithNilContext := &logrus.Entry{
		Data:    make(logrus.Fields),
		Context: nil,
	}
	assert.NotPanics(t, func() {
		_ = hook.Fire(entryWithNilContext)
	})
}

// Note: ExternalLogHook integration tests would require HTTP server setup
// Unit tests for individual hook components are tested above
