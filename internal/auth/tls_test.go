package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"testing"
	"time"
)

func generateRSACert() ([]byte, []byte, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate RSA key: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "test.example.com",
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().Add(time.Hour),
		KeyUsage:  x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create RSA certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	keyDER := x509.MarshalPKCS1PrivateKey(priv)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: keyDER})

	return certPEM, keyPEM, nil
}

func generateECDSACA() ([]byte, *ecdsa.PrivateKey, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate ECDSA key: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "Test CA",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}

	caDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create ECDSA CA certificate: %w", err)
	}

	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	return caPEM, priv, nil
}

func TestTLSLoadRSACert(t *testing.T) {
	certPEM, keyPEM, err := generateRSACert()
	if err != nil {
		t.Fatalf("Failed to generate RSA cert: %v", err)
	}

	_, err = tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Errorf("Failed to load RSA key pair: %v", err)
	}
}

func TestTLSLoadECDSACert(t *testing.T) {
	certPEM, keyPEM, err := generateECDSACert()
	if err != nil {
		t.Fatalf("Failed to generate ECDSA cert: %v", err)
	}

	_, err = tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Errorf("Failed to load ECDSA key pair: %v", err)
	}
}

func generateECDSACert() ([]byte, []byte, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate ECDSA key: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "test.example.com",
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().Add(time.Hour),
		KeyUsage:  x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create ECDSA certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal ECDSA key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return certPEM, keyPEM, nil
}

func generateECDSACertWithParams() ([]byte, []byte, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate ECDSA key: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "test.example.com",
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().Add(time.Hour),
		KeyUsage:  x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create ECDSA certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal ECDSA key: %w", err)
	}

	// Add EC PARAMETERS block as per OpenSSL format
	paramsDER, err := asn1.Marshal(asn1.ObjectIdentifier{1, 3, 132, 0, 34}) // secp384r1 OID
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal EC parameters: %w", err)
	}
	paramsPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PARAMETERS", Bytes: paramsDER})
	keyPEMBlock := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	keyPEM := append(paramsPEM, keyPEMBlock...)

	return certPEM, keyPEM, nil
}

func TestTLSLoadECDSACertWithParams(t *testing.T) {
	certPEM, keyPEM, err := generateECDSACertWithParams()
	if err != nil {
		t.Fatalf("Failed to generate ECDSA cert with params: %v", err)
	}

	_, err = tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Errorf("Failed to load ECDSA key pair with params: %v", err)
	}
}

func TestLoadECDSACA(t *testing.T) {
	caPEM, _, err := generateECDSACA()
	if err != nil {
		t.Fatalf("Failed to generate ECDSA CA: %v", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Error("Failed to load ECDSA CA")
	}
}

func TestLoadRSACA(t *testing.T) {
	caData, err := os.ReadFile("/tmp/ca-rsa.crt")
	if err != nil {
		t.Skip("RSA CA file not found, skipping test")
		return
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caData) {
		t.Error("Failed to load RSA CA")
	}
}
