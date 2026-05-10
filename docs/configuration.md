# Configuration

The libvirt-volume-provisioner is configured via a YAML file.  The default path is
`/etc/libvirt-volume-provisioner/config.yaml`; override it with the `--config` flag.

MinIO credentials (`access_key` / `secret_key`) may also be supplied via environment
variables so that secrets stay out of the config file — see [Credentials](#credentials)
below.

## Config file location

```
--config /etc/libvirt-volume-provisioner/config.yaml   (default)
--config /path/to/custom.yaml
```

The daemon starts with built-in defaults for every field, so a partial config file is
fine — only specify what differs from the defaults.

## Full reference

```yaml
# /etc/libvirt-volume-provisioner/config.yaml

server:
  port: 8080                                            # HTTP/HTTPS listen port
  tls_cert: ""                                          # Path to server TLS certificate (enables HTTPS)
  tls_key: ""                                          # Path to server TLS private key
  ca_cert: ""                                          # Path to client CA cert (enables mTLS)
  api_tokens_file: /etc/libvirt-volume-provisioner/tokens
  db_path: ./provisioner.db                            # SQLite job database path

minio:
  endpoint: https://minio.example.com                  # MinIO/S3 server URL (http or https)
  bucket: ""                                           # Default bucket (informational)
  access_key: ""                                       # See Credentials section
  secret_key: ""                                       # See Credentials section
  ca_cert: ""                                         # Custom CA cert for MinIO HTTPS
  retry_attempts: 3
  retry_backoff_ms: [100, 1000, 10000]                 # Delay between retries (ms)

libvirt:
  uri: qemu:///system                                  # libvirt connection URI
  pool: images                                         # Storage pool name
  max_concurrent: 2                                    # Max simultaneous provisioning jobs

lvm:
  volume_group: vg0                                    # LVM volume group for VM disks
  retry_attempts: 2
  retry_backoff_ms: [100, 1000]

cache:
  max_age: 168h                                        # Evict cached images older than this
  eviction_interval: 1h                                # How often to run the eviction sweep

logging:
  level: info                                          # debug | info | warn | error
  format: json                                         # json | text
  file: stdout                                         # stdout | stderr | /path/to/file.log
  sampling_rate: 0                                     # 0 = disabled; N = log every Nth entry
  loki_url: ""                                         # Loki push endpoint (optional)
  webhook_url: ""                                      # Webhook for log delivery (optional)

metrics:
  enabled: true                                        # Expose Prometheus metrics at /metrics

tracing:
  endpoint: ""                                         # OTLP gRPC endpoint; empty = disabled
  sampling_rate: 1.0                                   # 0.0–1.0 (1.0 = 100%)
  exporters: [otlp]                                    # Supported: otlp
```

## Credentials

`minio.access_key` and `minio.secret_key` can be set in the config file, but it is
recommended to supply them via environment variables so that credentials are not stored
in a world-readable file:

| Environment variable | Overrides |
|---|---|
| `MINIO_ACCESS_KEY` or `MINIO_ACCESS_KEY_ID` | `minio.access_key` |
| `MINIO_SECRET_KEY` or `MINIO_SECRET_ACCESS_KEY` | `minio.secret_key` |

Environment variables take precedence over config file values when both are present.

## Typical production config

```yaml
server:
  port: 3443
  tls_cert: /etc/pki/provisioner/servercert.pem
  tls_key:  /etc/pki/provisioner/serverkey.pem
  ca_cert:  /etc/pki/libvirt/ca.crt
  api_tokens_file: /etc/libvirt-volume-provisioner/tokens
  db_path: /var/lib/libvirt-volume-provisioner/jobs.db

minio:
  endpoint: https://minio.prod.example.com:9000
  bucket: vm-images
  ca_cert: /etc/pki/minio/ca.crt
  retry_attempts: 5
  retry_backoff_ms: [100, 1000, 10000, 30000, 60000]

libvirt:
  uri: qemu+tls://localhost/system
  pool: data
  max_concurrent: 4

lvm:
  volume_group: data

cache:
  max_age: 336h    # 2 weeks
  eviction_interval: 6h

logging:
  level: info
  format: json
  file: /var/log/libvirt-volume-provisioner/provisioner.log

tracing:
  endpoint: otel-collector.internal:4317
  sampling_rate: 0.1
```

## Systemd deployment

Create `/etc/libvirt-volume-provisioner/config.yaml` with your settings, then supply
credentials via systemd drop-in:

```ini
# /etc/systemd/system/libvirt-volume-provisioner.service.d/credentials.conf
[Service]
Environment="MINIO_ACCESS_KEY=your-access-key"
Environment="MINIO_SECRET_KEY=your-secret-key"
```

Reload and restart:

```bash
sudo systemctl daemon-reload
sudo systemctl restart libvirt-volume-provisioner
```

## Docker deployment

Mount the config file and pass credentials as environment variables:

```bash
docker run -d \
  --privileged \
  -v /var/run/libvirt:/var/run/libvirt:rw \
  -v /dev/mapper:/dev/mapper:rw \
  -v /etc/libvirt-volume-provisioner:/etc/libvirt-volume-provisioner:ro \
  -e MINIO_ACCESS_KEY=your-access-key \
  -e MINIO_SECRET_KEY=your-secret-key \
  -p 3443:3443 \
  ghcr.io/rossigee/libvirt-volume-provisioner:latest
```

## Certificate setup

See [Authentication](./authentication.md) for TLS certificate and API token setup.
