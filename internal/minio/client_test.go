package minio

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClient(t *testing.T) {
	tests := []struct {
		name        string
		envVars     map[string]string
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid configuration",
			envVars: map[string]string{
				"MINIO_ENDPOINT":   "https://minio.example.com:9000",
				"MINIO_ACCESS_KEY": "test-access-key",
				"MINIO_SECRET_KEY": "test-secret-key",
			},
			expectError: false,
		},
		{
			name: "missing access key",
			envVars: map[string]string{
				"MINIO_ENDPOINT":          "https://minio.example.com:9000",
				"MINIO_SECRET_ACCESS_KEY": "test-secret-key",
			},
			expectError: true,
			errorMsg:    "MINIO_ACCESS_KEY or MINIO_ACCESS_KEY_ID environment variable is required",
		},
		{
			name: "missing secret key",
			envVars: map[string]string{
				"MINIO_ENDPOINT":      "https://minio.example.com:9000",
				"MINIO_ACCESS_KEY_ID": "test-access-key",
			},
			expectError: true,
			errorMsg:    "MINIO_SECRET_KEY or MINIO_SECRET_ACCESS_KEY environment variable is required",
		},
		{
			name: "invalid endpoint URL",
			envVars: map[string]string{
				"MINIO_ENDPOINT":   "not-a-url",
				"MINIO_ACCESS_KEY": "test-access-key",
				"MINIO_SECRET_KEY": "test-secret-key",
			},
			expectError: true,
			errorMsg:    "invalid MINIO_ENDPOINT",
		},
		{
			name: "endpoint without scheme",
			envVars: map[string]string{
				"MINIO_ENDPOINT":   "minio.example.com:9000",
				"MINIO_ACCESS_KEY": "test-access-key",
				"MINIO_SECRET_KEY": "test-secret-key",
			},
			expectError: true,
			errorMsg:    "invalid MINIO_ENDPOINT scheme",
		},
		{
			name: "default endpoint when not set",
			envVars: map[string]string{
				"MINIO_ACCESS_KEY": "test-access-key",
				"MINIO_SECRET_KEY": "test-secret-key",
			},
			expectError: false,
		},
		{
			name: "valid configuration with _ID suffix",
			envVars: map[string]string{
				"MINIO_ENDPOINT":          "https://minio.example.com:9000",
				"MINIO_ACCESS_KEY_ID":     "test-access-key-id",
				"MINIO_SECRET_ACCESS_KEY": "test-secret-access-key",
			},
			expectError: false,
		},
		{
			name: "mixed variable names (old and new)",
			envVars: map[string]string{
				"MINIO_ENDPOINT":          "https://minio.example.com:9000",
				"MINIO_ACCESS_KEY":        "test-access-key",
				"MINIO_SECRET_ACCESS_KEY": "test-secret-access-key",
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear environment
			_ = os.Unsetenv("MINIO_ENDPOINT")
			_ = os.Unsetenv("MINIO_ACCESS_KEY")
			_ = os.Unsetenv("MINIO_ACCESS_KEY_ID")
			_ = os.Unsetenv("MINIO_SECRET_KEY")
			_ = os.Unsetenv("MINIO_SECRET_ACCESS_KEY")

			// Set test environment variables
			for key, value := range tt.envVars {
				_ = os.Setenv(key, value)
			}

			// Test client creation
			client, err := NewClient()

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

func TestValidateImageURL(t *testing.T) {
	// Setup test client with mock environment
	_ = os.Setenv("MINIO_ENDPOINT", "https://minio.example.com:9000")
	_ = os.Setenv("MINIO_ACCESS_KEY", "test-access-key")
	_ = os.Setenv("MINIO_SECRET_KEY", "test-secret-key")
	defer func() {
		_ = os.Unsetenv("MINIO_ENDPOINT")
		_ = os.Unsetenv("MINIO_ACCESS_KEY")
		_ = os.Unsetenv("MINIO_SECRET_KEY")
	}()

	client, err := NewClient()
	require.NoError(t, err)
	require.NotNil(t, client)

	tests := []struct {
		name        string
		imageURL    string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "invalid URL format",
			imageURL:    "not-a-url",
			expectError: true,
			errorMsg:    "invalid image URL",
		},
		{
			name:        "URL without path",
			imageURL:    "https://minio.example.com:9000",
			expectError: true,
			errorMsg:    "invalid image URL path",
		},
		{
			name:        "URL with insufficient path parts",
			imageURL:    "https://minio.example.com:9000/bucket",
			expectError: true,
			errorMsg:    "invalid image URL path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := client.ValidateImageURL(context.TODO(), tt.imageURL)

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				// Note: This will fail in a real test environment without MinIO server
				// but we're testing URL validation logic here
				assert.Error(t, err) // Expect connection error in test environment
			}
		})
	}
}
