# Configuration

The libvirt-volume-provisioner is configured via environment variables.

## Environment Variables

### Server Configuration

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `PORT` | HTTP server port | `8080` | No |
| `HOST` | HTTP server host | `0.0.0.0` | No |
| `TLS_CERT_FILE` | Path to TLS certificate | - | No |
| `TLS_KEY_FILE` | Path to TLS private key | - | No |

### MinIO Configuration

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `MINIO_ENDPOINT` | MinIO/S3 server URL | `https://minio.example.com` | Yes |
| `MINIO_ACCESS_KEY` | MinIO access key ID | - | Yes |
| `MINIO_SECRET_KEY` | MinIO secret key | - | Yes |
| `MINIO_REGION` | MinIO/S3 region | `us-east-1` | No |
| `MINIO_BUCKET` | MinIO bucket name | `vm-images` | No |
| `MINIO_USE_SSL` | Use SSL for MinIO connection | `true` | No |
| `MINIO_RETRY_ATTEMPTS` | Number of retry attempts | `3` | No |
| `MINIO_RETRY_BACKOFF_MS` | Retry backoff delays (comma-separated) | `100,1000,10000` | No |

### LVM Configuration

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `LVM_VOLUME_GROUP` | LVM volume group to use | `data` | No |
| `LVM_RETRY_ATTEMPTS` | Number of LVM retry attempts | `2` | No |
| `LVM_RETRY_BACKOFF_MS` | LVM retry backoff delays (comma-separated) | `100,1000` | No |

### Database Configuration

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `DB_PATH` | Path to job database file | `/var/lib/libvirt-volume-provisioner/jobs.db` | No |

### Authentication Configuration

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `CLIENT_CA_CERT` | Path to client CA certificate | `/etc/ssl/certs/ca-certificates.crt` | No |
| `API_TOKENS_FILE` | Path to API tokens file | `/etc/libvirt-volume-provisioner/tokens` | No |

### Logging Configuration

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `LOG_LEVEL` | Log level (debug, info, warn, error) | `info` | No |
| `LOG_FORMAT` | Log format (json, text) | `json` | No |

## Configuration Examples

### Basic Configuration

```bash
export MINIO_ENDPOINT="https://minio.example.com"
export MINIO_ACCESS_KEY="minioadmin"
export MINIO_SECRET_KEY="minioadmin"
export LVM_VOLUME_GROUP="vg0"
export PORT="8080"
```

### Production Configuration with TLS

```bash
export PORT="443"
export HOST="0.0.0.0"
export TLS_CERT_FILE="/etc/libvirt-volume-provisioner/server.crt"
export TLS_KEY_FILE="/etc/libvirt-volume-provisioner/server.key"
export CLIENT_CA_CERT="/etc/libvirt-volume-provisioner/client-ca.crt"

export MINIO_ENDPOINT="https://minio.prod.example.com"
export MINIO_ACCESS_KEY="prod-access-key"
export MINIO_SECRET_KEY="prod-secret-key"
export MINIO_BUCKET="production-vm-images"

export LVM_VOLUME_GROUP="prod-vg"
export LOG_LEVEL="info"
export LOG_FORMAT="json"
```

### Development Configuration

```bash
export MINIO_ENDPOINT="http://localhost:9000"
export MINIO_ACCESS_KEY="minioadmin"
export MINIO_SECRET_KEY="minioadmin"
export MINIO_USE_SSL="false"
export LVM_VOLUME_GROUP="vg0"
export LOG_LEVEL="debug"
export LOG_FORMAT="text"
```

## Systemd Service Configuration

Create or edit `/etc/default/libvirt-volume-provisioner`:

```bash
# Basic MinIO configuration
MINIO_ENDPOINT=https://minio.example.com
MINIO_ACCESS_KEY=your-access-key
MINIO_SECRET_KEY=your-secret-key

# LVM settings
LVM_VOLUME_GROUP=data

# Server settings
PORT=8080
HOST=0.0.0.0

# Logging
LOG_LEVEL=info
LOG_FORMAT=json
```

Then reload the service:

```bash
sudo systemctl daemon-reload
sudo systemctl restart libvirt-volume-provisioner
```

## Docker Configuration

Use environment variables with Docker:

```bash
docker run -d \
  --name libvirt-volume-provisioner \
  -e MINIO_ENDPOINT=https://minio.example.com \
  -e MINIO_ACCESS_KEY=minioadmin \
  -e MINIO_SECRET_KEY=minioadmin \
  -e LVM_VOLUME_GROUP=vg0 \
  -e PORT=8080 \
  -e LOG_LEVEL=info \
  ghcr.io/rossigee/libvirt-volume-provisioner:latest
```

Or use an environment file:

```bash
# .env file
MINIO_ENDPOINT=https://minio.example.com
MINIO_ACCESS_KEY=minioadmin
MINIO_SECRET_KEY=minioadmin
LVM_VOLUME_GROUP=vg0

docker run -d --env-file .env \
  --name libvirt-volume-provisioner \
  ghcr.io/rossigee/libvirt-volume-provisioner:latest
```

## Certificate Setup

See [Authentication](./authentication.md) for detailed certificate setup instructions.

## MinIO Retry Configuration

Configure retry behavior for MinIO connections:

```bash
# Number of retry attempts (default: 3)
export MINIO_RETRY_ATTEMPTS=5

# Retry backoff delays in milliseconds (comma-separated)
# Example: 100ms, 1s, 10s
export MINIO_RETRY_BACKOFF_MS=100,1000,10000
```

## LVM Retry Configuration

Configure retry behavior for LVM operations:

```bash
# Number of LVM retry attempts (default: 2)
export LVM_RETRY_ATTEMPTS=3

# Retry backoff delays in milliseconds
export LVM_RETRY_BACKOFF_MS=100,1000
```

## Logging Configuration

Control logging behavior:

```bash
# Log level: debug, info, warn, error
export LOG_LEVEL=debug

# Log format: json (structured), text (human-readable)
export LOG_FORMAT=json
```

Example output with JSON logging:

```json
{
  "timestamp": "2026-01-27T10:30:45.123Z",
  "level": "info",
  "component": "provisioner",
  "job_id": "550e8400-e29b-41d4-a716-446655440000",
  "message": "Starting image download",
  "image_url": "https://minio.example.com/images/ubuntu-20.04.qcow2"
}
```

## Verification

Verify configuration by checking the health endpoint:

```bash
curl https://hypervisor.example.com:8080/health \
  --cacert /path/to/ca.crt \
  --cert /path/to/client.crt \
  --key /path/to/client.key
```

Check service logs:

```bash
sudo journalctl -u libvirt-volume-provisioner -f
```

