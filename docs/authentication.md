# Authentication

The libvirt-volume-provisioner supports two authentication methods:

1. **X.509 Client Certificates** (Primary, Recommended for Production)
2. **HMAC-SHA256 API Tokens** (Fallback, for Development/Testing)

## X.509 Client Certificates (Mutual TLS)

Mutual TLS authentication provides the strongest security by requiring both client and server to verify each other's certificates.

### Creating Certificates

#### 1. Create Client CA Certificate

```bash
# Generate CA private key
openssl genrsa -out client-ca.key 4096

# Create CA certificate (valid for 1 year)
openssl req -new -x509 -days 365 -key client-ca.key -sha256 -out client-ca.crt \
  -subj "/C=US/ST=State/L=City/O=Organization/CN=client-ca"
```

#### 2. Create Server Certificate

```bash
# Generate server private key
openssl genrsa -out server.key 4096

# Create server certificate signing request
openssl req -new -key server.key -out server.csr \
  -subj "/C=US/ST=State/L=City/O=Organization/CN=hypervisor.example.com"

# Sign server certificate with CA
openssl x509 -req -in server.csr -CA client-ca.crt -CAkey client-ca.key \
  -CAcreateserial -out server.crt -days 365 -sha256
```

#### 3. Create Client Certificate

```bash
# Generate client private key
openssl genrsa -out client.key 4096

# Create client certificate signing request
openssl req -new -key client.key -out client.csr \
  -subj "/C=US/ST=State/L=City/O=Organization/CN=client"

# Sign client certificate with CA
openssl x509 -req -in client.csr -CA client-ca.crt -CAkey client-ca.key \
  -CAcreateserial -out client.crt -days 365 -sha256
```

### Installing Certificates

#### On Provisioner Host (Server)

```bash
# Copy certificates to secure location
sudo cp server.crt /etc/libvirt-volume-provisioner/server.crt
sudo cp server.key /etc/libvirt-volume-provisioner/server.key
sudo cp client-ca.crt /etc/libvirt-volume-provisioner/client-ca.crt

# Set proper permissions
sudo chmod 600 /etc/libvirt-volume-provisioner/server.key
sudo chmod 644 /etc/libvirt-volume-provisioner/server.crt
sudo chmod 644 /etc/libvirt-volume-provisioner/client-ca.crt

# Update service configuration
sudo systemctl edit libvirt-volume-provisioner
```

Add environment variables:

```ini
[Service]
Environment="TLS_CERT_FILE=/etc/libvirt-volume-provisioner/server.crt"
Environment="TLS_KEY_FILE=/etc/libvirt-volume-provisioner/server.key"
Environment="CLIENT_CA_CERT=/etc/libvirt-volume-provisioner/client-ca.crt"
```

#### On Client Host

```bash
# Copy client certificates to a secure location
cp client.crt ~/.config/libvirt-provisioner/client.crt
cp client.key ~/.config/libvirt-provisioner/client.key
cp client-ca.crt ~/.config/libvirt-provisioner/ca.crt

# Set proper permissions
chmod 600 ~/.config/libvirt-provisioner/client.key
chmod 644 ~/.config/libvirt-provisioner/client.crt
chmod 644 ~/.config/libvirt-provisioner/ca.crt
```

### Using Client Certificates

All API requests require the client certificate and key:

```bash
curl -X POST https://hypervisor.example.com:8080/api/v1/provision \
  --cacert ~/.config/libvirt-provisioner/ca.crt \
  --cert ~/.config/libvirt-provisioner/client.crt \
  --key ~/.config/libvirt-provisioner/client.key \
  -H "Content-Type: application/json" \
  -d '{ ... }'
```

## API Tokens (Development/Testing)

For simpler deployments or testing, you can use HMAC-SHA256 API tokens.

### Generating API Tokens

```bash
# Create token file
cat > /etc/libvirt-volume-provisioner/tokens << EOF
# Format: token_name:token_value
provisioner-client-1:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
provisioner-client-2:9d45e48ce8f4d6e1a2b8c3f4e5d6c7b8a9d0c1e2f3a4b5c6d7e8f9a0b1c2d3e4
EOF

# Set restrictive permissions
chmod 600 /etc/libvirt-volume-provisioner/tokens

# Configure provisioner
sudo systemctl edit libvirt-volume-provisioner
```

Add environment variable:

```ini
[Service]
Environment="API_TOKENS_FILE=/etc/libvirt-volume-provisioner/tokens"
```

### Using API Tokens

#### Bearer Token Header

```bash
curl -X POST https://hypervisor.example.com:8080/api/v1/provision \
  -H "Authorization: Bearer e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" \
  -H "Content-Type: application/json" \
  -d '{ ... }'
```

#### X-API-Token Header

```bash
curl -X POST https://hypervisor.example.com:8080/api/v1/provision \
  -H "X-API-Token: e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" \
  -H "Content-Type: application/json" \
  -d '{ ... }'
```

## Authentication Security Best Practices

1. **Always use HTTPS**: Never use HTTP in production
2. **Rotate certificates**: Replace certificates before expiration
3. **Protect private keys**: Store with restrictive permissions (600)
4. **Use short-lived tokens**: Rotate API tokens regularly
5. **Monitor authentication**: Log and audit all authentication attempts
6. **Use unique credentials**: Don't share client certificates/tokens
7. **Revoke compromised credentials**: Immediately remove compromised tokens/certs
8. **Audit access**: Maintain comprehensive logs of all API access

## Troubleshooting Authentication

### Certificate Verification Failed

```
curl: (60) SSL certificate problem: self signed certificate
```

Solution: Add the CA certificate or use `--insecure` (development only):

```bash
curl --cacert client-ca.crt https://hypervisor.example.com:8080/health
```

### Client Certificate Not Accepted

```
curl: (58) unable to use client certificate (no key found or wrong pass phrase?)
```

Solution: Verify certificate and key are in correct format and accessible:

```bash
# Check certificate details
openssl x509 -in client.crt -text -noout

# Check key format
openssl rsa -in client.key -check
```

### Token Authentication Failed

```json
{
  "error": "unauthorized",
  "details": "invalid or missing API token"
}
```

Solution: Verify token is in correct format and included in request header:

```bash
# Check token format (should be valid SHA256 hex)
echo "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" | \
  grep -E '^[a-f0-9]{64}$' && echo "Valid" || echo "Invalid"
```

