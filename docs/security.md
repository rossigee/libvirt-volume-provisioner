# Security

This document outlines security considerations and best practices for deploying libvirt-volume-provisioner.

## Authentication & Authorization

### Mutual TLS (Recommended for Production)

All production deployments should use mutual TLS authentication:

- **Server**: Authenticates client certificates
- **Client**: Verifies server certificate
- **Traffic**: All communication encrypted with TLS 1.2+

See [Authentication](./authentication.md) for certificate setup.

### API Token Authentication

For development and testing only:

- Use strong, randomly generated tokens (minimum 32 characters)
- Rotate tokens regularly (at least quarterly)
- Never commit tokens to version control
- Use environment variables or secrets management

## Network Security

### Firewall Rules

Restrict access to provisioner port:

```bash
# Allow only from specific hosts
sudo ufw allow from 10.0.0.0/24 to any port 8080

# Block all other access
sudo ufw default deny incoming
sudo ufw enable
```

### Network Segmentation

Deploy provisioner in isolated network segment:

```
Internet → Load Balancer → Firewall → Provisioner Network
                                              ↓
                                    [hypervisor-1]
                                    [hypervisor-2]
                                    [hypervisor-3]
```

### VPN/Bastion Access

For remote access:

```bash
# Connect through bastion host
ssh -J bastion.example.com hypervisor.example.com

# Or use VPN for all infrastructure access
```

## Data Security

### MinIO Credentials

**Never hardcode credentials in code:**

- Use environment variables
- Use secrets management (Kubernetes Secrets, Vault, etc.)
- Rotate credentials regularly
- Use dedicated service accounts

Example with Kubernetes Secrets:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: minio-credentials
  namespace: default
type: Opaque
stringData:
  access-key: "your-access-key"
  secret-key: "your-secret-key"
```

### TLS Certificates

**Protect private keys:**

```bash
# Set restrictive permissions
chmod 600 /etc/libvirt-volume-provisioner/server.key
sudo chown libvirt-volume-provisioner:libvirt-volume-provisioner /etc/libvirt-volume-provisioner/server.key

# Use hardware security modules (HSM) for key storage in high-security environments
```

### Database Security

The job database contains operational information:

```bash
# Restrict database file permissions
chmod 600 /var/lib/libvirt-volume-provisioner/jobs.db

# Consider encrypting database if handling sensitive data
```

## Access Control

### File Permissions

Ensure proper permissions on all configuration files:

```bash
# Configuration directory
sudo chmod 700 /etc/libvirt-volume-provisioner
sudo chown libvirt-volume-provisioner:libvirt-volume-provisioner /etc/libvirt-volume-provisioner

# Certificate files
sudo chmod 600 /etc/libvirt-volume-provisioner/server.key
sudo chmod 644 /etc/libvirt-volume-provisioner/server.crt

# API tokens file
sudo chmod 600 /etc/libvirt-volume-provisioner/tokens
```

### Systemd Security

Harden systemd service:

```ini
[Unit]
Description=Libvirt Volume Provisioner
After=network.target libvirtd.service

[Service]
Type=simple
User=libvirt-volume-provisioner
Group=libvirt-volume-provisioner

# Security hardening
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/libvirt
ReadWritePaths=/var/lib/libvirt-volume-provisioner
ReadWritePaths=/dev/mapper

# Resource limits
LimitNOFILE=65535
MemoryLimit=4G
CPUAccounting=true

[Install]
WantedBy=multi-user.target
```

## Audit & Logging

### Enable Audit Logging

All operations should be logged:

```bash
# Check audit logging is enabled
export LOG_LEVEL=debug
export LOG_FORMAT=json

# Logs include:
# - All API requests (with user/token info)
# - Image downloads and caching
# - LVM operations
# - Errors and warnings
```

### Log Retention

Retain logs for forensic analysis:

```bash
# Systemd journal retention (14 days)
sudo mkdir -p /etc/systemd/journald.conf.d
echo "[Journal]
Storage=persistent
SystemMaxUse=4G
MaxRetentionSec=14days" | sudo tee /etc/systemd/journald.conf.d/retention.conf

sudo systemctl restart systemd-journald
```

### Log Monitoring

Monitor for security events:

```bash
# Alert on authentication failures
sudo journalctl -u libvirt-volume-provisioner | grep -i "unauthorized"

# Monitor job failures
sudo journalctl -u libvirt-volume-provisioner | grep -i "failed"
```

## Input Validation

### Request Validation

The provisioner validates all inputs:

```bash
# Validates image URLs
# - Must be valid HTTPS URL
# - Must point to MinIO bucket
# - Path traversal prevention

# Validates volume names
# - Must match LVM naming conventions
# - Prevents special characters/shell injection

# Validates sizes
# - Must be positive integers
# - Checked against available space
```

## Secrets Management

### Kubernetes Secrets

For Kubernetes deployments:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: provisioner-config
  namespace: default
type: Opaque
stringData:
  MINIO_ACCESS_KEY: "access-key"
  MINIO_SECRET_KEY: "secret-key"
  API_TOKENS_FILE: |
    token1:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
    token2:9d45e48ce8f4d6e1a2b8c3f4e5d6c7b8a9d0c1e2f3a4b5c6d7e8f9a0b1c2d3e4
```

### HashiCorp Vault

For advanced secret management:

```bash
# Store credentials in Vault
vault kv put secret/provisioner/minio \
  access_key="minioadmin" \
  secret_key="minioadmin"

# Retrieve in provisioner
export MINIO_ACCESS_KEY=$(vault kv get -field=access_key secret/provisioner/minio)
```

## Updates & Patching

### Regular Updates

Keep provisioner updated:

```bash
# Check for updates
apt list --upgradable | grep libvirt-volume-provisioner

# Update to latest version
sudo apt update
sudo apt install --only-upgrade libvirt-volume-provisioner

# Verify update
libvirt-volume-provisioner --version
```

### Security Advisories

Monitor for security issues:

- GitHub Security Advisories
- CVE databases
- Golang security mailing list
- libvirt and MinIO security updates

## Incident Response

### If Credentials Are Compromised

1. **Immediately revoke credentials:**
   ```bash
   # Remove MinIO credentials
   # Generate new MinIO credentials
   # Rotate API tokens
   ```

2. **Rotate certificates:**
   ```bash
   # Generate new client certificates
   # Install new certificates
   # Restart provisioner
   ```

3. **Review audit logs:**
   ```bash
   sudo journalctl -u libvirt-volume-provisioner --since "1 hour ago"
   ```

4. **Check for unauthorized volumes:**
   ```bash
   sudo lvs
   sudo qemu-img info /var/lib/libvirt/images/*
   ```

### If Service is Compromised

1. **Stop the service:**
   ```bash
   sudo systemctl stop libvirt-volume-provisioner
   ```

2. **Preserve logs for investigation:**
   ```bash
   sudo journalctl -u libvirt-volume-provisioner -o json > /tmp/audit.json
   ```

3. **Audit all recent operations:**
   - Review job history
   - Check downloaded images
   - Verify volume operations

4. **Perform forensic analysis:**
   - Check for unauthorized changes
   - Review network connections
   - Analyze memory dumps if available

5. **Restore from known-good backup:**
   ```bash
   sudo systemctl stop libvirt-volume-provisioner
   # Restore configuration, certificates, database
   sudo systemctl start libvirt-volume-provisioner
   ```

## Security Checklist

### Deployment Checklist

- [ ] Mutual TLS enabled with valid certificates
- [ ] Firewall rules configured to restrict access
- [ ] Service runs as non-root user
- [ ] File permissions properly set (600/644)
- [ ] MinIO credentials configured via environment/secrets
- [ ] API tokens generated with strong randomness
- [ ] Audit logging enabled
- [ ] Log retention configured
- [ ] Regular backups scheduled
- [ ] Security updates monitored and applied

### Operational Checklist

- [ ] Monthly certificate expiration reviews
- [ ] Quarterly credential rotation
- [ ] Security patch updates applied promptly
- [ ] Audit logs reviewed regularly
- [ ] Access logs monitored for anomalies
- [ ] Disaster recovery procedures tested
- [ ] Incident response procedures documented

