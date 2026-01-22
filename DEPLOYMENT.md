# Deployment Guide

This guide covers deploying the libvirt-volume-provisioner in both traditional systemd service and containerized environments.

## Table of Contents

1. [Prerequisites](#prerequisites)
2. [Systemd Deployment (Recommended)](#systemd-deployment-recommended)
3. [Containerized Deployment](#containerized-deployment)
4. [Configuration](#configuration)
5. [Security Considerations](#security-considerations)
6. [Monitoring](#monitoring)
7. [Troubleshooting](#troubleshooting)

## Prerequisites

### System Requirements

- **Linux distribution** with systemd
- **libvirt** installed and running
- **LVM** configured with available volume group
- **MinIO** or S3-compatible storage accessible
- **Go 1.25+** (for building from source)

### Network Requirements

- **Inbound**: TCP port 8080 (configurable)
- **Outbound**: Access to MinIO/S3 endpoints
- **Local**: Access to libvirt socket (`/var/run/libvirt/libvirt-sock`)

### Permissions

The service requires access to:
- libvirt daemon socket
- LVM volume groups and devices
- Image storage directories
- SSL certificates (if using mutual TLS)

## Systemd Deployment (Recommended)

### 1. Install from Debian Package

```bash
# Download and install the .deb package
sudo dpkg -i libvirt-volume-provisioner_0.2.5_amd64.deb
sudo apt-get install -f  # Install dependencies if needed
```

### 2. Manual Installation

```bash
# Build the binary
make build-linux

# Install binary
sudo cp bin/libvirt-volume-provisioner /usr/bin/

# Install systemd files
sudo cp systemd/libvirt-volume-provisioner.service /etc/systemd/system/
sudo cp systemd/libvirt-volume-provisioner.socket /etc/systemd/system/

# Create user and directories
sudo useradd --system --shell /bin/false --home /var/lib/libvirt-volume-provisioner libvirt-volume-provisioner
sudo usermod -a -G libvirt-qemu libvirt-volume-provisioner

# Create directories
sudo mkdir -p /etc/libvirt-volume-provisioner /var/log/libvirt-volume-provisioner
sudo chown libvirt-volume-provisioner:libvirt-qemu /etc/libvirt-volume-provisioner
sudo chown libvirt-volume-provisioner:libvirt-qemu /var/log/libvirt-volume-provisioner
```

### 3. Configure Environment

```bash
# Edit the default configuration
sudo vi /etc/default/libvirt-volume-provisioner

# Example configuration:
MINIO_ENDPOINT=https://minio.example.com
MINIO_ACCESS_KEY=your-access-key
MINIO_SECRET_KEY=your-secret-key
LVM_VOLUME_GROUP=vg0
LOG_LEVEL=info
```

### 4. Start the Service

```bash
# Enable and start the service
sudo systemctl daemon-reload
sudo systemctl enable libvirt-volume-provisioner.socket
sudo systemctl start libvirt-volume-provisioner.socket

# Check status
sudo systemctl status libvirt-volume-provisioner.socket
sudo systemctl status libvirt-volume-provisioner.service

# View logs
sudo journalctl -u libvirt-volume-provisioner -f
```

## Containerized Deployment

### 1. Build the Container

```bash
# Build the production image
docker build -f Dockerfile.production -t libvirt-volume-provisioner:latest .

# Or use Docker Compose
docker-compose build
```

### 2. Prepare Host System

```bash
# Ensure libvirt socket has proper permissions
sudo usermod -a -G libvirt $(whoami)

# Create configuration directory
mkdir -p config/

# Create environment file
cp .env.example .env
vi .env  # Edit with your values
```

### 3. Run with Docker Compose

```bash
# Start the service
docker-compose up -d

# Check logs
docker-compose logs -f libvirt-volume-provisioner

# Check health
curl http://localhost:8080/health
```

### 4. Run with Docker Directly

```bash
docker run -d \
  --name libvirt-volume-provisioner \
  --privileged \
  -v /var/run/libvirt:/var/run/libvirt:rw \
  -v /var/lib/libvirt/images:/var/lib/libvirt/images:rw \
  -v /dev/mapper:/dev/mapper:rw \
  -v ./config:/etc/libvirt-volume-provisioner:ro \
  -v /etc/ssl/certs:/etc/ssl/certs:ro \
  -p 8080:8080 \
  -e MINIO_ENDPOINT=https://minio.example.com \
  -e MINIO_ACCESS_KEY=your-key \
  -e MINIO_SECRET_KEY=your-secret \
  -e LVM_VOLUME_GROUP=vg0 \
  libvirt-volume-provisioner:latest
```

## Configuration

### Environment Variables

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `MINIO_ENDPOINT` | MinIO/S3 server URL | `https://minio.example.com` | Yes |
| `MINIO_ACCESS_KEY` | MinIO access key | - | Yes |
| `MINIO_SECRET_KEY` | MinIO secret key | - | Yes |
| `LVM_VOLUME_GROUP` | LVM volume group name | `vg0` | Yes |
| `LISTEN_ADDR` | Server listen address | `127.0.0.1` (systemd), `0.0.0.0` (container) | No |
| `PORT` | Server port | `8080` | No |
| `CLIENT_CA_CERT` | Client CA certificate path | `/etc/ssl/certs/ca-certificates.crt` | No |
| `SERVER_CERT` | Server certificate path | - | No |
| `SERVER_KEY` | Server private key path | - | No |
| `API_TOKENS_FILE` | API tokens file path | `/etc/libvirt-volume-provisioner/tokens` | No |
| `LOG_LEVEL` | Logging level | `info` | No |

### API Tokens (Optional)

Create a tokens file for API authentication:

```bash
# /etc/libvirt-volume-provisioner/tokens
your-secure-api-token-here
another-token-if-needed
```

### SSL Certificates (Optional)

For mutual TLS authentication:

```bash
# Install certificates
sudo cp ca.crt /etc/ssl/certs/ca-certificates.crt
sudo cp server.crt /etc/ssl/certs/server.crt
sudo cp server.key /etc/ssl/private/server.key

# Set proper permissions
sudo chown root:root /etc/ssl/private/server.key
sudo chmod 600 /etc/ssl/private/server.key
```

## Security Considerations

### Systemd Deployment Security

- **Non-root user**: Runs as `libvirt-volume-provisioner` user
- **Limited privileges**: Only necessary system access
- **Device restrictions**: Limited to LVM and libvirt devices
- **Filesystem protection**: Read-only access where possible

### Container Deployment Security

- **Privileged mode**: Required for libvirt/LVM access (security trade-off)
- **Minimal base image**: Distroless for reduced attack surface
- **Read-only filesystem**: With tmpfs for temporary data
- **No new privileges**: Security hardening enabled

### Network Security

- **Local binding**: Defaults to localhost (systemd)
- **TLS encryption**: Mutual TLS when certificates provided
- **API tokens**: Additional authentication layer
- **Rate limiting**: Implement at reverse proxy level

## Monitoring

### Health Checks

```bash
# HTTP health endpoint
curl http://localhost:8080/health

# Response:
{
  "status": "healthy",
  "timestamp": "2024-01-14T10:30:00Z",
  "version": "0.2.5"
}
```

### Metrics

#### Prometheus Metrics Endpoint

```bash
# Access metrics (requires authentication)
curl -H "Authorization: Bearer <token>" https://localhost:3443/metrics
```

#### Available Metrics

- **HTTP Request Metrics**:
  - `libvirt_volume_provisioner_requests_total{endpoint,method,status}`
  - Tracks all API requests by endpoint, HTTP method, and response status

- **Job Metrics**:
  - `libvirt_volume_provisioner_jobs_total{status}` - Total jobs by final status
  - `libvirt_volume_provisioner_active_jobs` - Currently running jobs

- **Go Runtime Metrics**:
  - Memory usage, GC statistics, goroutine counts

#### ServiceMonitor Configuration

Deploy Prometheus ServiceMonitor for automatic metrics collection:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: libvirt-volume-provisioner
  namespace: monitoring
spec:
  selector:
    matchLabels:
      app: libvirt-volume-provisioner
  endpoints:
  - port: https
    path: /metrics
    scheme: https
    tlsConfig:
      insecureSkipVerify: true  # Use proper certs in production
    bearerTokenSecret:
      name: provisioner-api-tokens
      key: token
    interval: 30s
```

#### Alerting Rules

Configure these Prometheus alerting rules for operational monitoring:

```yaml
groups:
- name: libvirt-volume-provisioner
  rules:
  - alert: VolumeProvisionerDown
    expr: up{job="libvirt-volume-provisioner"} == 0
    for: 5m
    labels:
      severity: error
    annotations:
      summary: "Volume provisioner service is down"

  - alert: VolumeProvisionerHighErrorRate
    expr: rate(libvirt_volume_provisioner_requests_total{status=~"5.."}[5m]) / rate(libvirt_volume_provisioner_requests_total[5m]) > 0.1
    for: 10m
    labels:
      severity: warning
    annotations:
      summary: "High error rate on volume provisioner"

  - alert: VolumeProvisionerJobFailures
    expr: increase(libvirt_volume_provisioner_jobs_total{status="failed"}[10m]) > 5
    for: 5m
    labels:
      severity: error
    annotations:
      summary: "Multiple volume provisioning job failures"
```

### Logs

#### Systemd Logs
```bash
sudo journalctl -u libvirt-volume-provisioner -f
```

#### Container Logs
```bash
docker-compose logs -f libvirt-volume-provisioner
```

## Troubleshooting

### Common Issues

#### 1. Permission Denied

**Error**: `dial unix /var/run/libvirt/libvirt-sock: permission denied`

**Solution**:
```bash
# For systemd
sudo usermod -a -G libvirt libvirt-volume-provisioner

# For containers
sudo usermod -a -G libvirt $(whoami)
# Or run container with --group-add libvirt
```

#### 2. LVM Volume Group Not Found

**Error**: `volume group 'vg0' does not exist`

**Solution**:
```bash
# Check available volume groups
sudo vgs

# Set correct volume group in configuration
echo "LVM_VOLUME_GROUP=myvg" | sudo tee /etc/default/libvirt-volume-provisioner
```

#### 3. MinIO Connection Failed

**Error**: `connection refused` or `certificate verify failed`

**Solution**:
```bash
# Check connectivity
curl -I https://minio.example.com

# For self-signed certificates, add CA
sudo cp ca.crt /etc/ssl/certs/
sudo update-ca-certificates
```

#### 4. Container Cannot Access Devices

**Error**: `no such device` or `permission denied`

**Solution**: Ensure container runs with `--privileged` or proper device mounts:
```yaml
# docker-compose.yml
services:
  libvirt-volume-provisioner:
    privileged: true
    volumes:
      - /dev/mapper:/dev/mapper:rw
      - /dev/dm-*:/dev/dm-*:rw
```

### Debug Mode

Enable debug logging:
```bash
export LOG_LEVEL=debug
# Then restart the service
```

### Service Status Checks

```bash
# Systemd
sudo systemctl status libvirt-volume-provisioner

# Container
docker-compose ps
docker stats libvirt-volume-provisioner
```

### Log Analysis

Look for these patterns in logs:
- **Successful operations**: `Using cached image` or `Image downloaded and cached`
- **Errors**: `failed to` or `permission denied`
- **Performance**: Response times and throughput metrics