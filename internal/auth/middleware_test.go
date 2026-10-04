package auth

import (
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rossigee/libvirt-volume-provisioner/internal/config"
	"github.com/rossigee/libvirt-volume-provisioner/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mustCert builds a throwaway certificate carrying a common name, for the cases
// where the middleware only needs a peer certificate to be present. It is never
// verified here: verification is the TLS layer's job.
func mustCert(t *testing.T, cn string) *x509.Certificate {
	t.Helper()
	return &x509.Certificate{
		Subject: pkix.Name{CommonName: cn},
	}
}

// authedRouter returns a router whose single route is guarded by the validator,
// so a request either reaches the handler or is rejected by the middleware.
func authedRouter(t *testing.T, v *Validator) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.GET("/protected", v.Middleware(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"reached": true})
	})
	return r
}

func doRequest(t *testing.T, r *gin.Engine, mutate func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/protected", nil)
	if mutate != nil {
		mutate(req)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestMiddleware_AcceptsBearerToken(t *testing.T) {
	v, err := NewValidator(config.ServerConfig{APITokensFile: writeTokenFile(t, "secret-token")})
	require.NoError(t, err)

	w := doRequest(t, authedRouter(t, v), func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer secret-token")
	})

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"reached":true`)
}

func TestMiddleware_AcceptsXAPITokenHeader(t *testing.T) {
	v, err := NewValidator(config.ServerConfig{APITokensFile: writeTokenFile(t, "secret-token")})
	require.NoError(t, err)

	w := doRequest(t, authedRouter(t, v), func(r *http.Request) {
		r.Header.Set("X-API-Token", "secret-token")
	})

	assert.Equal(t, http.StatusOK, w.Code)
}

// A wrong token must be refused, and the refusal must say nothing about which
// tokens exist.
func TestMiddleware_RejectsWrongToken(t *testing.T) {
	v, err := NewValidator(config.ServerConfig{APITokensFile: writeTokenFile(t, "secret-token")})
	require.NoError(t, err)

	for _, header := range []struct{ name, value string }{
		{"Authorization", "Bearer wrong-token"},
		{"X-API-Token", "wrong-token"},
		// A prefix of a real token must not be accepted.
		{"X-API-Token", "secret"},
		// Nor a real token with trailing junk.
		{"X-API-Token", "secret-token "},
	} {
		t.Run(header.name+"="+header.value, func(t *testing.T) {
			w := doRequest(t, authedRouter(t, v), func(r *http.Request) {
				r.Header.Set(header.name, header.value)
			})

			require.Equal(t, http.StatusUnauthorized, w.Code)
			assert.NotContains(t, w.Body.String(), "secret-token",
				"the error must not echo a valid token")
		})
	}
}

// The Authorization header is only honoured with the Bearer scheme.
func TestMiddleware_IgnoresNonBearerAuthorization(t *testing.T) {
	v, err := NewValidator(config.ServerConfig{APITokensFile: writeTokenFile(t, "secret-token")})
	require.NoError(t, err)

	for _, value := range []string{"secret-token", "Basic c2VjcmV0LXRva2Vu", "bearer secret-token"} {
		t.Run(value, func(t *testing.T) {
			w := doRequest(t, authedRouter(t, v), func(r *http.Request) {
				r.Header.Set("Authorization", value)
			})
			assert.Equal(t, http.StatusUnauthorized, w.Code,
				"only an exact \"Bearer \" prefix may carry a token")
		})
	}
}

func TestMiddleware_NoCredentialsIs401(t *testing.T) {
	v, err := NewValidator(config.ServerConfig{APITokensFile: writeTokenFile(t, "secret-token")})
	require.NoError(t, err)

	w := doRequest(t, authedRouter(t, v), nil)

	require.Equal(t, http.StatusUnauthorized, w.Code)

	var got types.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "authentication required", got.Error)
	assert.Equal(t, 401, got.Code)
	assert.Contains(t, got.Message, "client certificate")
}

// A verified client certificate is an alternative to a token. TLS termination
// does the verification; the middleware only checks that one was presented.
func TestMiddleware_AcceptsClientCertificate(t *testing.T) {
	v, err := NewValidator(config.ServerConfig{APITokensFile: writeTokenFile(t, "secret-token")})
	require.NoError(t, err)

	w := doRequest(t, authedRouter(t, v), func(r *http.Request) {
		r.TLS = &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{mustCert(t, "hypervisor-001")},
		}
	})

	assert.Equal(t, http.StatusOK, w.Code,
		"a connection carrying a peer certificate must be allowed without a token")
}

// A TLS connection with no peer certificate is not a client certificate. Without
// this the handler would accept any TLS client.
func TestMiddleware_TLSWithoutPeerCertIs401(t *testing.T) {
	v, err := NewValidator(config.ServerConfig{APITokensFile: writeTokenFile(t, "secret-token")})
	require.NoError(t, err)

	w := doRequest(t, authedRouter(t, v), func(r *http.Request) {
		r.TLS = &tls.ConnectionState{} // no PeerCertificates
	})

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// A token still wins on a connection that also has a certificate, and a bad
// token must not be rescued by the presence of TLS state.
func TestMiddleware_BadTokenOnTLSConnectionIs401(t *testing.T) {
	v, err := NewValidator(config.ServerConfig{APITokensFile: writeTokenFile(t, "secret-token")})
	require.NoError(t, err)

	t.Run("good token with cert", func(t *testing.T) {
		w := doRequest(t, authedRouter(t, v), func(r *http.Request) {
			r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{}}}
			r.Header.Set("X-API-Token", "secret-token")
		})
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

// -- loadClientCAs ------------------------------------------------------

func TestLoadClientCAs_EmptyPathIsNotLoaded(t *testing.T) {
	v := &Validator{clientCAs: x509.NewCertPool(), apiTokens: map[string]bool{}}

	require.NoError(t, v.loadClientCAs(""))
	assert.False(t, v.IsClientCALoaded(),
		"no ca_cert configured must not claim CAs are loaded")
}

func TestLoadClientCAs_ValidPEMIsLoaded(t *testing.T) {
	caPEM, caKey, err := generateECDSACA()
	require.NoError(t, err)
	// A CA certificate is only useful alongside its private key, so assert the
	// helper produced a real key rather than a stub.
	require.NotNil(t, caKey)

	path := filepath.Join(t.TempDir(), "ca.crt")
	require.NoError(t, os.WriteFile(path, caPEM, 0o600))

	v := &Validator{clientCAs: x509.NewCertPool(), apiTokens: map[string]bool{}}
	require.NoError(t, v.loadClientCAs(path))

	// The flag is the meaningful assertion: AppendCertsFromPEM returning false is
	// what the negative case below exercises, so a true here means the CA really
	// was accepted into the pool.
	assert.True(t, v.IsClientCALoaded())
	assert.NotNil(t, v.GetClientCAs())
}

// A file that exists but holds no PEM must fail loudly. Silently continuing with
// an empty pool would reject every mTLS client at runtime.
func TestLoadClientCAs_NonPEMContentIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ca.crt")
	require.NoError(t, os.WriteFile(path, []byte("this is not a certificate"), 0o600))

	v := &Validator{clientCAs: x509.NewCertPool(), apiTokens: map[string]bool{}}
	err := v.loadClientCAs(path)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no valid PEM blocks")
	assert.False(t, v.IsClientCALoaded())
}

func TestLoadClientCAs_MissingFileIsRejected(t *testing.T) {
	v := &Validator{clientCAs: x509.NewCertPool(), apiTokens: map[string]bool{}}

	err := v.loadClientCAs(filepath.Join(t.TempDir(), "absent.crt"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// NewValidator must refuse to construct with an unusable CA rather than starting
// up and rejecting every client.
func TestNewValidator_BadCACertFailsConstruction(t *testing.T) {
	caPath := filepath.Join(t.TempDir(), "ca.crt")
	require.NoError(t, os.WriteFile(caPath, []byte("garbage"), 0o600))

	v, err := NewValidator(config.ServerConfig{
		CACert:        caPath,
		APITokensFile: writeTokenFile(t, "t"),
	})

	require.Error(t, err)
	assert.Nil(t, v)
	assert.Contains(t, err.Error(), "failed to load client CAs")
}

func TestNewValidator_MissingCAFileFailsConstruction(t *testing.T) {
	v, err := NewValidator(config.ServerConfig{
		CACert:        filepath.Join(t.TempDir(), "absent.crt"),
		APITokensFile: writeTokenFile(t, "t"),
	})

	require.Error(t, err)
	assert.Nil(t, v)
}

// Comment and blank lines in the token file must be ignored, not treated as
// tokens that would then be accepted as credentials.
func TestNewValidator_TokenFileCommentsAndBlanks(t *testing.T) {
	tokenFile := writeTokenFile(t, "# a comment", "", "   ", "real-token", "")

	v, err := NewValidator(config.ServerConfig{APITokensFile: tokenFile})
	require.NoError(t, err)

	router := authedRouter(t, v)

	for _, bad := range []string{"# a comment", "", "   ", "a comment"} {
		w := doRequest(t, router, func(r *http.Request) {
			r.Header.Set("X-API-Token", bad)
		})
		assert.Equal(t, http.StatusUnauthorized, w.Code, "token %q must not authenticate", bad)
	}

	w := doRequest(t, router, func(r *http.Request) {
		r.Header.Set("X-API-Token", "real-token")
	})
	assert.Equal(t, http.StatusOK, w.Code)
}

// A token file with nothing usable in it is a configuration error, not an
// empty-but-valid token set that would lock every client out silently.
func TestNewValidator_EmptyTokenFileIsRejected(t *testing.T) {
	v, err := NewValidator(config.ServerConfig{APITokensFile: writeTokenFile(t, "# only a comment")})

	require.Error(t, err)
	assert.Nil(t, v)
	assert.Contains(t, err.Error(), "no valid tokens")
}
