// Package auth provides authentication and authorization functionality
// for the libvirt-volume-provisioner service.
package auth

import (
	"crypto/subtle"
	"crypto/x509"
	"fmt"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/rossigee/libvirt-volume-provisioner/pkg/types"
)

// Validator handles authentication validation
type Validator struct {
	clientCAs      *x509.CertPool
	clientCALoaded bool
	apiTokens      map[string]bool
}

// NewValidator creates a new authentication validator
func NewValidator() (*Validator, error) {
	validator := &Validator{
		clientCAs: x509.NewCertPool(),
		apiTokens: make(map[string]bool),
	}

	if err := validator.loadClientCAs(); err != nil {
		return nil, fmt.Errorf("failed to load client CAs: %w", err)
	}

	if err := validator.loadAPITokens(); err != nil {
		return nil, fmt.Errorf("failed to load API tokens: %w", err)
	}

	return validator, nil
}

// loadClientCAs loads client certificate authorities
func (v *Validator) loadClientCAs() error {
	caCertPath := os.Getenv("CLIENT_CA_CERT")
	if caCertPath == "" {
		// No client CA configured; mTLS client cert auth is disabled
		v.clientCALoaded = false
		return nil
	}

	if _, err := os.Stat(caCertPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("CLIENT_CA_CERT file not found: %s", caCertPath)
		}
		return fmt.Errorf("failed to stat CLIENT_CA_CERT %s: %w", caCertPath, err)
	}

	//nolint:gosec // Path is controlled by admin via environment variable
	caCert, err := os.ReadFile(caCertPath)
	if err != nil {
		return fmt.Errorf("failed to read CA cert: %w", err)
	}

	if !v.clientCAs.AppendCertsFromPEM(caCert) {
		return fmt.Errorf("failed to parse CA cert: no valid PEM blocks in %s", caCertPath)
	}

	v.clientCALoaded = true
	return nil
}

// loadAPITokens loads API tokens for authentication
func (v *Validator) loadAPITokens() error {
	tokenFile := os.Getenv("API_TOKENS_FILE")
	if tokenFile == "" {
		//nolint:gosec // Default configuration file path, not credentials
		tokenFile = "/etc/libvirt-volume-provisioner/tokens"
	}

	if _, err := os.Stat(tokenFile); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("API token file not found: %s (set API_TOKENS_FILE env var)", tokenFile)
		}
		return fmt.Errorf("failed to stat API token file %s: %w", tokenFile, err)
	}

	//nolint:gosec // Path is controlled by admin via environment variable
	content, err := os.ReadFile(tokenFile)
	if err != nil {
		return fmt.Errorf("failed to read API tokens: %w", err)
	}

	for _, line := range strings.Split(string(content), "\n") {
		if token := strings.TrimSpace(line); token != "" && !strings.HasPrefix(token, "#") {
			v.apiTokens[token] = true
		}
	}

	if len(v.apiTokens) == 0 {
		return fmt.Errorf("API token file %s contains no valid tokens", tokenFile)
	}

	return nil
}

// Middleware returns Gin middleware for authentication
func (v *Validator) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if v.validateAPIToken(c) {
			c.Next()
			return
		}

		// Check for a verified client certificate (populated by net/http for TLS connections)
		if c.Request.TLS != nil && len(c.Request.TLS.PeerCertificates) > 0 {
			// Certificate validation is handled at the TLS layer (ClientAuth: VerifyClientCertIfGiven)
			c.Next()
			return
		}

		c.AbortWithStatusJSON(401, types.ErrorResponse{
			Error:   "authentication required",
			Message: "provide valid API token or client certificate",
			Code:    401,
		})
	}
}

// validateAPIToken validates API token from Authorization or X-API-Token headers
// using constant-time comparison to prevent timing attacks.
func (v *Validator) validateAPIToken(c *gin.Context) bool {
	var token string

	authHeader := c.GetHeader("Authorization")
	if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
		token = authHeader[7:]
	} else {
		token = c.GetHeader("X-API-Token")
	}

	if token == "" {
		return false
	}

	tokenBytes := []byte(token)
	for storedToken := range v.apiTokens {
		if subtle.ConstantTimeCompare([]byte(storedToken), tokenBytes) == 1 {
			return true
		}
	}
	return false
}

// GetClientCAs returns the client CA certificate pool
func (v *Validator) GetClientCAs() *x509.CertPool {
	return v.clientCAs
}

// IsClientCALoaded returns whether client CA certificates were loaded
func (v *Validator) IsClientCALoaded() bool {
	return v.clientCALoaded
}

