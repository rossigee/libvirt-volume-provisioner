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
  "status": "healthy"
}
```

### GET /healthz

Kubernetes-compatible health check (alias for /health).

### GET /livez

Kubernetes liveness probe (alias for /health).

## Prometheus Metrics

### GET /metrics

Prometheus-compatible metrics endpoint.

**Available Metrics:**

- `libvirt_volume_provisioner_requests_total` - Total HTTP requests by endpoint/method/status
- `libvirt_volume_provisioner_requests_duration_seconds` - Request latency histogram
- `libvirt_volume_provisioner_jobs_total` - Total jobs by status (started, completed, failed, cancelled)
- `libvirt_volume_provisioner_active_jobs` - Currently active provisioning jobs
- `libvirt_volume_provisioner_job_duration_seconds` - Job duration histogram
- Go runtime metrics (gc_duration_seconds, go_goroutines, go_memory_usage)

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
      insecureSkipVerify: true
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

  # High active jobs
  - alert: VolumeProvisionerHighActiveJobs
    expr: libvirt_volume_provisioner_active_jobs > 10
    for: 5m
    annotations:
      summary: "High number of active jobs"
      description: "{{ $value }} active provisioning jobs on {{ $labels.instance }}"
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
   - Panel: `histogram_quantile(0.95, rate(libvirt_volume_provisioner_requests_duration_seconds_bucket[5m]))`
   - Type: Graph

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

