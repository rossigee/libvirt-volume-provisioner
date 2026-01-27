# Usage Examples

This guide provides practical examples of using the libvirt-volume-provisioner API.

## Provisioning a New Volume

First provisioning request - image is downloaded, cached with compression preserved, then converted to raw and populated to the LVM volume:

```bash
curl -X POST https://hypervisor.example.com:8080/api/v1/provision \
  --cacert /path/to/ca.crt \
  --cert /path/to/client.crt \
  --key /path/to/client.key \
  -H "Content-Type: application/json" \
  -d '{
    "image_url": "https://minio.example.com/images/ubuntu-20.04.qcow2",
    "volume_name": "vm-root-disk-1",
    "volume_size_gb": 50,
    "image_type": "qcow2",
    "correlation_id": "provision-vm-001"
  }'
```

**Response:**

```json
{
  "job_id": "550e8400-e29b-41d4-a716-446655440000"
}
```

## Checking Provisioning Progress

Poll the status endpoint to monitor progress:

```bash
curl https://hypervisor.example.com:8080/api/v1/status/550e8400-e29b-41d4-a716-446655440000 \
  --cacert /path/to/ca.crt \
  --cert /path/to/client.crt \
  --key /path/to/client.key
```

**Response while in progress:**

```json
{
  "job_id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "running",
  "progress": {
    "stage": "downloading",
    "percent": 45,
    "bytes_processed": 22500000000,
    "bytes_total": 50000000000
  }
}
```

**Response when complete:**

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
  "correlation_id": "provision-vm-001",
  "cache_hit": false,
  "image_path": "/var/lib/libvirt/images/ubuntu-20.04.qcow2"
}
```

## Cache Hit Example

Second request for the same image uses cached version - much faster:

```bash
curl -X POST https://hypervisor.example.com:8080/api/v1/provision \
  --cacert /path/to/ca.crt \
  --cert /path/to/client.crt \
  --key /path/to/client.key \
  -H "Content-Type: application/json" \
  -d '{
    "image_url": "https://minio.example.com/images/ubuntu-20.04.qcow2",
    "volume_name": "vm-root-disk-2",
    "volume_size_gb": 50,
    "image_type": "qcow2",
    "correlation_id": "provision-vm-002"
  }'
```

**Response when completed (note `cache_hit: true`):**

```json
{
  "job_id": "660f9511-f30c-52e5-b827-557766551111",
  "status": "completed",
  "progress": {
    "stage": "finalizing",
    "percent": 100,
    "bytes_processed": 50000000000,
    "bytes_total": 50000000000
  },
  "correlation_id": "provision-vm-002",
  "cache_hit": true,
  "image_path": "/var/lib/libvirt/images/ubuntu-20.04.qcow2"
}
```

**Note:** The second request completes much faster due to cache hit, as the QCOW2 image is already cached in compressed format and doesn't need to be re-downloaded.

## Cancelling a Job

Cancel a running provisioning job:

```bash
curl -X DELETE https://hypervisor.example.com:8080/api/v1/cancel/550e8400-e29b-41d4-a716-446655440000 \
  --cacert /path/to/ca.crt \
  --cert /path/to/client.crt \
  --key /path/to/client.key
```

**Response (204 No Content):**

```
(empty response body)
```

## Health Check

Check if the provisioner is healthy:

```bash
curl https://hypervisor.example.com:8080/health \
  --cacert /path/to/ca.crt \
  --cert /path/to/client.crt \
  --key /path/to/client.key
```

**Response:**

```json
{
  "status": "healthy"
}
```

## Provisioning Multiple VMs in Sequence

Provisioning a fleet of VMs using the same base image:

```bash
#!/bin/bash

PROVISIONER_URL="https://hypervisor.example.com:8080"
IMAGE_URL="https://minio.example.com/images/ubuntu-20.04.qcow2"
BASE_VOLUME_NAME="fleet-vm"
VOLUMES=50  # GB

# Provision 5 VMs sequentially
for i in {1..5}; do
  VOLUME_NAME="${BASE_VOLUME_NAME}-${i}"
  CORR_ID="batch-provision-$(date +%s)-${i}"

  echo "Provisioning ${VOLUME_NAME}..."

  # Start provisioning
  JOB_RESPONSE=$(curl -s -X POST "${PROVISIONER_URL}/api/v1/provision" \
    --cacert /path/to/ca.crt \
    --cert /path/to/client.crt \
    --key /path/to/client.key \
    -H "Content-Type: application/json" \
    -d "{
      \"image_url\": \"${IMAGE_URL}\",
      \"volume_name\": \"${VOLUME_NAME}\",
      \"volume_size_gb\": ${VOLUMES},
      \"image_type\": \"qcow2\",
      \"correlation_id\": \"${CORR_ID}\"
    }")

  JOB_ID=$(echo "${JOB_RESPONSE}" | jq -r '.job_id')

  # Wait for completion
  while true; do
    STATUS=$(curl -s "${PROVISIONER_URL}/api/v1/status/${JOB_ID}" \
      --cacert /path/to/ca.crt \
      --cert /path/to/client.crt \
      --key /path/to/client.key)

    JOB_STATUS=$(echo "${STATUS}" | jq -r '.status')
    PERCENT=$(echo "${STATUS}" | jq -r '.progress.percent')

    if [ "${JOB_STATUS}" = "completed" ]; then
      CACHE_HIT=$(echo "${STATUS}" | jq -r '.cache_hit')
      echo "  ✓ Completed (cache_hit: ${CACHE_HIT})"
      break
    elif [ "${JOB_STATUS}" = "failed" ]; then
      ERROR=$(echo "${STATUS}" | jq -r '.error')
      echo "  ✗ Failed: ${ERROR}"
      exit 1
    else
      echo "  - ${PERCENT}% (${JOB_STATUS})"
      sleep 5
    fi
  done
done

echo "All VMs provisioned successfully!"
```

## Using API Tokens (Fallback Authentication)

If using API token authentication instead of mutual TLS:

```bash
curl -X POST https://hypervisor.example.com:8080/api/v1/provision \
  -H "Authorization: Bearer YOUR_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "image_url": "https://minio.example.com/images/ubuntu-20.04.qcow2",
    "volume_name": "vm-test",
    "volume_size_gb": 50,
    "image_type": "qcow2"
  }'
```

Or using X-API-Token header:

```bash
curl -X POST https://hypervisor.example.com:8080/api/v1/provision \
  -H "X-API-Token: YOUR_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "image_url": "https://minio.example.com/images/ubuntu-20.04.qcow2",
    "volume_name": "vm-test",
    "volume_size_gb": 50,
    "image_type": "qcow2"
  }'
```

