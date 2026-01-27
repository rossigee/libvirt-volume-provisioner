# Troubleshooting

Common issues and their solutions.

## Service Won't Start

### Error: "failed to connect to libvirt"

```
Error: failed to connect to libvirt: qemu:///system: permission denied
```

**Solution:** Ensure the service runs with proper permissions:

```bash
# Add user to libvirt group
sudo usermod -aG libvirt libvirt-volume-provisioner

# Or run as root (not recommended)
sudo systemctl edit libvirt-volume-provisioner
# Add: User=root
```

### Error: "LVM volume group not found"

```
Error: volume group 'data' does not exist or is not accessible
```

**Solution:** Check LVM configuration:

```bash
# List available volume groups
sudo vgdisplay

# Set correct volume group in environment
export LVM_VOLUME_GROUP="your-vg-name"

# Verify permission to access LVM
sudo lvs
```

### Error: "Permission denied" on LVM operations

```
Error: failed to create volume: permission denied
```

**Solution:** Add service user to lvm group:

```bash
# Add user to lvm group
sudo usermod -aG lvm libvirt-volume-provisioner

# Reload group permissions (may require login/logout or sudo -l)
```

## MinIO Connection Issues

### Error: "failed to connect to MinIO"

```
Error: dial tcp: lookup minio.example.com: Name or service not known
```

**Solution:** Verify MinIO endpoint and network:

```bash
# Check endpoint URL
echo $MINIO_ENDPOINT

# Test DNS resolution
nslookup minio.example.com

# Test network connectivity
curl -v https://minio.example.com:9000/

# Check firewall rules
sudo ufw allow 9000/tcp
```

### Error: "Authentication failed"

```
Error: access denied for user
```

**Solution:** Verify MinIO credentials:

```bash
# Check credentials are set
echo $MINIO_ACCESS_KEY
echo $MINIO_SECRET_KEY

# Test with mc client
mc config host add minio https://minio.example.com $MINIO_ACCESS_KEY $MINIO_SECRET_KEY
mc ls minio/

# Test with provisioner
curl -v https://hypervisor.example.com:8080/health
```

### Error: "SSL certificate problem"

```
Error: SSL certificate problem: self signed certificate
```

**Solution:** Install CA certificate:

```bash
# For development/testing only
export MINIO_SKIP_SSL_VERIFY=true

# Or install proper certificate
sudo cp minio-ca.crt /usr/local/share/ca-certificates/
sudo update-ca-certificates
sudo systemctl restart libvirt-volume-provisioner
```

## Job Failures

### Job stuck in "running" status

**Solution:** Check provisioner logs:

```bash
# View logs
sudo journalctl -u libvirt-volume-provisioner -f

# Check for errors
sudo journalctl -u libvirt-volume-provisioner -p err

# Look for specific job
sudo journalctl -u libvirt-volume-provisioner | grep job_id
```

### Job failed with "no space left on device"

```
Error: failed to populate volume: no space left on device
```

**Solution:** Free up disk space:

```bash
# Check disk usage
df -h /var/lib/libvirt

# Check LVM usage
sudo lvs -o LV_NAME,LV_SIZE,LV_SIZE_PERCENT

# Clean old images from cache
sudo rm -rf /var/lib/libvirt/images/*.qcow2

# Extend volume group
sudo lvextend -L +50G /dev/data/volumes
```

### Job failed with "image not found"

```
Error: failed to download image: object not found
```

**Solution:** Verify image URL:

```bash
# Check if image exists in MinIO
mc ls minio/vm-images/ubuntu-20.04.qcow2

# Verify URL format
# Should be: https://endpoint/bucket/image

# Check MinIO logs
docker logs minio  # if using Docker
```

## Certificate Issues

### Error: "certificate verification failed"

```
Error: certificate verify failed: unable to get local issuer certificate
```

**Solution:** Install CA certificate:

```bash
# Copy CA certificate
sudo cp ca.crt /usr/local/share/ca-certificates/

# Update CA store
sudo update-ca-certificates

# Restart service
sudo systemctl restart libvirt-volume-provisioner
```

### Error: "client certificate required"

```
curl: (58) unable to use client certificate (no key found or wrong pass phrase?)
```

**Solution:** Verify certificate and key files:

```bash
# Check certificate format
openssl x509 -in client.crt -text -noout

# Check key format
openssl rsa -in client.key -check

# Test certificate
curl --cacert ca.crt --cert client.crt --key client.key \
  https://hypervisor.example.com:8080/health
```

## Performance Issues

### High memory usage

**Solution:** Monitor and debug memory:

```bash
# Check current memory usage
ps aux | grep libvirt-volume-provisioner

# Get heap profile
curl http://localhost:6060/debug/pprof/heap > heap.prof
go tool pprof heap.prof

# Check for goroutine leaks
curl http://localhost:6060/debug/pprof/goroutine > goroutines.prof
go tool pprof goroutines.prof
```

### Slow provisioning

**Solution:** Check performance metrics:

```bash
# Check request latency
curl http://localhost:8080/metrics | grep request_duration

# Monitor system resources
top
iotop  # disk I/O

# Check network bandwidth
iftop
```

## Logging Issues

### No logs visible

**Solution:** Check log configuration:

```bash
# Verify service is running
sudo systemctl status libvirt-volume-provisioner

# Check log level
echo $LOG_LEVEL

# View system logs
sudo journalctl -u libvirt-volume-provisioner

# Check log file permissions
ls -la /var/log/libvirt-volume-provisioner/
```

### Logs are truncated or missing

**Solution:** Check journald configuration:

```bash
# Check journald status
systemctl status systemd-journald

# Verify log retention
journalctl --disk-usage

# Increase log storage
sudo vi /etc/systemd/journald.conf
# Set: Storage=persistent
# Increase: SystemMaxUse=4G

# Restart journald
sudo systemctl restart systemd-journald
```

## Health Check Failures

### Health endpoint returns error

```bash
# Test health endpoint
curl -v https://hypervisor.example.com:8080/health

# If certificate error:
curl -k https://hypervisor.example.com:8080/health

# Check service status
sudo systemctl status libvirt-volume-provisioner

# Restart service
sudo systemctl restart libvirt-volume-provisioner
```

## Getting Help

When reporting issues, please include:

1. **Service logs** (last 100 lines):
   ```bash
   sudo journalctl -u libvirt-volume-provisioner -n 100
   ```

2. **Service status**:
   ```bash
   sudo systemctl status libvirt-volume-provisioner
   ```

3. **Configuration** (without secrets):
   ```bash
   env | grep -E '^(MINIO|LVM|PORT|HOST|LOG)'
   ```

4. **Provisioner version**:
   ```bash
   libvirt-volume-provisioner --version
   ```

5. **Relevant error messages** from the logs

6. **Steps to reproduce** the issue

