package auth

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeTokenFile writes tokens to a temp file and returns its path.
func writeTokenFile(t *testing.T, tokens ...string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "tokens-*")
	require.NoError(t, err)
	for _, tok := range tokens {
		_, err = f.WriteString(tok + "\n")
		require.NoError(t, err)
	}
	require.NoError(t, f.Close())
	return f.Name()
}

func TestNewValidator(t *testing.T) {
	tokenFile := writeTokenFile(t, "test-token-abcdef")
	t.Setenv("API_TOKENS_FILE", tokenFile)

	t.Run("no client CA configured", func(t *testing.T) {
		t.Setenv("CLIENT_CA_CERT", "")
		validator, err := NewValidator()
		require.NoError(t, err)
		require.NotNil(t, validator)
		assert.False(t, validator.IsClientCALoaded())
	})

	t.Run("client CA points to non-existent file", func(t *testing.T) {
		t.Setenv("CLIENT_CA_CERT", "/nonexistent/ca.pem")
		_, err := NewValidator()
		assert.Error(t, err)
	})
}

func TestLoadAPITokens(t *testing.T) {
	t.Run("missing token file returns error", func(t *testing.T) {
		t.Setenv("API_TOKENS_FILE", filepath.Join(t.TempDir(), "nonexistent-tokens"))
		v := &Validator{apiTokens: make(map[string]bool)}
		err := v.loadAPITokens()
		assert.Error(t, err)
	})

	t.Run("valid token file loads tokens", func(t *testing.T) {
		tokenFile := writeTokenFile(t, "token-one", "token-two", "# comment")
		t.Setenv("API_TOKENS_FILE", tokenFile)
		v := &Validator{apiTokens: make(map[string]bool)}
		require.NoError(t, v.loadAPITokens())
		assert.True(t, v.apiTokens["token-one"])
		assert.True(t, v.apiTokens["token-two"])
		assert.False(t, v.apiTokens["# comment"])
	})

	t.Run("empty token file returns error", func(t *testing.T) {
		f, err := os.CreateTemp(t.TempDir(), "empty-tokens-*")
		require.NoError(t, err)
		require.NoError(t, f.Close())
		t.Setenv("API_TOKENS_FILE", f.Name())
		v := &Validator{apiTokens: make(map[string]bool)}
		err = v.loadAPITokens()
		assert.Error(t, err)
	})
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
