package main

import (
	"context"
	"testing"

	"github.com/rossigee/libvirt-volume-provisioner/internal/config"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitTracing_NoEndpoint(t *testing.T) {
	// Tracing is disabled when endpoint is empty
	tp, err := initTracing(context.Background(), config.TracingConfig{})
	assert.Error(t, err)
	assert.ErrorIs(t, err, errOTLPNotConfigured)
	assert.Nil(t, tp)
}

func TestInitTracing_InvalidConfig(t *testing.T) {
	// Should fail with no valid exporters
	tp, err := initTracing(context.Background(), config.TracingConfig{
		Endpoint:  "http://localhost:4317",
		Exporters: []string{"invalid-exporter"},
	})
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
		require.NoError(t, hook.Fire(entry))
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
	require.NoError(t, hook.Fire(entry))

	// Since no span is active, no trace fields should be added
	assert.NotContains(t, entry.Data, "trace_id")
	assert.NotContains(t, entry.Data, "span_id")
}
