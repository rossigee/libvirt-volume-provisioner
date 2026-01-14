package auth

import (
	"net/http"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestNewValidator(t *testing.T) {
	// Ensure no client CA file exists for test
	_ = os.Unsetenv("CLIENT_CA_CERT")
	defer func() { _ = os.Unsetenv("CLIENT_CA_CERT") }()

	// Set to non-existent path to ensure CA is not loaded
	_ = os.Setenv("CLIENT_CA_CERT", "/nonexistent/ca.pem")

	validator, err := NewValidator()

	assert.NoError(t, err)
	assert.NotNil(t, validator)
	assert.False(t, validator.IsClientCALoaded())
}

func TestLoadAPITokens(t *testing.T) {
	// Clear environment
	_ = os.Unsetenv("API_TOKENS_FILE")
	defer func() { _ = os.Unsetenv("API_TOKENS_FILE") }()

	tests := []struct {
		name           string
		tokensFile     string
		expectError    bool
		expectedTokens map[string]bool
	}{
		{
			name:           "no token file - use defaults",
			tokensFile:     "",
			expectError:    false,
			expectedTokens: map[string]bool{"dev-token-12345": true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.tokensFile != "" {
				_ = os.Setenv("API_TOKENS_FILE", tt.tokensFile)
			} else {
				_ = os.Unsetenv("API_TOKENS_FILE")
			}

			validator := &Validator{
				apiTokens: make(map[string]bool),
			}

			err := validator.loadAPITokens()

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				for token, expected := range tt.expectedTokens {
					assert.Equal(t, expected, validator.apiTokens[token])
				}
			}
		})
	}
}

func TestValidateAPIToken(t *testing.T) {
	validator := &Validator{
		apiTokens: map[string]bool{
			"valid-token":   true,
			"another-token": true,
		},
	}

	tests := []struct {
		name       string
		authHeader string
		apiToken   string
		expected   bool
	}{
		{
			name:       "valid bearer token",
			authHeader: "Bearer valid-token",
			expected:   true,
		},
		{
			name:     "valid X-API-Token",
			apiToken: "another-token",
			expected: true,
		},
		{
			name:       "invalid bearer token",
			authHeader: "Bearer invalid-token",
			expected:   false,
		},
		{
			name:     "invalid X-API-Token",
			apiToken: "invalid-token",
			expected: false,
		},
		{
			name:     "empty headers",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a fresh gin context for each test
			c, _ := gin.CreateTestContext(nil)
			c.Request = &http.Request{Header: make(http.Header)}
			if tt.authHeader != "" {
				c.Request.Header.Set("Authorization", tt.authHeader)
			}
			if tt.apiToken != "" {
				c.Request.Header.Set("X-API-Token", tt.apiToken)
			}
			result := validator.validateAPIToken(c)
			assert.Equal(t, tt.expected, result)
		})
	}
}
