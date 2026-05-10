# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.11.0] - 2026-05-10

### Changed
- **Configuration**: Daemon now reads `/etc/libvirt-volume-provisioner/config.yaml` instead of
  environment variables. All settings (server port, TLS, MinIO endpoint/CA, libvirt URI/pool,
  LVM volume group, logging, metrics, tracing, cache) are now in the config file.
  MinIO credentials (`MINIO_ACCESS_KEY` / `MINIO_SECRET_KEY`) remain env-var-only so secrets
  stay out of the config file.
- **New config fields wired**: `libvirt.uri`, `libvirt.pool`, `libvirt.max_concurrent`, and
  `minio.bucket` are now read from config (previously hardcoded).
- `--config` flag added to override the default config path.

### Removed
- All configuration env vars except `MINIO_ACCESS_KEY` / `MINIO_ACCESS_KEY_ID` and
  `MINIO_SECRET_KEY` / `MINIO_SECRET_ACCESS_KEY`.

## [0.10.0] - 2026-04-28

### Security
- **TLS hardening**: Removed `InsecureSkipVerify` from MinIO client; custom CA supported via `MINIO_CA_CERT`
- **mTLS**: Fixed peer certificate auth to use `c.Request.TLS.PeerCertificates` (was never functional)
- **Token comparison**: Replaced string equality with `crypto/subtle.ConstantTimeCompare` to prevent timing attacks
- **Default CA removed**: System CA bundle (`/etc/ssl/certs/ca-certificates.crt`) no longer used as default `CLIENT_CA_CERT`; an unconfigured CA now means mTLS is disabled, not open to any public-CA cert
- **Token file**: Missing or empty `API_TOKENS_FILE` is now a fatal startup error (removed hardcoded `dev-token-12345` fallback)
- **Path traversal**: Cache keys validated as 64-character lowercase hex before constructing file paths

### Fixed
- **Data races**: Per-`Job` `sync.RWMutex` added; all field reads/writes are now lock-protected
- **Background context**: Provisioning and cache jobs now use `context.WithTimeout(context.Background(), 30m)` instead of the HTTP request context, which was cancelled on response
- **Job cleanup ordering**: `CleanupCompletedJobs` now deletes oldest entries first (was random map iteration)
- **Health check threshold**: Degraded state now triggers at `active_jobs >= 2` (max capacity), not `> 2`
- **LVM device check**: Uses `os.Stat` instead of shell-out; bounds-checked `Attributes[4]` access
- **SQL error**: `sql.ErrNoRows` comparison now uses `errors.Is`
- **Metrics wiring**: All declared metrics are now instrumented — `health_status`, `dependencies_up`, `active_jobs`, `job_duration`, `jobs_total`, `cache_hits/misses`, `image_download_size`, `stage_duration` were previously defined but never updated
- **Health uptime**: Response field now reports real elapsed time instead of `"unknown"`

### Removed
- Dead code: `DownloadImage`/`downloadImageOnce` (MinIO), `AllocateImage`, `GetImageNameFromURL` (pool)
- Duplicate Prometheus global registry registration in `handlers.go`
- Duplicate error log block in Loki hook `sendLogs`

### Changed
- Go toolchain updated to 1.26.2 (go.mod and Docker builder images)
- All Makefile targets now have `##` help comments and complete `.PHONY` declaration

## [0.8.1] - 2026-04-09

### Fixed
- **sudoers file ownership**: postinst script now sets correct ownership (root:root) to avoid sudoers permission error.

## [0.8.0] - 2026-04-09

### Added
- **Timing package**: New `internal/timing` package with `Estimator` and `MovingAverage` for calculating stage weights and progress based on data transfer rates.
- **Dynamic progress weight estimation**: Stage weights (download vs convert split) are now calculated dynamically based on historical rates and image sizes, replacing the static 50/50 split.
- **Stage timing metrics**: New Prometheus metrics:
  - `libvirt_volume_provisioner_stage_duration_seconds` - histogram for download/convert stage durations
  - `libvirt_volume_provisioner_stage_throughput_bytes_per_second` - gauge for current throughput per stage

### Fixed
- **Stuck progress at 55%**: Progress reporting now works on first run with no historical data by using default rates (100 MB/s download, 200 MB/s convert).
- **qemu-img convert output**: Confirmed correct output target is device path, not stdout (test coverage added).

### Changed
- **Default rates**: Updated to 100 MB/s download, 200 MB/s convert (was 300/500 MB/s).
- **Progress algorithm**: Uses `timing.Estimator` for weight calculation and time-based progress ticks during convert stage.

## [0.7.3] - 2026-04-07

### Added
- **Database schema for stage rates**: New `stage_rates` table to store historical performance data for progress estimation.
- **Rate-based progress estimation**: Collects download and conversion rates, uses historical averages with defaults (300 Mb/s download, 500 Mb/s convert) for accurate ETA reporting.

### Fixed
- **LVM volume population failure**: `qemu-img convert` failed on block devices with "Cannot grow device files" error. Fixed by streaming output directly to device file using native Go I/O, avoiding device resize issues.

### Changed
- **Progress reporting**: Now uses estimated completion times based on rate data instead of hardcoded percentages.

## [0.6.3] - 2026-03-20

### Fixed
- **Image cache mechanism**: Three bugs caused cached images to be re-downloaded on every request:
  - The local file checksum computation after download was dead code — the fallback always set a non-empty cache key before reaching it, so the actual SHA256 of the downloaded file was never computed or stored.
  - The cache lookup key switched between `SHA256(image content)` (when a MinIO `.sha256` file was present) and `SHA256(URL)` (when absent), causing guaranteed cache misses whenever MinIO `.sha256` availability changed between runs.
  - Checksum files in `sha256sum(1)` format (`HASH  filename`) were silently rejected by a strict 64-character length check, causing all such files to fall back to URL-hash keying even when a valid remote checksum was available.
- **Cache design corrected**: The cache key is now always `SHA256(URL)` (stable filesystem identifier). After each download the actual file checksum is computed and stored in the `.sha256` sentinel. On subsequent requests, if a remote checksum is available from MinIO it is compared against the stored value; a mismatch triggers a fresh download (stale cache eviction). Both raw-hash and `sha256sum(1)` format checksum files are now accepted.

## [0.6.2] - 2026-03-18

### Fixed
- **Prometheus metrics not served**: The custom Prometheus registry created in `NewMetrics()` was discarded instead of being stored, so `/metrics` served an empty default registry. The registry is now stored on the `Metrics` struct and used by the `/metrics` HTTP handler.

## [0.6.1] - 2026-03-18

### Fixed
- **Image cache key fallback**: When no `.sha256` file is available in MinIO, the provisioner now uses a SHA256 hash of the image URL as the cache key instead of the raw URL. This fixes a crash when using HTTPS endpoints with ports (e.g. `https://host:9000/...`) where the raw URL produced an invalid filesystem path.

## [0.6.0] - 2026-03-12

### Added
- **Enterprise Observability Stack**: Comprehensive monitoring and debugging capabilities
  - **Enhanced Logging System**: Configurable JSON/text logging with sampling, external aggregation (webhook/Loki), and structured error logging
  - **Advanced Metrics Collection**: 20+ Prometheus metrics covering cache ratios, job lifecycle, image operations, and health status
  - **Multi-Exporter Tracing**: OpenTelemetry support with OTLP, Jaeger, and Zipkin exporters, configurable sampling, and distributed context propagation
- **Request Logging Middleware**: HTTP request/response logging with correlation IDs and performance metrics
- **External Log Hooks**: Configurable webhook and Loki integration for centralized logging
- **Health Status Monitoring**: Comprehensive dependency and system health tracking
- **Performance Optimization**: Log sampling and configurable tracing to minimize overhead

### Changed
- **Dependencies**: Updated Gin (v1.12.0), OpenTelemetry (v1.42.0), MinIO (v7.0.99), golangci-lint (v2.11.3)
- **CI/CD**: Streamlined workflow configuration, removed problematic build-release workflows
- **Code Quality**: Enhanced error handling, resource management, and type safety

### Fixed
- **GitHub Actions Workflows**: Resolved CI failures by removing incompatible workflow configurations
- **Resource Management**: Improved HTTP client usage and response body handling
- **Type Safety**: Enhanced error checking and nil pointer protection

## [0.5.3] - 2026-03-12

### Added
- **Enhanced Logging System**: New configurable logging with JSON/text formats, log sampling, external aggregation support (webhook, Loki), and structured error logging
- **Comprehensive Metrics**: Added cache hit/miss ratios, job execution metrics, image download statistics, storage operation metrics, and health status indicators
- **Multi-Exporter Tracing**: Support for OTLP, Jaeger, and Zipkin exporters with configurable sampling rates and enhanced span coverage
- **Request Logging Middleware**: HTTP request/response logging with correlation IDs and performance metrics
- **External Log Hooks**: Configurable webhook and Loki integration for centralized logging

### Fixed
- **Image Cache**: Fixed cache key mismatch that caused images to be redownloaded on every provision request. The `getOrDownloadImage` function now consistently uses the SHA256 checksum as the cache key for both cache lookup and storage.
- **GitHub Actions Workflow**: Fixed YAML indentation issues in build-release.yml that prevented proper CI/CD execution

### Changed
- **Dependencies**: Updated Gin (v1.11.0→v1.12.0), OpenTelemetry (v1.40.0→v1.42.0), MinIO (v7.0.98→v7.0.99), and other security patches
- **golangci-lint**: Updated to v2.11.3 with enhanced rules
- **API Handler**: Refactored to support enhanced metrics collection

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
