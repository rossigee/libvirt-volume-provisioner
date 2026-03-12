package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// HookConfig holds configuration for external log hooks
type HookConfig struct {
	Type          string            `json:"type"           yaml:"type"`           // "webhook", "elasticsearch", etc.
	URL           string            `json:"url"            yaml:"url"`            // Endpoint URL
	Headers       map[string]string `json:"headers"        yaml:"headers"`        // HTTP headers
	Timeout       time.Duration     `json:"timeout"        yaml:"timeout"`        // Request timeout
	BufferSize    int               `json:"buffer_size"    yaml:"buffer_size"`    // Batch size
	FlushInterval time.Duration     `json:"flush_interval" yaml:"flush_interval"` // Flush interval
}

// ExternalLogHook sends logs to external systems
type ExternalLogHook struct {
	config    HookConfig
	client    *http.Client
	buffer    []*logrus.Entry
	bufferMux sync.Mutex
	ticker    *time.Ticker
	stopChan  chan struct{}
}

// NewExternalLogHook creates a new external log hook
func NewExternalLogHook(config HookConfig) *ExternalLogHook {
	if config.Timeout == 0 {
		config.Timeout = 5 * time.Second
	}
	if config.BufferSize == 0 {
		config.BufferSize = 100
	}
	if config.FlushInterval == 0 {
		config.FlushInterval = 30 * time.Second
	}

	hook := &ExternalLogHook{
		config: config,
		client: &http.Client{
			Timeout: config.Timeout,
		},
		buffer:   make([]*logrus.Entry, 0, config.BufferSize),
		stopChan: make(chan struct{}),
	}

	// Start background flusher
	hook.ticker = time.NewTicker(config.FlushInterval)
	go hook.flushWorker()

	return hook
}

// Levels returns the log levels this hook should be fired for
func (h *ExternalLogHook) Levels() []logrus.Level {
	return []logrus.Level{
		logrus.PanicLevel,
		logrus.FatalLevel,
		logrus.ErrorLevel,
		logrus.WarnLevel,
		logrus.InfoLevel,
	}
}

// Fire sends the log entry to the external system
func (h *ExternalLogHook) Fire(entry *logrus.Entry) error {
	h.bufferMux.Lock()
	defer h.bufferMux.Unlock()

	h.buffer = append(h.buffer, entry)

	// Flush if buffer is full
	if len(h.buffer) >= h.config.BufferSize {
		h.flush()
	}

	return nil
}

// flushWorker runs in background to periodically flush logs
func (h *ExternalLogHook) flushWorker() {
	for {
		select {
		case <-h.ticker.C:
			h.bufferMux.Lock()
			if len(h.buffer) > 0 {
				h.flush()
			}
			h.bufferMux.Unlock()
		case <-h.stopChan:
			h.ticker.Stop()
			h.bufferMux.Lock()
			if len(h.buffer) > 0 {
				h.flush()
			}
			h.bufferMux.Unlock()
			return
		}
	}
}

// flush sends buffered logs to external system
func (h *ExternalLogHook) flush() {
	if len(h.buffer) == 0 {
		return
	}

	// Convert entries to JSON
	var logs []map[string]interface{}
	for _, entry := range h.buffer {
		logEntry := map[string]interface{}{
			"timestamp": entry.Time.Format(time.RFC3339),
			"level":     entry.Level.String(),
			"message":   entry.Message,
		}

		// Add fields
		for k, v := range entry.Data {
			logEntry[k] = v
		}

		logs = append(logs, logEntry)
	}

	// Send to external system
	h.sendLogs(logs)

	// Clear buffer
	h.buffer = h.buffer[:0]
}

// sendLogs sends log batch to external system
func (h *ExternalLogHook) sendLogs(logs []map[string]interface{}) {
	jsonData, err := json.Marshal(logs)
	if err != nil {
		// Log error to stderr since we can't use the logger here
		logrus.StandardLogger().WithError(err).Error("Failed to marshal logs for external hook")
		return
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, h.config.URL, bytes.NewBuffer(jsonData))
	if err != nil {
		logrus.StandardLogger().WithError(err).Error("Failed to create request for external log hook")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range h.config.Headers {
		req.Header.Set(key, value)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		logrus.StandardLogger().WithError(err).Error("Failed to send logs to external system")
		return
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			logrus.StandardLogger().WithError(closeErr).Error("Failed to close response body")
		}
	}()

	if resp.StatusCode >= 400 {
		logrus.StandardLogger().WithFields(logrus.Fields{
			"status_code": resp.StatusCode,
			"url":         h.config.URL,
		}).Error("External log system returned error")
	}

	if resp.StatusCode >= 400 {
		logrus.StandardLogger().WithFields(logrus.Fields{
			"status_code": resp.StatusCode,
			"url":         h.config.URL,
		}).Error("External log system returned error")
	}
}

// Close stops the background worker and flushes remaining logs
func (h *ExternalLogHook) Close() {
	close(h.stopChan)
}

// LokiHook sends logs to Grafana Loki
type LokiHook struct {
	config HookConfig
	client *http.Client
}

// NewLokiHook creates a hook for sending logs to Loki
func NewLokiHook(lokiURL string, labels map[string]string) *LokiHook {
	config := HookConfig{
		Type:    "loki",
		URL:     lokiURL + "/loki/api/v1/push",
		Headers: map[string]string{"Content-Type": "application/json"},
		Timeout: 5 * time.Second,
	}

	return &LokiHook{
		config: config,
		client: &http.Client{Timeout: config.Timeout},
	}
}

func (h *LokiHook) Levels() []logrus.Level {
	return []logrus.Level{logrus.InfoLevel, logrus.WarnLevel, logrus.ErrorLevel}
}

func (h *LokiHook) Fire(entry *logrus.Entry) error {
	// Format for Loki push API
	stream := map[string]string{
		"level":  entry.Level.String(),
		"source": "libvirt-provisioner",
	}

	values := [][]interface{}{
		{
			fmt.Sprintf("%d", entry.Time.UnixNano()),
			entry.Message,
		},
	}

	payload := map[string]interface{}{
		"streams": []map[string]interface{}{
			{
				"stream": stream,
				"values": values,
			},
		},
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		logrus.StandardLogger().WithError(err).Error("Failed to marshal Loki payload")
		return nil
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, h.config.URL, bytes.NewBuffer(jsonData))
	if err != nil {
		logrus.StandardLogger().WithError(err).Error("Failed to create Loki request")
		return nil
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.client.Do(req)
	if err != nil {
		logrus.StandardLogger().WithError(err).Error("Failed to send logs to Loki")
		return nil
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			logrus.StandardLogger().WithError(closeErr).Error("Failed to close Loki response body")
		}
	}()

	if resp.StatusCode >= 400 {
		logrus.StandardLogger().WithFields(logrus.Fields{
			"status_code": resp.StatusCode,
			"url":         h.config.URL,
		}).Error("Loki returned error")
	}
	return nil
}
