package logging

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/trace"
)

// Config holds logging configuration
type Config struct {
	Level        string `json:"level"         yaml:"level"`
	Format       string `json:"format"        yaml:"format"`        // "json" or "text"
	SamplingRate int    `json:"sampling_rate" yaml:"sampling_rate"` // 0 = no sampling, 100 = sample every 100th log
	Output       string `json:"output"        yaml:"output"`        // "stdout", "stderr", or file path
}

// DefaultConfig returns sensible defaults
func DefaultConfig() Config {
	return Config{
		Level:        "info",
		Format:       "json",
		SamplingRate: 0, // No sampling
		Output:       "stdout",
	}
}

// Logger wraps logrus.Logger with additional features
type Logger struct {
	*logrus.Logger
	sampler *LogSampler
}

// NewLogger creates a configured logger
func NewLogger(config Config) (*Logger, error) {
	logger := logrus.New()

	// Configure output
	switch strings.ToLower(config.Output) {
	case "stderr":
		logger.SetOutput(os.Stderr)
	case "stdout":
		logger.SetOutput(os.Stdout)
	default:
		// Assume it's a file path
		file, err := os.OpenFile(config.Output, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			return nil, fmt.Errorf("failed to open log file %q: %w", config.Output, err)
		}
		logger.SetOutput(file)
	}

	// Configure format
	switch strings.ToLower(config.Format) {
	case "json":
		logger.SetFormatter(&logrus.JSONFormatter{
			TimestampFormat: time.RFC3339,
		})
	case "text":
		logger.SetFormatter(&logrus.TextFormatter{
			TimestampFormat: time.RFC3339,
			FullTimestamp:   true,
		})
	default:
		logger.SetFormatter(&logrus.JSONFormatter{
			TimestampFormat: time.RFC3339,
		})
	}

	// Configure level
	level, err := logrus.ParseLevel(config.Level)
	if err != nil {
		level = logrus.InfoLevel
	}
	logger.SetLevel(level)

	// Add trace correlation hook
	logger.AddHook(&TraceCorrelationHook{})

	var sampler *LogSampler
	if config.SamplingRate > 1 {
		sampler = NewLogSampler(config.SamplingRate)
	}

	return &Logger{
		Logger:  logger,
		sampler: sampler,
	}, nil
}

// WithContext adds context to log entry
func (l *Logger) WithContext(ctx context.Context) *logrus.Entry {
	entry := l.Logger.WithContext(ctx)
	if l.sampler != nil && !l.sampler.ShouldLog() {
		// Return a no-op entry for sampled logs
		return &logrus.Entry{Logger: &logrus.Logger{Out: io.Discard}}
	}
	return entry
}

// WithError adds error to log entry
func (l *Logger) WithError(err error) *logrus.Entry {
	entry := l.Logger.WithError(err)
	if l.sampler != nil && !l.sampler.ShouldLog() {
		return &logrus.Entry{Logger: &logrus.Logger{Out: io.Discard}}
	}
	return entry
}

// WithField adds field to log entry
func (l *Logger) WithField(key string, value interface{}) *logrus.Entry {
	entry := l.Logger.WithField(key, value)
	if l.sampler != nil && !l.sampler.ShouldLog() {
		return &logrus.Entry{Logger: &logrus.Logger{Out: io.Discard}}
	}
	return entry
}

// WithFields adds multiple fields to log entry
func (l *Logger) WithFields(fields logrus.Fields) *logrus.Entry {
	entry := l.Logger.WithFields(fields)
	if l.sampler != nil && !l.sampler.ShouldLog() {
		return &logrus.Entry{Logger: &logrus.Logger{Out: io.Discard}}
	}
	return entry
}

// TraceCorrelationHook injects trace_id and span_id into logrus entries
type TraceCorrelationHook struct{}

func (h *TraceCorrelationHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (h *TraceCorrelationHook) Fire(entry *logrus.Entry) error {
	if entry == nil {
		return nil
	}

	span := trace.SpanFromContext(entry.Context)
	if span != nil && span.IsRecording() {
		spanContext := span.SpanContext()
		if spanContext.IsValid() {
			entry.Data["trace_id"] = spanContext.TraceID().String()
			entry.Data["span_id"] = spanContext.SpanID().String()
		}
	}
	return nil
}

// LogSampler implements log sampling to reduce verbosity
type LogSampler struct {
	rate     int
	counter  int
	lastTime time.Time
}

func NewLogSampler(rate int) *LogSampler {
	return &LogSampler{
		rate:     rate,
		counter:  0,
		lastTime: time.Now(),
	}
}

func (s *LogSampler) ShouldLog() bool {
	s.counter++
	if s.counter >= s.rate {
		s.counter = 0
		return true
	}
	return false
}

// RequestLogger middleware for Gin
func (l *Logger) RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery

		// Process request
		c.Next()

		// Log request details
		end := time.Now()
		latency := end.Sub(start)

		if raw != "" {
			path = path + "?" + raw
		}

		entry := l.WithContext(c.Request.Context()).WithFields(logrus.Fields{
			"method":     c.Request.Method,
			"path":       path,
			"status":     c.Writer.Status(),
			"latency":    latency.String(),
			"client_ip":  c.ClientIP(),
			"user_agent": c.Request.UserAgent(),
			"request_id": c.GetString("request_id"),
		})

		if len(c.Errors) > 0 {
			entry.Error(c.Errors.ByType(gin.ErrorTypePrivate).String())
		} else {
			entry.Info("HTTP request")
		}
	}
}
