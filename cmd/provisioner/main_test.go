package main

import (
	"context"
	"os"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func TestInitTracing_NoEndpoint(t *testing.T) {
	// Test when tracing is not configured
	originalEnabled := os.Getenv("TRACING_ENABLED")
	originalEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	defer func() {
		if originalEnabled != "" {
			_ = os.Setenv("TRACING_ENABLED", originalEnabled)
		} else {
			_ = os.Unsetenv("TRACING_ENABLED")
		}
		if originalEndpoint != "" {
			_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", originalEndpoint)
		} else {
			_ = os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")
		}
	}()

	// Ensure tracing is not enabled
	_ = os.Unsetenv("TRACING_ENABLED")
	_ = os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")

	tp, err := initTracing(context.Background())
	assert.Error(t, err)
	assert.ErrorIs(t, err, errOTLPNotConfigured)
	assert.Nil(t, tp)
}

func TestInitTracing_InvalidConfig(t *testing.T) {
	// Test with invalid configuration
	originalEnabled := os.Getenv("TRACING_ENABLED")
	originalEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	defer func() {
		if originalEnabled != "" {
			_ = os.Setenv("TRACING_ENABLED", originalEnabled)
		} else {
			_ = os.Unsetenv("TRACING_ENABLED")
		}
		if originalEndpoint != "" {
			_ = os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", originalEndpoint)
		} else {
			_ = os.Unsetenv("OTEL_EXPORTER_OTLP_ENDPOINT")
		}
	}()

	// Set invalid configuration
	_ = os.Setenv("TRACING_ENABLED", "true")
	_ = os.Setenv("TRACING_EXPORTERS", "invalid-exporter")

	tp, err := initTracing(context.Background())
	// Should fail with no valid exporters
	assert.Error(t, err)
	assert.Nil(t, tp)
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
		hook.Fire(entry)
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
	hook.Fire(entry)

	// Since no span is active, no trace fields should be added
	assert.NotContains(t, entry.Data, "trace_id")
	assert.NotContains(t, entry.Data, "span_id")
}
