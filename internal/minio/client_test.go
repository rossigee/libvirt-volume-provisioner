package minio

import (
	"context"
	"testing"

	"github.com/rossigee/libvirt-volume-provisioner/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClient(t *testing.T) {
	tests := []struct {
		name        string
		cfg         config.MinIOConfig
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid configuration",
			cfg: config.MinIOConfig{
				Endpoint:  "https://minio.example.com:9000",
				AccessKey: "test-access-key",
				SecretKey: "test-secret-key",
			},
			expectError: false,
		},
		{
			name: "missing access key",
			cfg: config.MinIOConfig{
				Endpoint:  "https://minio.example.com:9000",
				SecretKey: "test-secret-key",
			},
			expectError: true,
			errorMsg:    "access_key is required",
		},
		{
			name: "missing secret key",
			cfg: config.MinIOConfig{
				Endpoint:  "https://minio.example.com:9000",
				AccessKey: "test-access-key",
			},
			expectError: true,
			errorMsg:    "secret_key is required",
		},
		{
			name: "invalid endpoint URL",
			cfg: config.MinIOConfig{
				Endpoint:  "not-a-url",
				AccessKey: "test-access-key",
				SecretKey: "test-secret-key",
			},
			expectError: true,
			errorMsg:    "invalid minio endpoint scheme",
		},
		{
			name: "endpoint without scheme",
			cfg: config.MinIOConfig{
				Endpoint:  "minio.example.com:9000",
				AccessKey: "test-access-key",
				SecretKey: "test-secret-key",
			},
			expectError: true,
			errorMsg:    "invalid minio endpoint scheme",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewClient(tt.cfg)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, client)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, client)
			}
		})
	}
}

func testClient(t *testing.T) *Client {
	t.Helper()
	client, err := NewClient(config.MinIOConfig{
		Endpoint:      "https://minio.example.com:9000",
		AccessKey:     "test-access-key",
		SecretKey:     "test-secret-key",
		RetryAttempts: 3,
		RetryBackoffMS: []int{100, 1000, 10000},
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	return client
}

// MockProgressUpdater for testing download methods
type MockProgressUpdater struct {
	updates []struct {
		stage     string
		percent   float64
		processed int64
		total     int64
	}
}

func (m *MockProgressUpdater) UpdateProgress(stage string, percent float64, bytesProcessed, bytesTotal int64) {
	m.updates = append(m.updates, struct {
		stage     string
		percent   float64
		processed int64
		total     int64
	}{stage, percent, bytesProcessed, bytesTotal})
}

// TestDownloadImageToPath tests the DownloadImageToPath method
func TestDownloadImageToPath(t *testing.T) {
	client := testClient(t)
	updater := &MockProgressUpdater{}

	// Test with invalid URL - should fail during URL parsing
	err := client.DownloadImageToPath(context.Background(), "not-a-valid-url", "/tmp/test.img", updater)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid image URL")

	// Test with valid URL format but invalid destination path (this will fail at network level, not path validation)
	err = client.DownloadImageToPath(context.Background(),
		"https://minio.example.com:9000/test-bucket/test-image.qcow2",
		"/invalid/path/test.img", updater)
	assert.Error(t, err) // Expected to fail due to network/connection issues, not path validation

	// Test with valid URL format and path but non-existent server - should fail during connection
	err = client.DownloadImageToPath(context.Background(),
		"https://minio.example.com:9000/test-bucket/test-image.qcow2",
		"/tmp/test.img", updater)
	assert.Error(t, err) // Expected to fail in test environment without MinIO server
}
