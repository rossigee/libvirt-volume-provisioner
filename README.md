# libvirt-volume-provisioner

A daemon service for provisioning LVM volumes with VM images on libvirt hypervisor hosts.

## Overview

The `libvirt-volume-provisioner` runs as a systemd service on hypervisor hosts and provides an HTTP API for:

- Downloading VM images from MinIO object storage with intelligent checksum-based caching
- Caching images with compression preservation to reduce disk space usage
- Converting cached QCOW2 images to raw format for LVM volume population
- Populating LVM volumes with VM disk data
- Progress tracking and error reporting

## Bigger Picture: VM Deployment Workflow

The `libvirt-volume-provisioner` is a critical component in a complete VM deployment system. Here's how it fits into the larger infrastructure:

### Complete Workflow

```
1. IMAGE PREPARATION
   User/CI Pipeline
        ↓
   Build VM Image (cloud-init enabled)
        ↓
   Upload to MinIO Bucket
   (ubuntu-20.04.qcow2 + .sha256 checksum)

2. VM DEFINITION
   Infrastructure-as-Code (Terraform/Ansible/etc)
        ↓
   Define VM in libvirtd:
   - vCPU, Memory, Network
   - Root volume attachment (empty or placeholder)
   - Cloud-init user-data config

3. ROOT VOLUME PROVISIONING ← libvirt-volume-provisioner starts here
   Infrastructure Automation
        ↓
   Call: POST /api/v1/provision
   - Image URL: MinIO bucket location
   - Volume: LVM device for root disk
   - Size: Desired disk size
        ↓
   Wait for provisioning to complete
   (Check cache → Download → Populate LVM volume)

4. VM STARTUP
   Infrastructure Automation
        ↓
   Start VM via libvirtd
        ↓
   Cloud-init runs (first boot)
   - Reads user-data configuration
   - Provisions VM with desired state:
     * User accounts
     * SSH keys
     * Packages
     * Configuration management setup
   - Configures networking
   - Runs custom provisioning scripts
        ↓
   VM fully operational

5. SUBSEQUENT REPROVISIONING
   To reprovision existing VM:
   ↓
   Shut down VM
   ↓
   Call: POST /api/v1/provision (same volume)
   - Volume is reused (size validated)
   - Image re-populated with fresh base
   ↓
   Start VM
   ↓
   Cloud-init re-provisions with new user-data
```

### Component Interaction Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│ Infrastructure Orchestration (infrastructure-builder, etc)       │
└──────────────────────┬──────────────────────────────────────────┘
                       │
        ┌──────────────┼──────────────┬──────────────┐
        │              │              │              │
        ▼              ▼              ▼              ▼
    ┌────────┐   ┌──────────┐   ┌─────────┐   ┌─────────────────┐
    │ MinIO  │   │ libvirtd │   │   LVM   │   │ Cloud-Init      │
    │Bucket  │   │ VM Mgmt  │   │Volumes  │   │Configuration   │
    │        │   │          │   │         │   │                 │
    │Images  │   │ VM Defs  │   │ Storage │   │ User-data       │
    └────────┘   └──────────┘   └─────────┘   │ Provisioning    │
        ▲              ▲              ▲         └─────────────────┘
        │              │              │              ▲
        │              └──────────────┼──────────────┘
        │                             │
        └─────────────────────────────┼───────────────────┐
                                      │                   │
                        ┌─────────────▼──────────────┐    │
                        │ libvirt-volume-provisioner │    │
                        │                            │    │
                        │ • Check cache              │    │
                        │ • Download images          │    │
                        │ • Populate LVM volumes     │    │
                        │ • Convert QCOW2 → RAW     │    │
                        └────────────────────────────┘    │
                                      ▲                   │
                                      │                   │
                                      └───────────────────┘
                              Infrastructure API Calls
```

### Key Design Concepts

1. **Image Immutability**: Base images in MinIO never change; reprovisioning gets fresh copy
2. **Idempotent Provisioning**: Cloud-init ensures VM reaches desired state regardless of history
3. **Volume Reuse**: Same LVM volume can be repopulated multiple times (for reprovisioning)
4. **Separation of Concerns**:
   - MinIO: Stores base images
   - libvirtd: Manages VM lifecycle and resources
   - libvirt-volume-provisioner: Bridges the gap (populates volumes from images)
   - Cloud-init: Final configuration and customization

## Architecture

```
Client (infrastructure-builder)
    ↓ HTTP API
libvirt-volume-provisioner (daemon)
    ↓ Check Cache & Download
MinIO (.sha256 checksums) → libvirt Pool Cache → LVM Volume
    ↓
VM Definition → libvirt → Running VM
```

### Image Caching

The provisioner implements intelligent image caching with compression preservation:

- **Checksum-based caching**: Uses SHA256 checksums from MinIO `.sha256` files as cache keys
- **Compression-preserving storage**: Images are cached as plain files in `/var/lib/libvirt/images/`, preserving QCOW2 compression instead of expanding to raw format
- **Cache directory**: Managed by libvirt's `images` storage pool
- **Fallback behavior**: Falls back to URL-based caching if checksums aren't available
- **Cache validation**: Verifies cached images against checksums before use
- **Storage efficiency**: Cached QCOW2 images remain compressed, significantly reducing disk space usage

## API

### POST /api/v1/provision
Start volume provisioning job.

**Behavior:**
- Downloads and caches QCOW2 images from MinIO with compression preservation
- Checksum-based cache ensures images are only downloaded once
- Creates or reuses compatible LVM volumes
- Converts cached QCOW2 images to raw format for final LVM volume population
- Provides progress tracking and error reporting
- Implements automatic rollback: cleans up partially created volumes on failure

**Request:**
```json
{
  "image_url": "https://minio.example.com/images/ubuntu-20.04.qcow2",
  "volume_name": "itx-master-controlplane-1",
  "volume_size_gb": 50,
  "image_type": "qcow2",
  "correlation_id": "optional-uuid"
}
```

**Response:**
```json
{
  "job_id": "550e8400-e29b-41d4-a716-446655440000"
}
```

**Volume Handling:**
- **New Volume**: Created if volume doesn't exist
- **Reuse**: Compatible existing volumes are reused (size validation ±5%)
- **Error**: Incompatible existing volumes cause job failure

### GET /api/v1/status/{job_id}
Get provisioning job status.

**Response:**
```json
{
  "job_id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "completed",
  "progress": {
    "stage": "finalizing",
    "percent": 100,
    "bytes_processed": 50000000000,
    "bytes_total": 50000000000
  },
  "correlation_id": "550e8400-e29b-41d4-a716-446655440000",
  "cache_hit": true,
  "image_path": "/var/lib/libvirt/images/base-standard.qcow2"
}
```

### DELETE /api/v1/cancel/{job_id}
Cancel a running provisioning job.

## Usage Examples

### Provisioning a new volume with cache

First provisioning request - image is downloaded, cached with compression preserved, then converted to raw and populated to the LVM volume:

```bash
curl -X POST https://hypervisor.example.com:8080/api/v1/provision \
  --cacert /path/to/ca.crt \
  --cert /path/to/client.crt \
  --key /path/to/client.key \
  -H "Content-Type: application/json" \
  -d '{
    "image_url": "https://minio.example.com/images/ubuntu-20.04.qcow2",
    "volume_name": "vm-root-disk-1",
    "volume_size_gb": 50,
    "image_type": "qcow2",
    "correlation_id": "provision-vm-001"
  }'
```

Response:
```json
{
  "job_id": "550e8400-e29b-41d4-a716-446655440000"
}
```

### Check provisioning progress

```bash
curl https://hypervisor.example.com:8080/api/v1/status/550e8400-e29b-41d4-a716-446655440000 \
  --cacert /path/to/ca.crt \
  --cert /path/to/client.crt \
  --key /path/to/client.key
```

Response while in progress:
```json
{
  "job_id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "running",
  "progress": {
    "stage": "downloading",
    "percent": 45,
    "bytes_processed": 22500000000,
    "bytes_total": 50000000000
  }
}
```

Response when complete:
```json
{
  "job_id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "completed",
  "progress": {
    "stage": "finalizing",
    "percent": 100,
    "bytes_processed": 50000000000,
    "bytes_total": 50000000000
  },
  "correlation_id": "provision-vm-001",
  "cache_hit": false,
  "image_path": "/var/lib/libvirt/images/ubuntu-20.04.qcow2"
}
```

### Provisioning another volume with same image (cache hit)

Second request for the same image uses cached version - much faster:

```bash
curl -X POST https://hypervisor.example.com:8080/api/v1/provision \
  --cacert /path/to/ca.crt \
  --cert /path/to/client.crt \
  --key /path/to/client.key \
  -H "Content-Type: application/json" \
  -d '{
    "image_url": "https://minio.example.com/images/ubuntu-20.04.qcow2",
    "volume_name": "vm-root-disk-2",
    "volume_size_gb": 50,
    "image_type": "qcow2",
    "correlation_id": "provision-vm-002"
  }'
```

Response when completed (note `cache_hit: true`):
```json
{
  "job_id": "660f9511-f30c-52e5-b827-557766551111",
  "status": "completed",
  "progress": {
    "stage": "finalizing",
    "percent": 100,
    "bytes_processed": 50000000000,
    "bytes_total": 50000000000
  },
  "correlation_id": "provision-vm-002",
  "cache_hit": true,
  "image_path": "/var/lib/libvirt/images/ubuntu-20.04.qcow2"
}
```

**Note:** The second request completes much faster due to cache hit, as the QCOW2 image is already cached in compressed format and doesn't need to be re-downloaded.

## Authentication

- **Primary**: X.509 client certificates (mutual TLS)
- **Fallback**: HMAC-SHA256 API tokens for simpler deployments

## Installation

The libvirt-volume-provisioner supports multiple deployment methods to suit different infrastructure preferences.

### Quick Start

#### Option 1: Debian Package (Recommended for Production)

```bash
# Download and install
wget https://github.com/rossigee/libvirt-volume-provisioner/releases/download/v0.3.0/libvirt-volume-provisioner_0.3.0_amd64.deb
sudo apt install ./libvirt-volume-provisioner_0.3.0_amd64.deb

# Configure (edit with your values)
sudo vi /etc/default/libvirt-volume-provisioner

# Start service
sudo systemctl enable libvirt-volume-provisioner.socket
sudo systemctl start libvirt-volume-provisioner.socket
```

#### Option 2: Docker Container

```bash
# Clone and setup
git clone https://github.com/rossigee/libvirt-volume-provisioner.git
cd libvirt-volume-provisioner

# Configure environment
cp .env.example .env
vi .env  # Add your MinIO/S3 credentials

# Start with Docker Compose
docker-compose up -d
```

#### Option 3: Build from Source

```bash
# Clone and build
git clone https://github.com/rossigee/libvirt-volume-provisioner.git
cd libvirt-volume-provisioner

# Build and install
make build-linux
sudo make install-systemd

# Configure and start
sudo vi /etc/default/libvirt-volume-provisioner
sudo systemctl enable libvirt-volume-provisioner.socket
sudo systemctl start libvirt-volume-provisioner.socket
```

### Deployment Methods

| Method | Use Case | Pros | Cons |
|--------|----------|------|------|
| **Systemd Service** | Production servers, bare metal | Native performance, full access to host resources | Requires root access for installation |
| **Docker Container** | Containerized infrastructure, development | Easy deployment, isolation | Requires privileged mode for libvirt/LVM access |
| **Binary Only** | Custom deployments, embedded systems | Maximum flexibility | Manual service management |

See [DEPLOYMENT.md](DEPLOYMENT.md) for comprehensive deployment instructions for each method.

2. **Install the package:**
    ```bash
    sudo apt install libvirt-volume-provisioner
    ```

### Option 2: Native Installation

1. **Add to hypervisor ISO:**
    ```bash
    # Include in server-kvm autoinstall
    packages:
      - libvirt-volume-provisioner
    ```

2. **Systemd service:**
    ```bash
    sudo systemctl enable libvirt-volume-provisioner
    sudo systemctl start libvirt-volume-provisioner
    ```

3. **Configuration:**
    ```bash
    # Environment variables
    export MINIO_ENDPOINT="https://minio.example.com"
    export MINIO_ACCESS_KEY="your-access-key"
    export MINIO_SECRET_KEY="your-secret-key"
    ```

### Option 2: Docker Installation

#### Using Pre-built Images (Recommended)

1. **Pull and run from GitHub Container Registry:**
    ```bash
    docker run -d \
      --name libvirt-volume-provisioner \
      --privileged \
      -v /var/run/libvirt:/var/run/libvirt:rw \
      -v /var/lib/libvirt/images:/var/lib/libvirt/images:rw \
      -v /dev/mapper:/dev/mapper:rw \
      -p 8080:8080 \
      -e MINIO_ENDPOINT=https://minio.example.com \
      -e MINIO_ACCESS_KEY=your-access-key \
      -e MINIO_SECRET_KEY=your-secret-key \
      -e LVM_VOLUME_GROUP=vg0 \
      ghcr.io/rossigee/libvirt-volume-provisioner:latest
    ```

    **Available Tags:**
    - `latest` - Latest production build
    - `v{X.Y.Z}` - Specific version releases
    - `dev` - Development builds
    - `{commit-sha}` - Specific commit builds

#### Building from Source

1. **Build the Docker image:**
    ```bash
    make build-docker
    ```

2. **Create environment file:**
    ```bash
    cat > .env << EOF
    MINIO_ENDPOINT=https://minio.example.com
    MINIO_ACCESS_KEY=your-access-key
    MINIO_SECRET_KEY=your-secret-key
    PORT=8080
    HOST=0.0.0.0
    EOF
    ```

3. **Run the container:**
    ```bash
    make docker-compose-up
    ```

### Option 3: Ubuntu .deb Package

1. **Build the .deb package:**
    ```bash
    make deb
    ```

2. **Install the package:**
    ```bash
    sudo dpkg -i libvirt-volume-provisioner_0.3.0_amd64.deb
    sudo apt-get install -f  # Install any missing dependencies
    ```

3. **Configure the service:**
    ```bash
    sudo systemctl edit libvirt-volume-provisioner
    # Add your environment variables in the [Service] section
    ```

4. **Start the service:**
    ```bash
    sudo systemctl enable libvirt-volume-provisioner
    sudo systemctl start libvirt-volume-provisioner
    ```

## CI/CD

### Continuous Integration

The project uses GitHub Actions for continuous integration:

- **CI Pipeline**: Runs on every push to main/master and pull requests
- **Tests**: Executes `make test` to run all Go tests
- **Linting**: Runs `golangci-lint` for code quality checks
- **Build**: Verifies the binary builds successfully

### Release Process

Tagged releases (e.g., `v0.1.0`) automatically trigger the release pipeline:

1. **Build Debian Package**: Creates a `.deb` package for Ubuntu/Debian systems
2. **Deploy to B2 Repository**: Uploads the `.deb` package to the internal B2-backed Debian repository at `https://debs.golder.tech`
3. **Build Docker Image**: Pushes to `ghcr.io/rossigee/libvirt-volume-provisioner:v0.1.0`
4. **Create GitHub Release**: Automatically creates a release with the `.deb` package as an asset

### Repository Setup

The project deploys to a B2-backed Debian repository with GPG signature verification.

#### For Repository Maintainers (CI/CD Setup):

1. **Create B2 Bucket**: Ensure `debs-golder-tech-static` bucket exists
2. **Configure Repository Structure**: The bucket should have `dists/` and `pool/` directories
3. **Set GitHub Secrets**:
   - `B2_KEY_ID`: Your B2 application key ID
   - `B2_APPLICATION_KEY`: Your B2 application key
   - `GPG_PRIVATE_KEY`: Your GPG private key (exported with `gpg --export-secret-keys --armor <key-id>`)

#### For Users (Installing from Repository):

See [REPOSITORY-README.md](REPOSITORY-README.md) for detailed setup instructions, or use the automated setup script:

```bash
# Download and run the setup script
curl -fsSL https://raw.githubusercontent.com/rossigee/libvirt-volume-provisioner/main/setup-repo.sh | sudo bash

# Or manually:
sudo curl -fsSL https://debs.golder.tech/gpg-key.asc | gpg --dearmor | sudo tee /usr/share/keyrings/golder-tech-archive-keyring.gpg > /dev/null
echo "deb [signed-by=/usr/share/keyrings/golder-tech-archive-keyring.gpg] https://debs.golder.tech stable main" | sudo tee /etc/apt/sources.list.d/golder-tech.list
sudo apt update
```

### Creating a Release

To create a new release:

```bash
# Tag the version
git tag v0.1.0
git push origin v0.1.0
```

The GitHub Actions workflow will automatically:
- Build and test the code
- Create the Debian package
- Update the B2 Debian repository with the new package
- Build and push the Docker image
- Create a GitHub release with the package

## Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `PORT` | HTTP server port | `8080` |
| `HOST` | HTTP server host | `0.0.0.0` |
| `MINIO_ENDPOINT` | MinIO server URL | `https://minio.example.com` |
| `MINIO_ACCESS_KEY` | MinIO access key | Required |
| `MINIO_SECRET_KEY` | MinIO secret key | Required |
| `CLIENT_CA_CERT` | Client CA certificate path | `/etc/ssl/certs/ca-certificates.crt` |
| `API_TOKENS_FILE` | API tokens file path | `/etc/libvirt-volume-provisioner/tokens` |

### Certificate Setup

For production deployments:

```bash
# Create client CA
openssl genrsa -out client-ca.key 4096
openssl req -new -x509 -days 365 -key client-ca.key -sha256 -out client-ca.crt

# Install on provisioner host
sudo cp client-ca.crt /etc/ssl/certs/ca-certificates.crt
sudo systemctl restart libvirt-volume-provisioner
```

### Monitoring Setup

#### Prometheus ServiceMonitor

For Kubernetes deployments with Prometheus Operator:

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
      insecureSkipVerify: true
    bearerTokenSecret:
      name: provisioner-api-tokens
      key: token
```

#### Alerting Rules

Key alerting rules for operational monitoring:

```yaml
# Service down alerts
- alert: VolumeProvisionerDown
  expr: up{job="libvirt-volume-provisioner"} == 0
  for: 5m

# Error rate monitoring
- alert: VolumeProvisionerHighErrorRate
  expr: rate(libvirt_volume_provisioner_requests_total{status=~"5.."}[5m]) > 0.1
  for: 10m

# Job failure alerts
- alert: VolumeProvisionerJobFailures
  expr: increase(libvirt_volume_provisioner_jobs_total{status="failed"}[10m]) > 5
  for: 5m
```

## Development

### Building

```bash
make build          # Build for current platform
make build-linux    # Build for Linux
make test           # Run tests
make lint           # Run linting
make clean          # Clean build artifacts

# Packaging
make deb            # Build Ubuntu .deb package

# Docker
make docker-build   # Build Docker image
make docker-run     # Run in Docker container
```

### Running Locally

```bash
# Set environment
export MINIO_ENDPOINT="https://minio.example.com"
export MINIO_ACCESS_KEY="..."
export MINIO_SECRET_KEY="..."

# Run service
make run
```

## Integration

### With infrastructure-builder

The provisioner integrates with the `infrastructure-builder` tool:

```bash
# Deploy VM (will call provisioner API)
./infrastructure-builder deploy -t vm -m base-standard -d itx-master-controlplane-1
```

### Monitoring

#### Health & Metrics

- **Health endpoints**:
  - `GET /health` - Basic health check
  - `GET /healthz` - Kubernetes health check (compatible)
  - `GET /livez` - Kubernetes liveness probe

- **Prometheus metrics** (`GET /metrics`):
  - `libvirt_volume_provisioner_requests_total` - HTTP request counts by endpoint/method/status
  - `libvirt_volume_provisioner_jobs_total` - Job counts by status (started, completed, failed)
  - `libvirt_volume_provisioner_active_jobs` - Currently active provisioning jobs
  - Go runtime metrics (GC, goroutines, memory usage)

#### Alerting

Comprehensive alerting rules are configured for:

- **Service Health**: Alerts when provisioner service is down
- **Error Rates**: Triggers on high 5xx error rates (>10% over 5 minutes)
- **Job Failures**: Alerts on multiple failed provisioning jobs (>5 failures in 10 minutes)
- **Performance**: Warns on high active job counts (>10 concurrent jobs)

#### Logging

- **Structured logging**: JSON format with correlation IDs for request tracing
- **Log levels**: Configurable (debug, info, warn, error)
- **Integration**: Compatible with Loki/Promtail for centralized aggregation

#### Service Monitoring

- **Prometheus ServiceMonitor**: Automatic metrics collection from all provisioner instances
- **Grafana dashboards**: Visualization panels for key metrics and performance indicators
- **Distributed tracing**: OpenTelemetry integration for request tracing across services

## Security

- **Mutual TLS**: Required for production deployments
- **API token fallback**: For development/testing
- **Resource limits**: CPU and memory constraints per operation
- **Audit logging**: All operations logged with actor identification

## Performance

- **Concurrent operations**: Maximum 2 simultaneous provisions per host
- **Progress tracking**: Real-time updates during long operations
- **Resource monitoring**: Automatic cleanup and leak prevention

## Troubleshooting

### Common Issues

1. **MinIO connection fails**
   - Check `MINIO_ENDPOINT` URL
   - Verify access credentials
   - Check network connectivity

2. **LVM operations fail**
   - Verify `lvcreate`/`qemu-img` commands available
   - Check LVM volume group exists
   - Verify sufficient disk space

3. **Certificate errors**
   - Ensure client CA certificate installed
   - Check certificate validity
   - Verify TLS configuration

### Logs

```bash
# Service logs
sudo journalctl -u libvirt-volume-provisioner -f

# Check service status
sudo systemctl status libvirt-volume-provisioner
```

## License

This project is part of the VM platform infrastructure tools.