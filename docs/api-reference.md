# API Reference

The libvirt-volume-provisioner provides a RESTful HTTP API for managing volume provisioning operations.

## Base URL

```
https://hypervisor.example.com:8080/api/v1
```

## Authentication

All API requests require authentication via:
- **Primary**: X.509 client certificates (mutual TLS)
- **Fallback**: HMAC-SHA256 API tokens for simpler deployments

See [Authentication](./authentication.md) for setup details.

## Endpoints

### POST /api/v1/provision

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
  "image_type": "qcow2",
  "correlation_id": "optional-uuid"
}
```

**Request Fields:**
- `image_url` (required): Full URL to the image in MinIO
- `volume_name` (required): Name of the LVM volume to create/reuse
- `volume_size_gb` (required): Desired volume size in GB
- `image_type` (required): Image format (e.g., "qcow2", "raw")
- `correlation_id` (optional): UUID for request tracking and logging

**Response (Success - 201 Created):**

```json
{
  "job_id": "550e8400-e29b-41d4-a716-446655440000"
}
```

**Response (Error - 400 Bad Request):**

```json
{
  "error": "invalid request",
  "details": "volume_size_gb must be greater than 0"
}
```

**Volume Handling:**
- **New Volume**: Created if volume doesn't exist
- **Reuse**: Compatible existing volumes are reused (size validation ±5%)
- **Error**: Incompatible existing volumes cause job failure

---

### GET /api/v1/status/{job_id}

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
    "bytes_total": 50000000000
  },
  "correlation_id": "550e8400-e29b-41d4-a716-446655440000",
  "cache_hit": false,
  "image_path": "/var/lib/libvirt/images/ubuntu-20.04.qcow2"
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
  "image_path": "/var/lib/libvirt/images/ubuntu-20.04.qcow2"
}
```

**Response (Failed - 200 OK with error status):**

```json
{
  "job_id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "failed",
  "progress": null,
  "correlation_id": "550e8400-e29b-41d4-a716-446655440000",
  "error": "failed to populate volume: no space left on device",
  "cache_hit": false,
  "image_path": null
}
```

**Response Fields:**
- `job_id`: Unique identifier for the job
- `status`: One of: `pending`, `running`, `completed`, `failed`, `cancelled`
- `progress`: Progress information (null if not applicable)
  - `stage`: Current operation (e.g., "downloading", "converting", "populating", "finalizing")
  - `percent`: Completion percentage (0-100)
  - `bytes_processed`: Bytes processed so far
  - `bytes_total`: Total bytes to process
- `correlation_id`: UUID for request tracking
- `cache_hit`: Whether the image was retrieved from cache
- `image_path`: Path to the cached/populated image (null on failure)
- `error`: Error message if status is failed

**Job Statuses:**
- `pending`: Job queued, waiting to start
- `running`: Job actively provisioning
- `completed`: Job finished successfully
- `failed`: Job failed (check error field)
- `cancelled`: Job was cancelled

---

### DELETE /api/v1/cancel/{job_id}

Cancel a running provisioning job.

**Path Parameters:**
- `job_id`: The UUID of the job to cancel

**Response (Success - 204 No Content):**

```
(empty response body)
```

**Response (Already Complete - 400 Bad Request):**

```json
{
  "error": "invalid request",
  "details": "cannot cancel job in 'completed' status"
}
```

**Response (Not Found - 404 Not Found):**

```json
{
  "error": "not found",
  "details": "job not found"
}
```

---

## Health Check Endpoints

### GET /health

Basic health check endpoint.

**Response (200 OK):**

```json
{
  "status": "healthy"
}
```

### GET /healthz

Kubernetes-compatible health check (same as /health).

### GET /livez

Kubernetes-compatible liveness probe (same as /health).

---

## Metrics Endpoint

### GET /metrics

Prometheus-compatible metrics endpoint.

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
- `201 Created` - Resource created successfully
- `204 No Content` - Request succeeded with no content
- `400 Bad Request` - Invalid request parameters
- `401 Unauthorized` - Authentication failed
- `403 Forbidden` - Insufficient permissions
- `404 Not Found` - Resource not found
- `409 Conflict` - Resource conflict (e.g., volume already exists but is incompatible)
- `500 Internal Server Error` - Server error

### Error Response Format

```json
{
  "error": "error_code",
  "details": "human-readable description",
  "correlation_id": "optional-uuid-for-tracking"
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

