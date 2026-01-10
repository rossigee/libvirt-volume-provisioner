package auth

import (
	"crypto/tls"
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
	clientCALoaded bool            // Whether client CA certificates were loaded
	apiTokens      map[string]bool // Simple token validation
}

// NewValidator creates a new authentication validator
func NewValidator() (*Validator, error) {
	validator := &Validator{
		clientCAs: x509.NewCertPool(),
		apiTokens: make(map[string]bool),
	}

	// Load client CA certificates
	if err := validator.loadClientCAs(); err != nil {
		return nil, fmt.Errorf("failed to load client CAs: %w", err)
	}

	// Load API tokens
	if err := validator.loadAPITokens(); err != nil {
		return nil, fmt.Errorf("failed to load API tokens: %w", err)
	}

	return validator, nil
}

// loadClientCAs loads client certificate authorities
func (v *Validator) loadClientCAs() error {
	caCertPath := os.Getenv("CLIENT_CA_CERT")
	if caCertPath == "" {
		caCertPath = "/etc/ssl/certs/client-ca.pem"
	}

	if _, err := os.Stat(caCertPath); os.IsNotExist(err) {
		// For development, allow unauthenticated access
		v.clientCALoaded = false
		return nil
	}

	caCert, err := os.ReadFile(caCertPath)
	if err != nil {
		return fmt.Errorf("failed to read CA cert: %w", err)
	}

	if !v.clientCAs.AppendCertsFromPEM(caCert) {
		return fmt.Errorf("failed to parse CA cert")
	}

	v.clientCALoaded = true
	return nil
}

// loadAPITokens loads API tokens for authentication
func (v *Validator) loadAPITokens() error {
	tokenFile := os.Getenv("API_TOKENS_FILE")
	if tokenFile == "" {
		tokenFile = "/etc/libvirt-volume-provisioner/api-tokens"
	}

	if _, err := os.Stat(tokenFile); os.IsNotExist(err) {
		// For development, add a default token
		v.apiTokens["dev-token-12345"] = true
		return nil
	}

	content, err := os.ReadFile(tokenFile)
	if err != nil {
		return fmt.Errorf("failed to read API tokens: %w", err)
	}

	// Simple token list (one per line)
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		token := strings.TrimSpace(line)
		if token != "" {
			v.apiTokens[token] = true
		}
	}

	return nil
}

// Middleware returns Gin middleware for authentication
func (v *Validator) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check for API token in header
		if v.validateAPIToken(c) {
			c.Next()
			return
		}

		// Check for client certificate
		if tlsConn, ok := c.Request.Context().Value("tls-conn").(*tls.Conn); ok {
			if len(tlsConn.ConnectionState().PeerCertificates) > 0 {
				// Certificate validation is handled by TLS config
				c.Next()
				return
			}
		}

		// No valid authentication found
		c.AbortWithStatusJSON(401, types.ErrorResponse{
			Error:   "authentication required",
			Message: "provide valid API token or client certificate",
			Code:    401,
		})
	}
}

// validateAPIToken validates API token from Authorization or X-API-Token headers
func (v *Validator) validateAPIToken(c *gin.Context) bool {
	authHeader := c.GetHeader("Authorization")

	// Check for Bearer token in Authorization header
	if authHeader != "" && len(authHeader) > 7 && authHeader[:7] == "Bearer " {
		token := authHeader[7:]
		return v.apiTokens[token]
	}

	// Check for X-API-Token header
	token := c.GetHeader("X-API-Token")
	if token != "" {
		return v.apiTokens[token]
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
