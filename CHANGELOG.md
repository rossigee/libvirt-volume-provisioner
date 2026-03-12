# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.5.3] - 2026-03-12

### Fixed
- **Image Cache**: Fixed cache key mismatch that caused images to be redownloaded on every provision request. The `getOrDownloadImage` function now consistently uses the SHA256 checksum as the cache key for both cache lookup and storage.

### Changed
- **golangci-lint**: Updated to v2.11.3

## [0.5.2] - 2026-02-27

### Fixed
- **OTLP Endpoint Parsing**: Fixed gRPC exporter "too many colons in address" error by parsing full URLs and extracting host:port
- **Progress Percentages**: Corrected provisioning progress calculation to advance properly (download: 10-40%, create: 45%, convert: 55-95%)
- **Context Handling**: Resolved contextcheck lint errors by updating CancelJob method signatures and implementations

## [0.5.1] - 2026-02-16

### Fixed
- **CHANGELOG**: Added documentation for v0.5.1 release

## [0.5.0] - 2026-02-12

### Added
- **Production Readiness**: Comprehensive systemd service hardening with security best practices
- **Database Backup**: Automated daily database backups with systemd timer and retention policy
- **Enhanced Testing**: Expanded test coverage from 27.4% to 41.6% with 9 new test cases
- **OpenTelemetry**: Fixed tracing configuration (gRPC port 4317) and added connection validation
- **Docker Host Image**: Ubuntu 24.04 cloud image prepared for Docker host provisioning
- **Repository Security**: Complete removal of sensitive files from git history (devices.json, binaries, artifacts)

### Changed
- **Database Configuration**: Fixed environment variable mismatch (DB_PATH vs DATABASE_PATH)
- **MinIO TLS**: Added InsecureSkipVerify support for self-signed certificates
- **Makefile**: Enhanced Debian package building with security-hardened systemd service
- **CI/CD**: Improved build reliability and Docker image publishing to GHCR

### Fixed
- **Critical Bug**: Database initialization failure due to incorrect environment variable
- **Test Panics**: Resolved nil pointer dereferences in test environment detection
- **Git History**: Cleaned repository of accidentally committed binaries, coverage reports, and sensitive data
- **OTLP Tracing**: Fixed gRPC endpoint configuration preventing trace export
- **Linting**: Resolved gosec G402 TLS warning and line length violations

### Security
- **Systemd Hardening**: Added NoNewPrivileges, PrivateTmp, ProtectHome, ProtectSystem, and ReadWritePaths restrictions
- **Data Protection**: Removed sensitive infrastructure data (devices.json) from repository history
- **Binary Security**: Eliminated committed binaries that could contain sensitive build information
- **TLS Security**: Proper handling of self-signed certificates with security annotations

### Developer Experience
- **Test Quality**: Significant improvement in test coverage and reliability
- **Repository Health**: Clean git history with only appropriate files tracked
- **Build Reliability**: Fixed CI/CD pipeline issues and improved error handling

## [0.4.0] - 2026-02-11

### Added
- OpenTelemetry integration for distributed tracing with gRPC export
- Comprehensive logging with trace correlation using Logrus hooks
- Docker containerization with multi-stage builds and security hardening
- GitHub Actions CI/CD pipeline with automated testing and Docker publishing
- GitHub Container Registry (GHCR) integration for container distribution

### Changed
- Enhanced API error handling with structured error responses
- Improved database schema with proper migrations and error handling
- Modernized Go module dependencies and build process
- Refactored job processing with improved concurrency and error recovery

### Fixed
- Race conditions in job processing and database operations
- Memory leaks in long-running job operations
- API response inconsistencies and missing error details
- Docker build issues with libvirt dependencies

### Security
- Added request validation and input sanitization
- Implemented proper authentication token handling
- Enhanced TLS certificate validation
- Added security headers and CORS protection

## [0.3.0] - 2026-01-27

### Added
- New file-based image caching system that preserves QCOW2 compression, replacing libvirt RAW volume allocation
- `AllocateImageFile()` method for allocating compressed image cache paths without converting to RAW format
- Comprehensive test suite with 25 unit tests covering all cache operations and error paths
- Enhanced README with "Bigger Picture" section explaining the complete VM deployment workflow with diagrams
- Integer overflow validation in `CheckCache()` for secure file size conversions
- Security hardening of directory permissions (0o750) and file permissions (0o600)

### Changed
- **BREAKING**: Image caching now stores QCOW2 images in compressed format instead of uncompressed RAW volumes
- Refactored `CheckCache()` to use direct filesystem lookups via checksum files instead of libvirt volume queries
- Updated `getOrDownloadImage()` in job manager to use file-based caching for better compression handling
- Improved cache directory creation and error handling with early directory initialization
- Enhanced documentation with compression preservation details and deployment workflow context

### Fixed
- Storage space efficiency: Compressed QCOW2 images now remain compressed in cache (was being expanded to RAW)
- Integer overflow vulnerability when converting file sizes in `CheckCache()` (G115 gosec)
- Directory permissions too permissive (0755 → 0o750) for security hardening
- File permissions in tests too permissive (0644 → 0o600)
- gosec G304 file inclusion vulnerability with proper nolint directives
- gci import formatting issues throughout codebase

### Security
- Added explicit validation for negative file sizes before uint64 conversion
- Hardened directory permissions for cache directories (0o750)
- Hardened file permissions for sensitive files (0o600)
- Validated file inclusion paths in tests with security-conscious nolint directives

## [0.2.7] - 2026-01-24

### Added
- Expanded monitoring and alerting documentation
- Improved security in systemd service configuration with enhanced LVM access controls

### Changed
- Updated systemd service with security best practices
- Enhanced code quality and testing infrastructure

### Fixed
- Code quality improvements and linting

## [0.2.6] - 2026-01-10

### Fixed
- Resolved lint errors in TLS certificate tests

## [0.2.5] - 2026-01-05

### Added
- GitHub Container Registry (GHCR) publishing to CI/CD workflow

### Changed
- Modernized container images to latest versions
- Replaced Redis with Valkey
- Updated to latest PostgreSQL 18

### Fixed
- Fixed GHCR image tagging paths
- Fixed dev Docker image builds in CI/CD
- Removed static linking for libvirt builds
- Fixed CI workflow to use master branch only
