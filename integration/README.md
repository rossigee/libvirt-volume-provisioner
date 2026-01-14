# Integration Tests

This directory contains comprehensive integration tests for the libvirt-volume-provisioner that validate end-to-end functionality using real services.

## Overview

The integration test suite includes:

- **Full workflow testing** - Complete volume provisioning from MinIO to LVM
- **Caching validation** - Ensures images are properly cached and reused
- **Error scenario testing** - Validates failure handling and recovery
- **Performance benchmarking** - Measures provisioning times and efficiency
- **Chaos engineering** - Tests system resilience under adverse conditions
- **Concurrency testing** - Validates behavior under high load

## Prerequisites

- Docker and Docker Compose
- At least 8GB RAM available for test containers
- 20GB free disk space for test data

## Quick Start

```bash
# Start the test environment
make integration-up

# Wait for services to be healthy (check logs)
make integration-logs

# Run the integration tests
make integration-test

# Clean up when done
make integration-clean
```

## Test Environment

The test suite spins up a complete environment with:

- **MinIO** - S3-compatible object storage for test images
- **PostgreSQL** - Database for job persistence (more realistic than SQLite)
- **Redis** - Optional caching layer for performance testing
- **libvirt** - VM management daemon for LVM operations
- **Test Runner** - Containerized test execution environment

### Test Data

The tests automatically generate and upload test QCOW2 images of various sizes (100MB, 500MB, 1GB) to MinIO for realistic testing scenarios.

## Test Categories

### Core Functionality Tests

- **FullProvisioningWorkflow** - End-to-end volume creation
- **ImageCaching** - Cache hit/miss validation
- **ErrorScenarios** - Failure mode testing

### Performance Tests

- **Performance** - Cold start vs cached provisioning benchmarks
- **ConcurrentRequests** - Multi-request handling under load
- **RateLimiting** - System behavior under rapid requests

### Chaos Engineering Tests

- **NetworkInterruption** - Network failure simulation
- **DiskFull** - Storage exhaustion testing
- **ServiceRestart** - Process restart resilience
- **ResourceCleanup** - Proper cleanup after failures

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `TEST_MINIO_ENDPOINT` | `http://minio:9000` | MinIO server URL |
| `TEST_MINIO_ACCESS_KEY` | `testminio` | MinIO access key |
| `TEST_MINIO_SECRET_KEY` | `testminio123` | MinIO secret key |
| `TEST_POSTGRES_DSN` | `postgres://testuser:testpass@postgres:5432/libvirt_test` | PostgreSQL connection |
| `TEST_REDIS_URL` | `redis://redis:6379` | Redis connection URL |
| `TEST_LIBVIRT_URI` | `qemu:///system` | libvirt connection URI |
| `TEST_LVM_VG` | `testvg` | LVM volume group for testing |
| `TEST_PROVISIONER_URL` | `http://localhost:8080` | Provisioner API endpoint |

### Custom Configuration

Create a `.env` file in the integration directory:

```bash
# Custom MinIO instance
TEST_MINIO_ENDPOINT=https://minio.example.com
TEST_MINIO_ACCESS_KEY=your-key
TEST_MINIO_SECRET_KEY=your-secret

# Custom volume group
TEST_LVM_VG=my-test-vg
```

## Running Specific Tests

```bash
# Run only core functionality tests
go test -tags=integration -run TestIntegrationSuite ./integration

# Run only chaos tests
go test -tags=integration -run TestChaosSuite ./integration

# Run with verbose output
go test -tags=integration -v ./integration

# Run with race detection
go test -tags=integration -race ./integration
```

## Debugging

### View Service Logs

```bash
# All services
make integration-logs

# Specific service
cd integration && docker-compose -f docker-compose.test.yml logs minio
cd integration && docker-compose -f docker-compose.test.yml logs postgres
```

### Access Services Directly

```bash
# MinIO web interface
open http://localhost:9001

# PostgreSQL
docker exec -it test-postgres psql -U testuser -d libvirt_test

# Redis
docker exec -it test-redis redis-cli
```

### Test Debugging

```bash
# Run tests with debugging
cd integration && docker-compose -f docker-compose.test.yml run --rm integration-tests /usr/local/bin/integration.test -test.v -test.run TestFullProvisioningWorkflow

# Keep containers running after test failure
cd integration && docker-compose -f docker-compose.test.yml run --rm integration-tests bash
```

## Performance Benchmarks

The test suite includes automated performance measurements:

- **Cold start time** - First provisioning of an image
- **Cache hit time** - Subsequent provisioning of cached images
- **Concurrent throughput** - Multiple simultaneous requests
- **Resource usage** - Memory and CPU consumption

### Expected Performance

- Cold start: 2-5 minutes (depending on image size)
- Cache hit: < 30 seconds
- Concurrent requests: Linear scaling up to system limits

## Troubleshooting

### Common Issues

#### Services Not Starting
```bash
# Check service health
cd integration && docker-compose -f docker-compose.test.yml ps

# Restart specific service
cd integration && docker-compose -f docker-compose.test.yml restart minio
```

#### Out of Disk Space
```bash
# Clean up Docker resources
docker system prune -a --volumes

# Check disk usage
df -h
```

#### Permission Issues
```bash
# Ensure Docker can access host libvirt
sudo usermod -a -G libvirt $USER
newgrp libvirt
```

#### Test Timeouts
```bash
# Increase timeout for large images
export TEST_TIMEOUT=15m
```

### Logs and Diagnostics

```bash
# Detailed test output
make integration-test 2>&1 | tee test-output.log

# Service health checks
curl http://localhost:9000/minio/health/live
docker exec test-postgres pg_isready -U testuser
docker exec test-redis redis-cli ping
```

## CI/CD Integration

The integration tests are designed to run in CI/CD pipelines:

```yaml
# GitHub Actions example
- name: Run Integration Tests
  run: |
    make integration-up
    timeout 600 make integration-test || (make integration-logs && exit 1)
    make integration-down
```

## Contributing

When adding new integration tests:

1. Use the existing test suite structure
2. Include proper setup and teardown
3. Add appropriate timeouts
4. Document any new environment requirements
5. Update this README with new test descriptions

## Support

For issues with integration tests:

1. Check the service logs: `make integration-logs`
2. Verify environment variables
3. Ensure sufficient system resources
4. Check Docker daemon status
5. Review test output for specific error messages