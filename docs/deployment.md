# Deployment

This guide covers continuous integration, release processes, and deployment strategies.

## Continuous Integration

The project uses GitHub Actions for automated CI/CD pipelines.

### CI Pipeline

Runs on every push and pull request:

- **Build**: Compiles binary for multiple platforms
- **Tests**: Executes `make test` (requires >80% coverage)
- **Linting**: Runs `golangci-lint` for code quality
- **Security**: Scans for vulnerabilities

### GitHub Actions Workflow

Located in `.github/workflows/ci.yml`:

```yaml
name: CI
on:
  push:
    branches: [master]
  pull_request:
    branches: [master]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
        with:
          go-version: '1.21'
      - run: make test
      - run: make lint
```

## Release Process

### Creating a Release

Releases are triggered by git tags:

```bash
# Update version in Makefile
vi Makefile  # DEB_VERSION = 0.3.0

# Commit version update
git add Makefile
git commit -m "release: bump version to v0.3.0"

# Create annotated tag
git tag -a v0.3.0 -m "Release v0.3.0"

# Push tag to trigger release workflow
git push origin v0.3.0
```

### Automated Release Pipeline

When a tag is created, GitHub Actions automatically:

1. **Build Debian Package**: Creates `.deb` for Ubuntu/Debian
2. **Build Docker Image**: Pushes to `ghcr.io/rossigee/libvirt-volume-provisioner:v0.3.0`
3. **Update Debian Repository**: Uploads `.deb` to B2-backed repository
4. **Create GitHub Release**: Creates release with `.deb` as asset
5. **Generate Changelog**: Auto-generates changelog from commit messages

### Release Checklist

- [ ] Update version in Makefile (`DEB_VERSION`)
- [ ] Update CHANGELOG.md with new features/fixes
- [ ] Update README.md if needed
- [ ] Run tests: `make test`
- [ ] Run linting: `make lint`
- [ ] Create annotated git tag: `git tag -a v0.3.0 -m "Release v0.3.0"`
- [ ] Push tag: `git push origin v0.3.0`
- [ ] Verify GitHub release was created
- [ ] Verify Docker image was pushed
- [ ] Verify Debian package in repository
- [ ] Update documentation if needed

## Deployment Strategies

### Blue-Green Deployment

For zero-downtime updates:

```bash
# Run new version on different port
docker run -d \
  --name libvirt-provisioner-new \
  -p 8081:8080 \
  ghcr.io/rossigee/libvirt-volume-provisioner:v0.3.0

# Test new version
curl http://localhost:8081/health

# Switch traffic (via load balancer)
# Update load balancer to point 8080 → 8081

# Stop old version
docker stop libvirt-provisioner-old
docker rm libvirt-provisioner-old

# Rename new version
docker rename libvirt-provisioner-new libvirt-provisioner
```

### Rolling Deployment

Update multiple hosts sequentially:

```bash
#!/bin/bash

HOSTS=("hypervisor1.example.com" "hypervisor2.example.com" "hypervisor3.example.com")
NEW_VERSION="v0.3.0"

for host in "${HOSTS[@]}"; do
  echo "Deploying to $host..."

  # SSH to host and update
  ssh "ubuntu@$host" << ENDSSH
    # Stop current version
    sudo systemctl stop libvirt-volume-provisioner

    # Download new package
    wget https://github.com/rossigee/libvirt-volume-provisioner/releases/download/${NEW_VERSION}/libvirt-volume-provisioner_${NEW_VERSION}_amd64.deb

    # Install new version
    sudo dpkg -i libvirt-volume-provisioner_${NEW_VERSION}_amd64.deb

    # Start new version
    sudo systemctl start libvirt-volume-provisioner

    # Wait for health check
    for i in {1..30}; do
      if sudo systemctl is-active --quiet libvirt-volume-provisioner; then
        echo "Service started"
        break
      fi
      sleep 2
    done
ENDSSH

  # Wait before next host
  sleep 60
done

echo "Deployment complete"
```

### Canary Deployment

Route percentage of traffic to new version:

```yaml
apiVersion: networking.istio.io/v1alpha3
kind: VirtualService
metadata:
  name: libvirt-provisioner
spec:
  hosts:
  - provisioner.example.com
  http:
  - match:
    - uri:
        prefix: /api
    route:
    - destination:
        host: libvirt-provisioner-v0.2.7
        port:
          number: 8080
      weight: 90
    - destination:
        host: libvirt-provisioner-v0.3.0
        port:
          number: 8080
      weight: 10
```

## Monitoring Deployments

### Health Checks During Deployment

```bash
#!/bin/bash

PROVISIONER_URL="https://hypervisor.example.com:8080"

# Wait for service to be healthy
for i in {1..30}; do
  if curl -s "${PROVISIONER_URL}/health" | grep -q healthy; then
    echo "Service is healthy"
    exit 0
  fi
  echo "Waiting for service... ($i/30)"
  sleep 5
done

echo "Service failed to become healthy"
exit 1
```

### Smoke Tests

```bash
#!/bin/bash

PROVISIONER_URL="https://hypervisor.example.com:8080"

# Test 1: Health check
echo "Testing health check..."
curl -f "${PROVISIONER_URL}/health" || exit 1

# Test 2: Metrics endpoint
echo "Testing metrics..."
curl -f "${PROVISIONER_URL}/metrics" || exit 1

# Test 3: Provision API (dry-run)
echo "Testing provision API..."
curl -f -X POST "${PROVISIONER_URL}/api/v1/provision" \
  -H "Content-Type: application/json" \
  -d '{
    "image_url": "https://minio.example.com/images/test.qcow2",
    "volume_name": "test-volume",
    "volume_size_gb": 10,
    "image_type": "qcow2"
  }' || exit 1

echo "All smoke tests passed"
```

## Rollback Procedures

### Rollback from Systemd

```bash
# Stop current version
sudo systemctl stop libvirt-volume-provisioner

# Downgrade to previous version
sudo apt-get install libvirt-volume-provisioner=0.2.7

# Start previous version
sudo systemctl start libvirt-volume-provisioner

# Verify
sudo systemctl status libvirt-volume-provisioner
```

### Rollback from Docker

```bash
# Stop new version
docker stop libvirt-provisioner

# Start previous version
docker run -d \
  --name libvirt-provisioner \
  ghcr.io/rossigee/libvirt-volume-provisioner:v0.2.7 \
  # ... other options ...
```

## Repository Management

### Debian Repository Setup

Packages are automatically deployed to B2-backed Debian repository at `https://debs.golder.tech`.

#### For Repository Maintainers:

Configure GitHub Secrets:
- `B2_KEY_ID`: B2 application key ID
- `B2_APPLICATION_KEY`: B2 application key
- `GPG_PRIVATE_KEY`: GPG private key (export with `gpg --export-secret-keys --armor <key-id>`)

The repository requires:
- B2 bucket: `debs-golder-tech-static`
- Structure: `dists/` and `pool/` directories
- GPG signature verification enabled

#### For Users:

```bash
# Add repository
curl -fsSL https://raw.githubusercontent.com/rossigee/libvirt-volume-provisioner/main/setup-repo.sh | sudo bash

# Or manually:
sudo curl -fsSL https://debs.golder.tech/gpg-key.asc | gpg --dearmor | \
  sudo tee /usr/share/keyrings/golder-tech-archive-keyring.gpg > /dev/null
echo "deb [signed-by=/usr/share/keyrings/golder-tech-archive-keyring.gpg] https://debs.golder.tech stable main" | \
  sudo tee /etc/apt/sources.list.d/golder-tech.list
sudo apt update

# Install or upgrade
sudo apt install libvirt-volume-provisioner
```

