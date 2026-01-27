# libvirt-volume-provisioner

A daemon service for provisioning LVM volumes with VM images on libvirt hypervisor hosts.

## Overview

The `libvirt-volume-provisioner` runs as a systemd service on hypervisor hosts and provides an HTTP API for:

- Downloading VM images from MinIO object storage with intelligent checksum-based caching
- Caching images with compression preservation to reduce disk space usage
- Converting cached QCOW2 images to raw format for LVM volume population
- Populating LVM volumes with VM disk data
- Progress tracking and error reporting

**Key Features:**

✅ Compression-preserving image caching (50-70% space savings)
✅ Checksum-based cache invalidation
✅ Mutual TLS authentication for production deployments
✅ RESTful HTTP API with progress tracking
✅ Prometheus metrics and health checks
✅ Automatic rollback on provisioning failure
✅ Multi-deployment support (Systemd, Docker, .deb)

## Quick Start

### Debian Package (Recommended)

```bash
wget https://github.com/rossigee/libvirt-volume-provisioner/releases/download/v0.3.0/libvirt-volume-provisioner_0.3.0_amd64.deb
sudo apt install ./libvirt-volume-provisioner_0.3.0_amd64.deb
sudo systemctl enable --now libvirt-volume-provisioner.socket
```

### Docker

```bash
docker run -d \
  --privileged \
  -v /var/run/libvirt:/var/run/libvirt:rw \
  -v /dev/mapper:/dev/mapper:rw \
  -p 8080:8080 \
  -e MINIO_ENDPOINT=https://minio.example.com \
  -e MINIO_ACCESS_KEY=your-access-key \
  -e MINIO_SECRET_KEY=your-secret-key \
  ghcr.io/rossigee/libvirt-volume-provisioner:latest
```

### Build from Source

```bash
git clone https://github.com/rossigee/libvirt-volume-provisioner.git
cd libvirt-volume-provisioner
make build-linux
sudo make install-systemd
```

See [Installation](./docs/installation.md) for detailed instructions.

## Documentation

| Topic | Description |
|-------|-------------|
| [Architecture](./docs/architecture.md) | System design, workflow, and caching strategy |
| [API Reference](./docs/api-reference.md) | Complete HTTP API documentation |
| [Usage Examples](./docs/usage-examples.md) | Practical curl examples and integration patterns |
| [Installation](./docs/installation.md) | Installation methods and deployment options |
| [Configuration](./docs/configuration.md) | Environment variables and service setup |
| [Authentication](./docs/authentication.md) | TLS certificates and API token setup |
| [Monitoring](./docs/monitoring.md) | Prometheus metrics, alerting, and logging |
| [Integration](./docs/integration.md) | Integration with infrastructure-builder, Ansible, Terraform |
| [Development](./docs/development.md) | Building, testing, and contributing |
| [Deployment](./docs/deployment.md) | CI/CD pipelines, release process, and strategies |
| [Troubleshooting](./docs/troubleshooting.md) | Common issues and solutions |
| [Security](./docs/security.md) | Security considerations and best practices |

## Key Concepts

### Image Immutability
Base images in MinIO never change; reprovisioning gets fresh copy.

### Idempotent Provisioning
Cloud-init ensures VM reaches desired state regardless of history.

### Compression Preservation
QCOW2 images are cached in compressed format, not expanded to raw. This results in 50-70% storage savings.

### Checksum-Based Caching
Uses SHA256 checksums from MinIO `.sha256` files as cache keys for reliable cache invalidation.

## API Overview

### Provision Volume

```bash
curl -X POST https://hypervisor.example.com:8080/api/v1/provision \
  --cert client.crt --key client.key --cacert ca.crt \
  -H "Content-Type: application/json" \
  -d '{
    "image_url": "https://minio.example.com/images/ubuntu-20.04.qcow2",
    "volume_name": "vm-root-disk",
    "volume_size_gb": 50,
    "image_type": "qcow2"
  }'
```

### Check Status

```bash
curl https://hypervisor.example.com:8080/api/v1/status/{job_id} \
  --cert client.crt --key client.key --cacert ca.crt
```

See [API Reference](./docs/api-reference.md) for complete documentation.

## Configuration

Configure via environment variables:

| Variable | Example |
|----------|---------|
| `MINIO_ENDPOINT` | `https://minio.example.com` |
| `MINIO_ACCESS_KEY` | Your MinIO access key |
| `MINIO_SECRET_KEY` | Your MinIO secret key |
| `LVM_VOLUME_GROUP` | `data` |
| `PORT` | `8080` |

See [Configuration](./docs/configuration.md) for all options.

## Monitoring

- **Health Endpoints**: `/health`, `/healthz`, `/livez`
- **Metrics**: Prometheus-compatible at `/metrics`
- **Logging**: Structured JSON logs via systemd journal

See [Monitoring](./docs/monitoring.md) for detailed setup.

## Security

- **Mutual TLS**: Recommended for production
- **API Tokens**: For development/testing
- **Input Validation**: All requests validated
- **Audit Logging**: All operations logged

See [Security](./docs/security.md) for best practices.

## Performance

- **Concurrent Operations**: Max 2 concurrent provisions per host
- **Cache Hit Performance**: 50-70% faster than first download
- **Storage Efficiency**: 50-70% space savings with compressed images

## Support

For issues or questions:

1. Check [Troubleshooting](./docs/troubleshooting.md)
2. Review [API Reference](./docs/api-reference.md)
3. See [Configuration](./docs/configuration.md)
4. Open an issue on [GitHub](https://github.com/rossigee/libvirt-volume-provisioner/issues)

## License

This project is licensed under the MIT License.

