# Installation

The libvirt-volume-provisioner supports multiple deployment methods to suit different infrastructure preferences.

## Quick Start

### Option 1: Debian Package (Recommended for Production)

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

### Option 2: Docker Container

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

### Option 3: Build from Source

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

## Deployment Methods Comparison

| Method | Use Case | Pros | Cons |
|--------|----------|------|------|
| **Systemd Service** | Production servers, bare metal | Native performance, full access to host resources | Requires root access for installation |
| **Docker Container** | Containerized infrastructure, development | Easy deployment, isolation | Requires privileged mode for libvirt/LVM access |
| **Binary Only** | Custom deployments, embedded systems | Maximum flexibility | Manual service management |

## Native Installation (Bare Metal/Hypervisor)

### 1. Add to Hypervisor ISO

For automated deployments, include in your server autoinstall configuration:

```yaml
packages:
  - libvirt-volume-provisioner
```

### 2. Enable Systemd Service

```bash
sudo systemctl enable libvirt-volume-provisioner
sudo systemctl start libvirt-volume-provisioner
```

### 3. Configure Environment

Set required environment variables:

```bash
export MINIO_ENDPOINT="https://minio.example.com"
export MINIO_ACCESS_KEY="your-access-key"
export MINIO_SECRET_KEY="your-secret-key"
export LVM_VOLUME_GROUP="data"
```

See [Configuration](./configuration.md) for all available options.

## Docker Installation

### Using Pre-built Images (Recommended)

Pull and run from GitHub Container Registry:

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
- `v0.3.0` - Specific version releases
- `dev` - Development builds
- `{commit-sha}` - Specific commit builds

### Building Docker Image from Source

```bash
# Build the Docker image
make build-docker

# Create environment file
cat > .env << EOF
MINIO_ENDPOINT=https://minio.example.com
MINIO_ACCESS_KEY=your-access-key
MINIO_SECRET_KEY=your-secret-key
PORT=8080
HOST=0.0.0.0
EOF

# Run the container
make docker-compose-up
```

### Docker Compose Example

```yaml
version: '3.8'

services:
  libvirt-volume-provisioner:
    image: ghcr.io/rossigee/libvirt-volume-provisioner:latest
    container_name: libvirt-provisioner
    privileged: true
    volumes:
      - /var/run/libvirt:/var/run/libvirt:rw
      - /var/lib/libvirt/images:/var/lib/libvirt/images:rw
      - /dev/mapper:/dev/mapper:rw
    ports:
      - "8080:8080"
    environment:
      - MINIO_ENDPOINT=https://minio.example.com
      - MINIO_ACCESS_KEY=${MINIO_ACCESS_KEY}
      - MINIO_SECRET_KEY=${MINIO_SECRET_KEY}
      - LVM_VOLUME_GROUP=vg0
      - PORT=8080
    restart: unless-stopped
```

## Ubuntu .deb Package Installation

### Building the Package

```bash
# Build the .deb package
make deb
```

### Installing the Package

```bash
# Install the package
sudo dpkg -i libvirt-volume-provisioner_0.3.0_amd64.deb
sudo apt-get install -f  # Install any missing dependencies
```

### Configuring the Service

```bash
# Edit service configuration
sudo systemctl edit libvirt-volume-provisioner
```

Add your environment variables in the `[Service]` section:

```ini
[Service]
Environment="MINIO_ENDPOINT=https://minio.example.com"
Environment="MINIO_ACCESS_KEY=your-access-key"
Environment="MINIO_SECRET_KEY=your-secret-key"
Environment="LVM_VOLUME_GROUP=data"
```

### Starting the Service

```bash
sudo systemctl enable libvirt-volume-provisioner
sudo systemctl start libvirt-volume-provisioner
```

## Repository Installation

For easier updates, you can add the Debian repository:

```bash
# Download and run the setup script
curl -fsSL https://raw.githubusercontent.com/rossigee/libvirt-volume-provisioner/main/setup-repo.sh | sudo bash

# Or manually:
sudo curl -fsSL https://debs.golder.tech/gpg-key.asc | gpg --dearmor | sudo tee /usr/share/keyrings/golder-tech-archive-keyring.gpg > /dev/null
echo "deb [signed-by=/usr/share/keyrings/golder-tech-archive-keyring.gpg] https://debs.golder.tech stable main" | sudo tee /etc/apt/sources.list.d/golder-tech.list
sudo apt update

# Now install/update from repository
sudo apt install libvirt-volume-provisioner
```

## Verification

After installation, verify the service is running:

```bash
# Check service status
sudo systemctl status libvirt-volume-provisioner

# Check if listening on port 8080
ss -tlnp | grep 8080

# Test health endpoint
curl https://hypervisor.example.com:8080/health \
  --insecure  # For testing only; use proper certificates in production
```

