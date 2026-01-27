# Development

This guide covers building, testing, and contributing to the libvirt-volume-provisioner.

## Building

### Prerequisites

- Go 1.21 or higher
- Make
- libvirt-dev headers
- qemu-img

### Building the Binary

```bash
# Build for current platform
make build

# Build for Linux
make build-linux

# Build and install
sudo make install-systemd
```

### Building Docker Image

```bash
# Development image
make build-docker-dev

# Production image
make build-docker
```

### Building Debian Package

```bash
# Build .deb package
make deb

# Install locally
sudo dpkg -i libvirt-volume-provisioner_0.3.0_amd64.deb
```

## Testing

### Running Tests

```bash
# Run all tests
make test

# Run tests with verbose output
go test -v ./...

# Run tests with coverage
go test -cover ./...

# Run specific test
go test -run TestAllocateImageFile ./...
```

### Test Coverage

```bash
# Generate coverage report
go test -coverprofile=coverage.out ./...

# View coverage in browser (requires go-tool-cover)
go tool cover -html=coverage.out
```

## Linting and Code Quality

### Running Linting

```bash
# Run all linters
make lint

# Individual linters
golangci-lint run
go vet ./...
go fmt ./...
```

### Fixing Lint Issues

```bash
# Auto-fix formatting
go fmt ./...

# Auto-fix imports
goimports -w .
```

## Running Locally

### Development Setup

```bash
# Clone repository
git clone https://github.com/rossigee/libvirt-volume-provisioner.git
cd libvirt-volume-provisioner

# Install dependencies
make deps

# Set environment variables
export MINIO_ENDPOINT="http://localhost:9000"
export MINIO_ACCESS_KEY="minioadmin"
export MINIO_SECRET_KEY="minioadmin"
export MINIO_USE_SSL="false"
export LVM_VOLUME_GROUP="vg0"
export LOG_LEVEL="debug"
export LOG_FORMAT="text"

# Run service
make run
```

### Testing with Local MinIO

```bash
# Start MinIO in Docker
docker run -d \
  --name minio \
  -p 9000:9000 \
  -e MINIO_ROOT_USER=minioadmin \
  -e MINIO_ROOT_PASSWORD=minioadmin \
  minio/minio server /data

# Create test bucket
mc mb local/vm-images

# Upload test image
mc cp ubuntu-20.04.qcow2 local/vm-images/
```

## Project Structure

```
.
├── cmd/
│   └── provisioner/          # Main entry point
├── internal/
│   ├── api/                  # HTTP API handlers
│   ├── auth/                 # Authentication
│   ├── jobs/                 # Job management
│   ├── libvirt/              # libvirt integration
│   ├── lvm/                  # LVM operations
│   ├── minio/                # MinIO client
│   ├── storage/              # Job database
│   └── retry/                # Retry logic
├── pkg/
│   └── types/                # Shared types
├── docs/                     # Documentation
├── integration/              # Integration tests
├── Makefile                  # Build targets
└── README.md                 # Main documentation
```

## Contributing

### Code Style

- Follow Go conventions
- Use `go fmt` for formatting
- Add comments for exported functions
- Write descriptive commit messages
- Keep lines under 100 characters

### Testing Requirements

- Aim for >80% code coverage
- Test happy path and error paths
- Use table-driven tests for multiple scenarios
- Mock external dependencies (MinIO, libvirt, LVM)

### Commit Messages

Use semantic commit format:

```
type(scope): short description

Longer description if needed.

Fixes: #123
```

Types:
- `feat`: New feature
- `fix`: Bug fix
- `test`: Test-related changes
- `docs`: Documentation changes
- `refactor`: Code refactoring
- `perf`: Performance improvements
- `chore`: Build, CI, dependency updates

### Pull Request Process

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Make your changes
4. Run tests: `make test`
5. Run linting: `make lint`
6. Commit with semantic messages
7. Push to your fork
8. Create a Pull Request with description
9. Ensure CI checks pass
10. Request review from maintainers

## Debug Build

Build with debug symbols:

```bash
# Build with debug info (larger binary)
go build -o libvirt-volume-provisioner -v \
  -ldflags "-X main.version=dev" \
  ./cmd/provisioner

# Strip debug symbols (smaller binary)
strip libvirt-volume-provisioner
```

## Profiling

### CPU Profiling

```bash
# Enable CPU profiling
GODEBUG=http/2=0 pprof \
  http://localhost:8080/debug/pprof/profile?seconds=30

# Analyze results
go tool pprof /tmp/profile.pb.gz
```

### Memory Profiling

```bash
# Get heap profile
curl http://localhost:8080/debug/pprof/heap > heap.pb.gz

# Analyze memory usage
go tool pprof heap.pb.gz
```

## Release Process

See [Deployment](./deployment.md) for release procedures.

