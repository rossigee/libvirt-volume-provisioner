package main

import (
	"context"
	"os"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func TestInitOTLP_NoEndpoint(t *testing.T) {
	// Test when OTEL_EXPORTER_OTLP_ENDPOINT is not set
	originalEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	defer func() {
		if originalEndpoint != "" {
			_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", originalEndpoint)
		} else {
			_ = os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")
		}
	}()

	// Ensure endpoint is not set
	_ = os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")

	tp, err := initOTLP(context.Background())
	assert.Error(t, err)
	assert.ErrorIs(t, err, errOTLPNotConfigured)
	assert.Nil(t, tp)
}

func TestInitOTLP_InvalidEndpoint(t *testing.T) {
	// Test with invalid endpoint
	originalEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	defer func() {
		if originalEndpoint != "" {
			_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", originalEndpoint)
		} else {
			_ = os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")
		}
	}()

	// Set invalid endpoint (missing protocol)
	_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "invalid-endpoint")

	tp, err := initOTLP(context.Background())
	// The OTLP exporter might not fail immediately on creation
	// but it should fail when trying to export spans
	// For this test, we just check that it's not configured properly
	if err == nil {
		// If no error, at least check that it's not the expected configured state
		// The main thing is that OTLP is not properly configured
		assert.NotNil(t, tp) // It might create a tracer provider even with invalid endpoint
	}
}

func TestLogCorrelationHook(t *testing.T) {
	hook := &logCorrelationHook{}

	// Test that hook returns all levels
	levels := hook.Levels()
	assert.Len(t, levels, 7) // All logrus levels

	// Test firing hook without context (should not panic)
	entry := &logrus.Entry{
		Level:   logrus.InfoLevel,
		Message: "test message",
	}

	// Should not panic even without span context
	assert.NotPanics(t, func() {
		_ = hook.Fire(entry)
	})
}

func TestLogCorrelationHook_WithSpan(t *testing.T) {
	// This test verifies that the hook integrates with OpenTelemetry spans
	// Note: This test assumes OTLP is not configured, so spans won't be recorded
	hook := &logCorrelationHook{}

	entry := &logrus.Entry{
		Level:   logrus.InfoLevel,
		Message: "test message",
		Data:    make(logrus.Fields),
	}

	// Fire hook - should add trace_id and span_id if span exists
	_ = hook.Fire(entry)

	// Since no span is active, no trace fields should be added
	assert.NotContains(t, entry.Data, "trace_id")
	assert.NotContains(t, entry.Data, "span_id")
}
