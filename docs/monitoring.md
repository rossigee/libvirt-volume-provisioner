# Monitoring

The libvirt-volume-provisioner provides comprehensive monitoring capabilities through health checks, metrics, and logging.

## Health Endpoints

### GET /health

Basic health check - returns 200 if service is running.

```bash
curl https://hypervisor.example.com:8080/health \
  --cacert /path/to/ca.crt \
  --cert /path/to/client.crt \
  --key /path/to/client.key
```

**Response:**

```json
{
  "status": "healthy",
  "timestamp": "2026-04-28T09:00:00Z",
  "version": "v0.10.0",
  "uptime": "3h14m52s"
}
```

Returns `"status": "degraded"` when all job slots are occupied (max 2 concurrent jobs).

### GET /healthz

Kubernetes-compatible health check (alias for /health).

### GET /livez

Kubernetes liveness probe (alias for /health).

## Distributed Tracing

The provisioner includes comprehensive distributed tracing using OpenTelemetry (OTLP).

### OpenTelemetry Configuration

Configure tracing by setting the OTLP gRPC endpoint:

```bash
export OTEL_EXPORTER_OTLP_ENDPOINT="https://otel-collector.example.com:4317"
export OTEL_SERVICE_NAME="libvirt-volume-provisioner"
```

### Trace Spans

The following operations are instrumented with spans:

- **HTTP Requests**: Automatic span creation for all API endpoints via otelgin middleware
- **Job Lifecycle**: `runJob`, `runCacheJob` with job metadata
- **MinIO Operations**: `DownloadImageToPath` with image URL and destination path
- **LVM Operations**: `CreateVolume`, `PopulateVolume`, `DeleteVolume` with volume metadata

### Trace Context Propagation

- HTTP request contexts are propagated to job operations
- Job operations create child spans with independent timeouts
- Trace context is available throughout the request lifecycle

### Log Correlation

Logs include trace and span IDs for correlation:

```json
{
  "timestamp": "2026-01-27T10:30:45.123Z",
  "level": "info",
  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
  "span_id": "00f067aa0ba902b7",
  "job_id": "550e8400-e29b-41d4-a716-446655440000",
  "message": "Starting image download"
}
```

## Prometheus Metrics

### GET /metrics

Prometheus-compatible metrics endpoint.

**Available Metrics:**

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `libvirt_volume_provisioner_requests_total` | counter | method, endpoint, status | Total HTTP requests |
| `libvirt_volume_provisioner_request_duration_seconds` | histogram | method, endpoint | HTTP request latency |
| `libvirt_volume_provisioner_active_jobs` | gauge | — | Currently running jobs (max 2) |
| `libvirt_volume_provisioner_jobs_total` | counter | status | Jobs by terminal status: `completed`, `failed`, `cancelled` |
| `libvirt_volume_provisioner_job_duration_seconds` | histogram | status | Job execution duration |
| `libvirt_volume_provisioner_cache_hits_total` | counter | — | Cache hits (image already local and valid) |
| `libvirt_volume_provisioner_cache_misses_total` | counter | — | Cache misses (download required) |
| `libvirt_volume_provisioner_cache_hit_ratio` | gauge | — | Rolling cache hit ratio (0.0–1.0) |
| `libvirt_volume_provisioner_images_downloaded_total` | counter | — | Successful image downloads |
| `libvirt_volume_provisioner_image_download_size_bytes` | histogram | image_type | Downloaded image sizes |
| `libvirt_volume_provisioner_image_errors_total` | counter | operation, error_type | Image operation errors |
| `libvirt_volume_provisioner_storage_operations_total` | counter | operation, result | Storage DB operations |
| `libvirt_volume_provisioner_storage_errors_total` | counter | operation, error_type | Storage DB errors |
| `libvirt_volume_provisioner_stage_duration_seconds` | histogram | stage | Download/convert stage durations |
| `libvirt_volume_provisioner_stage_throughput_bytes_per_second` | gauge | stage | Current throughput per stage |
| `libvirt_volume_provisioner_health_status` | gauge | — | 1=healthy, 0=degraded (at capacity) |
| `libvirt_volume_provisioner_dependencies_up` | gauge | dependency | 1=up, 0=down per dependency: `minio`, `lvm`, `libvirt`, `storage` |

**Stage Timing Metrics:**

The provisioner tracks performance of individual job stages:

| Metric | Type | Description |
|--------|------|-------------|
| `stage_duration_seconds` | histogram | Time for download/convert stages |
| `stage_throughput_bytes_per_second` | gauge | Current throughput (labels: `download`, `convert`) |

Query average stage durations:
```promql
# Average download time
rate(libvirt_volume_provisioner_stage_duration_seconds_sum{stage="download"}[5m]) / rate(libvirt_volume_provisioner_stage_duration_seconds_count{stage="download"}[5m])

# Average convert time
rate(libvirt_volume_provisioner_stage_duration_seconds_sum{stage="convert"}[5m]) / rate(libvirt_volume_provisioner_stage_duration_seconds_count{stage="convert"}[5m])

# Current throughput
libvirt_volume_provisioner_stage_throughput_bytes_per_second
```

**Progress Reporting:**

Job progress is reported as percentage (0-100%) and includes:
- **Stage**: Current stage (`initializing`, `downloading`, `converting`, `finalizing`)
- **Percent**: Overall job completion percentage
- **BytesProcessed/BytesTotal**: Transfer progress

The progress split between download and convert stages is dynamically estimated based on:
1. Historical stage durations from the database (last 20 measurements)
2. Default rates: 100 MB/s download, 200 MB/s convert
3. Image size (for qcow2, uses virtual size from header)

For cache hits, the download stage is skipped and convert spans the full 0-100% range.

### Prometheus ServiceMonitor (Kubernetes)

For deployments with Prometheus Operator:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: libvirt-volume-provisioner
  namespace: monitoring
spec:
  selector:
    matchLabels:
      app: libvirt-volume-provisioner
  endpoints:
  - port: https
    path: /metrics
    scheme: https
    tlsConfig:
      caFile: /etc/prometheus/ca.crt
      certFile: /etc/prometheus/client.crt
      keyFile: /etc/prometheus/client.key
    bearerTokenSecret:
      name: provisioner-api-tokens
      key: token
```

### Prometheus Scrape Configuration

For direct Prometheus scraping:

```yaml
global:
  scrape_interval: 15s

scrape_configs:
  - job_name: libvirt-volume-provisioner
    scheme: https
    static_configs:
      - targets: ['hypervisor.example.com:8080']
    tls_config:
      ca_file: /etc/prometheus/ca.crt
      cert_file: /etc/prometheus/client.crt
      key_file: /etc/prometheus/client.key
```

## Alerting Rules

### Prometheus Alert Rules

Create `/etc/prometheus/rules/libvirt-provisioner.yml`:

```yaml
groups:
- name: libvirt-volume-provisioner
  interval: 30s
  rules:
  # Service health alerts
  - alert: VolumeProvisionerDown
    expr: up{job="libvirt-volume-provisioner"} == 0
    for: 5m
    annotations:
      summary: "Volume Provisioner is down"
      description: "libvirt-volume-provisioner at {{ $labels.instance }} is not responding"

  # High error rate
  - alert: VolumeProvisionerHighErrorRate
    expr: rate(libvirt_volume_provisioner_requests_total{status=~"5.."}[5m]) > 0.1
    for: 10m
    annotations:
      summary: "High error rate detected"
      description: "Error rate is {{ $value | humanizePercentage }} for {{ $labels.instance }}"

  # Job failures
  - alert: VolumeProvisionerJobFailures
    expr: increase(libvirt_volume_provisioner_jobs_total{status="failed"}[10m]) > 5
    for: 5m
    annotations:
      summary: "Multiple provisioning job failures"
      description: "{{ $value }} jobs failed in the last 10 minutes on {{ $labels.instance }}"

  # Performance degradation
  - alert: VolumeProvisionerHighLatency
    expr: histogram_quantile(0.95, rate(libvirt_volume_provisioner_requests_duration_seconds_bucket[5m])) > 30
    for: 10m
    annotations:
      summary: "High request latency"
      description: "95th percentile latency is {{ $value }}s on {{ $labels.instance }}"

  # Service degraded (all job slots occupied)
  - alert: VolumeProvisionerDegraded
    expr: libvirt_volume_provisioner_health_status == 0
    for: 2m
    annotations:
      summary: "Volume Provisioner is degraded"
      description: "All {{ $value }} job slots occupied on {{ $labels.instance }}"

  # Dependency unavailable
  - alert: VolumeProvisionerDependencyDown
    expr: libvirt_volume_provisioner_dependencies_up == 0
    for: 5m
    annotations:
      summary: "Volume Provisioner dependency unavailable"
      description: "Dependency {{ $labels.dependency }} is down on {{ $labels.instance }}"

  # Low cache hit ratio
  - alert: VolumeProvisionerLowCacheHitRatio
    expr: libvirt_volume_provisioner_cache_hit_ratio < 0.5
    for: 30m
    annotations:
      summary: "Low image cache hit ratio"
      description: "Cache hit ratio is {{ $value | humanizePercentage }} on {{ $labels.instance }} — consider pre-warming the cache"
```

## Logging

The provisioner generates structured logs suitable for centralized aggregation.

### Log Format

JSON format (default):

```json
{
  "timestamp": "2026-01-27T10:30:45.123Z",
  "level": "info",
  "component": "provisioner",
  "job_id": "550e8400-e29b-41d4-a716-446655440000",
  "correlation_id": "provision-vm-001",
  "message": "Starting image download",
  "image_url": "https://minio.example.com/images/ubuntu-20.04.qcow2"
}
```

Text format (set `LOG_FORMAT=text`):

```
2026-01-27T10:30:45.123Z [INFO] provisioner: Starting image download (job_id=550e8400-e29b-41d4-a716-446655440000, image_url=https://minio.example.com/images/ubuntu-20.04.qcow2)
```

### Viewing Logs

#### Systemd Logs

```bash
# Recent logs
sudo journalctl -u libvirt-volume-provisioner -n 100

# Follow logs
sudo journalctl -u libvirt-volume-provisioner -f

# Logs from last hour
sudo journalctl -u libvirt-volume-provisioner --since "1 hour ago"

# Filter by log level
sudo journalctl -u libvirt-volume-provisioner -p err
```

#### Docker Logs

```bash
# Recent logs
docker logs libvirt-volume-provisioner

# Follow logs
docker logs -f libvirt-volume-provisioner

# Last 100 lines
docker logs --tail 100 libvirt-volume-provisioner
```

### Log Aggregation (Loki/Promtail)

Configure Promtail to scrape logs:

```yaml
scrape_configs:
  - job_name: libvirt-volume-provisioner
    static_configs:
      - targets:
          - localhost
        labels:
          job: libvirt-volume-provisioner
          __path__: /var/log/libvirt-volume-provisioner.log
```

Query in Grafana Loki:

```logql
{job="libvirt-volume-provisioner"} | json | level="error"
```

## Grafana Dashboards

### Sample Dashboard JSON

Create a Grafana dashboard with:

#### Metrics Panels

1. **Service Status**
   - Panel: `up{job="libvirt-volume-provisioner"}`
   - Type: Stat

2. **Request Rate**
   - Panel: `rate(libvirt_volume_provisioner_requests_total[5m])`
   - Type: Graph

3. **Error Rate**
   - Panel: `rate(libvirt_volume_provisioner_requests_total{status=~"5.."}[5m])`
   - Type: Graph

4. **Active Jobs**
   - Panel: `libvirt_volume_provisioner_active_jobs`
   - Type: Gauge

5. **Job Success Rate**
   - Panel: `rate(libvirt_volume_provisioner_jobs_total{status="completed"}[5m]) / rate(libvirt_volume_provisioner_jobs_total[5m])`
   - Type: Gauge

6. **Response Latency (95th percentile)**
   - Panel: `histogram_quantile(0.95, rate(libvirt_volume_provisioner_request_duration_seconds_bucket[5m]))`
   - Type: Graph

7. **Cache Hit Ratio**
   - Panel: `libvirt_volume_provisioner_cache_hit_ratio`
   - Type: Gauge (0–1)

8. **Dependency Health**
   - Panel: `libvirt_volume_provisioner_dependencies_up`
   - Type: Table (one row per dependency label)

9. **Download Throughput**
   - Panel: `libvirt_volume_provisioner_stage_throughput_bytes_per_second{stage="download"}`
   - Type: Graph

#### Tracing Integration

For distributed tracing, configure Tempo or Jaeger data source and create trace panels:

7. **Trace Explorer**
   - Query: `{resource.service.name="libvirt-volume-provisioner"}`
   - Shows end-to-end request traces

8. **Span Performance**
   - Service: `libvirt-volume-provisioner`
   - Operation: `runJob`, `DownloadImageToPath`, `CreateVolume`, etc.
   - Shows span duration percentiles and error rates

9. **Service Map**
   - Visualize service dependencies and trace flows
   - Shows relationships between provisioner, MinIO, and LVM operations

## Key Metrics to Monitor

### Performance Indicators

- **Request latency**: Monitor 95th and 99th percentiles
- **Throughput**: Track requests per second
- **Error rate**: Alert on 5xx errors >10%
- **Active jobs**: Should not consistently exceed 10

### Reliability Indicators

- **Service uptime**: Track availability
- **Job success rate**: Should be >95%
- **Job failure reasons**: Monitor and categorize
- **Cache hit rate**: Track to optimize storage

### Resource Indicators

- **Memory usage**: Monitor Go runtime metrics
- **Goroutine count**: Detect leaks
- **Garbage collection**: Monitor GC pause time

