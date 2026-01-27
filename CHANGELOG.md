# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
