# API Reference

The libvirt-volume-provisioner provides a RESTful HTTP API for managing volume provisioning operations.

For the machine-readable OpenAPI 3.0.3 specification, see [`api.yaml`](../api.yaml) in the repository root.

## Base URL

```
https://hypervisor.example.com:8080/api/v1
```

## Authentication

All `/api/v1/*` requests require authentication via:
- **Primary**: X.509 client certificates (mutual TLS)
- **Fallback**: HMAC-SHA256 API tokens via `X-API-Token` header

Health, metrics, and liveness endpoints do not require authentication.

See [Authentication](./authentication.md) for setup details.

## Endpoints

### POST /api/v1/jobs

Start a volume provisioning job.

**Description:**
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
  "image_type": "qcow2"
}
```

**Request Fields:**
- `image_url` (required): Full URL to the image in MinIO
- `volume_name` (required): Name of the LVM volume to create/reuse
- `volume_size_gb` (required, min=1): Desired volume size in GB
- `image_type` (optional): Image format, defaults to "qcow2" (e.g., "qcow2", "raw")
- `correlation_id` (optional): Correlation ID for request tracking

**Response (Success - 202 Accepted):**

```json
{
  "job_id": "550e8400-e29b-41d4-a716-446655440000"
}
```

**Response (Error - 400 Bad Request):**

```json
{
  "error": "invalid request",
  "message": "image_url and volume_name are required",
  "code": 400
}
```

**Volume Handling:**
- **New Volume**: Created if volume doesn't exist
- **Reuse**: Compatible existing volumes are reused (size validation ±5%)
- **Error**: Incompatible existing volumes cause job failure

---

### GET /api/v1/jobs/{job_id}

Get the status of a provisioning job.

**Path Parameters:**
- `job_id`: The UUID returned from the provision endpoint

**Response (Running - 200 OK):**

```json
{
  "job_id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "running",
  "progress": {
    "stage": "downloading",
    "percent": 45,
    "bytes_processed": 22500000000,
    "bytes_total": 50000000000,
    "eta_sec": 120
  },
  "created_at": "2024-01-14T10:30:00Z",
  "updated_at": "2024-01-14T10:35:00Z"
}
```

**Response (Completed - 200 OK):**

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
  "image_path": "/var/lib/libvirt/images/ubuntu-20.04.qcow2",
  "created_at": "2024-01-14T10:30:00Z",
  "updated_at": "2024-01-14T10:40:00Z"
}
```

**Response (Failed - 200 OK with error status):**

```json
{
  "job_id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "failed",
  "error": "failed to populate volume: no space left on device",
  "created_at": "2024-01-14T10:30:00Z",
  "updated_at": "2024-01-14T10:40:00Z"
}
```

**Response Fields:**
- `job_id`: Unique identifier for the job
- `status`: One of: `pending`, `running`, `completed`, `failed`, `cancelled`
- `progress`: Progress information (omitted if not applicable)
  - `stage`: Current operation (e.g., "downloading", "converting", "populating", "finalizing")
  - `percent`: Completion percentage (0-100)
  - `bytes_processed`: Bytes processed so far
  - `bytes_total`: Total bytes to process
  - `eta_sec`: Estimated seconds remaining (may be absent early in job)
- `correlation_id`: UUID for request tracking (omitted if not set)
- `cache_hit`: Whether the image was retrieved from cache (omitted if not yet determined)
- `image_path`: Path to the cached/populated image (omitted on failure)
- `error`: Error message (only present when status is `failed`)
- `created_at`: Job creation timestamp (RFC 3339)
- `updated_at`: Last update timestamp (RFC 3339)

---

### POST /api/v1/jobs/{job_id}/cancel

Cancel a running provisioning job.

**Path Parameters:**
- `job_id`: The UUID of the job to cancel

**Response (Success - 200 OK):**

```json
{
  "status": "cancelled",
  "job_id": "550e8400-e29b-41d4-a716-446655440000"
}
```

**Response (Cannot Cancel - 400 Bad Request):**

```json
{
  "error": "failed to cancel job",
  "message": "cannot cancel job in 'completed' status",
  "code": 400
}
```

**Response (Not Found - 404 Not Found):**

```json
{
  "error": "job not found",
  "message": "job not found",
  "code": 404
}
```

---

### GET /api/v1/cache/images

List all cached images on the hypervisor host.

**Description:**
- Returns information about all QCOW2 images currently cached locally
- Includes image path, size, and checksum for each cached image
- Useful for monitoring cache usage and available images
- Supports pagination via `offset` and `limit` query parameters

**Query Parameters:**
- `offset` (optional, default=0): Number of items to skip
- `limit` (optional, default=100): Maximum number of items to return

**Response (Success - 200 OK):**

```json
{
  "images": [
    {
      "path": "/var/lib/libvirt/images/ubuntu-20.04.qcow2",
      "size": 2147483648,
      "checksum": "abc123def456..."
    },
    {
      "path": "/var/lib/libvirt/images/centos-8.qcow2",
      "size": 4294967296,
      "checksum": "def789ghi012..."
    }
  ],
  "count": 2
}
```

**Response Fields:**
- `images`: Array of cached image objects (paginated)
  - `path`: Full filesystem path to the cached image
  - `size`: Image size in bytes
  - `checksum`: SHA256 checksum of the image
- `count`: Total number of cached images
- `offset`: Current offset in the result set
- `limit`: Maximum number of items returned

---

### POST /api/v1/cache/fetch

Fetch and cache an image without creating a volume (prewarming).

**Description:**
- Downloads and caches a QCOW2 image from MinIO for future use
- Useful for prewarming the cache with commonly used images
- Does not create LVM volumes or perform conversions
- Returns a job ID for tracking the cache operation

**Request:**

```json
{
  "image_url": "https://minio.example.com/images/ubuntu-20.04.qcow2"
}
```

**Request Fields:**
- `image_url` (required): Full URL to the image in MinIO

**Response (Success - 202 Accepted):**

```json
{
  "job_id": "550e8400-e29b-41d4-a716-446655440000"
}
```

**Response (Error - 400 Bad Request):**

```json
{
  "error": "invalid request",
  "message": "image_url is required",
  "code": 400
}
```

---

## Health Check Endpoints

### GET /health

Basic health check endpoint. No authentication required.

**Response (200 OK):**

```json
{
  "status": "healthy",
  "timestamp": "2024-01-14T10:30:00Z",
  "version": "1.0.0",
  "uptime": "2h30m45s"
}
```

Returns `"status": "degraded"` when more than 2 jobs are active.

### GET /healthz

Kubernetes-compatible health check (same as /health).

### GET /livez

Kubernetes-compatible liveness probe (same as /health).

---

## Metrics Endpoint

### GET /metrics

Prometheus-compatible metrics endpoint. No authentication required.

**Format:** Prometheus text exposition format

**Metrics:**
- `libvirt_volume_provisioner_requests_total` - Total HTTP requests by endpoint/method/status
- `libvirt_volume_provisioner_jobs_total` - Total jobs by status (started, completed, failed)
- `libvirt_volume_provisioner_active_jobs` - Currently active provisioning jobs
- Go runtime metrics (GC, goroutines, memory usage)

---

## Error Handling

### HTTP Status Codes

- `200 OK` - Request succeeded
- `202 Accepted` - Job accepted for async processing
- `400 Bad Request` - Invalid request parameters
- `404 Not Found` - Resource not found
- `500 Internal Server Error` - Server error

### Error Response Format

All errors follow a consistent structure:

```json
{
  "error": "error_category",
  "message": "human-readable description",
  "code": 400
}
```

---

## Rate Limiting

The API enforces a maximum of 2 concurrent provisioning operations per host to prevent resource exhaustion. Additional requests will be queued.

---

## Request/Response Headers

### Request Headers

```
Content-Type: application/json
```

### Response Headers

```
Content-Type: application/json
X-Request-ID: correlation-id-uuid
```
