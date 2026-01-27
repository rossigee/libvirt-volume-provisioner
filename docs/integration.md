# Integration

This document describes how libvirt-volume-provisioner integrates with other infrastructure tools and systems.

## Integration with infrastructure-builder

The provisioner integrates seamlessly with the `infrastructure-builder` tool for automated VM provisioning workflows.

### Basic Integration

```bash
# infrastructure-builder detects and uses provisioner API
./infrastructure-builder deploy -t vm -m base-standard -d itx-master-controlplane-1
```

The builder automatically:
1. Calls provisioner API to populate root volume
2. Waits for provisioning to complete
3. Starts the VM with libvirtd
4. Applies cloud-init configuration

### Configuration

In infrastructure-builder config:

```yaml
provisioning:
  provisioner_url: "https://hypervisor.example.com:8080"
  provisioner_cert: "/path/to/client.crt"
  provisioner_key: "/path/to/client.key"
  provisioner_ca: "/path/to/ca.crt"
```

## Integration with Ansible

Use Ansible to provision VMs via the provisioner:

```yaml
- name: Provision VM root volume
  uri:
    url: "https://hypervisor.example.com:8080/api/v1/provision"
    method: POST
    client_cert: "/path/to/client.crt"
    client_key: "/path/to/client.key"
    ca_path: "/path/to/ca.crt"
    body_format: json
    body:
      image_url: "https://minio.example.com/images/ubuntu-20.04.qcow2"
      volume_name: "{{ vm_name }}-root"
      volume_size_gb: 50
      image_type: "qcow2"
      correlation_id: "{{ ansible_date_time.iso8601_basic }}"
  register: provision_result

- name: Wait for provisioning to complete
  uri:
    url: "https://hypervisor.example.com:8080/api/v1/status/{{ provision_result.json.job_id }}"
    client_cert: "/path/to/client.crt"
    client_key: "/path/to/client.key"
    ca_path: "/path/to/ca.crt"
  register: status_result
  until: status_result.json.status in ['completed', 'failed']
  retries: 30
  delay: 10
```

## Integration with Terraform

Use Terraform to manage VM provisioning:

```hcl
provider "libvirt" {
  uri = "qemu:///system"
}

resource "null_resource" "provision_volume" {
  provisioner "local-exec" {
    command = <<-EOT
      curl -X POST https://hypervisor.example.com:8080/api/v1/provision \
        --cacert ${var.ca_cert} \
        --cert ${var.client_cert} \
        --key ${var.client_key} \
        -H "Content-Type: application/json" \
        -d '{
          "image_url": "${var.image_url}",
          "volume_name": "${var.volume_name}",
          "volume_size_gb": ${var.volume_size_gb},
          "image_type": "qcow2",
          "correlation_id": "${var.correlation_id}"
        }' > /tmp/provision_response.json
    EOT
  }
}
```

## Integration with Kubernetes

Deploy the provisioner as a sidecar or separate service in Kubernetes:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: vm-provisioner
spec:
  containers:
  - name: libvirt-volume-provisioner
    image: ghcr.io/rossigee/libvirt-volume-provisioner:v0.3.0
    ports:
    - containerPort: 8080
      name: http
    - containerPort: 8443
      name: https
    env:
    - name: MINIO_ENDPOINT
      value: "https://minio.example.com"
    - name: MINIO_ACCESS_KEY
      valueFrom:
        secretKeyRef:
          name: minio-credentials
          key: access-key
    - name: MINIO_SECRET_KEY
      valueFrom:
        secretKeyRef:
          name: minio-credentials
          key: secret-key
    volumeMounts:
    - name: libvirt
      mountPath: /var/run/libvirt
    - name: lvm
      mountPath: /dev/mapper
    - name: tls
      mountPath: /etc/tls
  volumes:
  - name: libvirt
    hostPath:
      path: /var/run/libvirt
  - name: lvm
    hostPath:
      path: /dev/mapper
  - name: tls
    secret:
      secretName: provisioner-tls
```

## Custom Integration Example

Create a custom client library to integrate with your application:

```python
import requests
import json
import time
from pathlib import Path

class LibvirtProvisioner:
    def __init__(self, base_url, cert_path, key_path, ca_path):
        self.base_url = base_url
        self.cert = (str(cert_path), str(key_path))
        self.ca = str(ca_path)

    def provision_volume(self, image_url, volume_name, volume_size_gb, correlation_id=None):
        """Start a provisioning job"""
        payload = {
            "image_url": image_url,
            "volume_name": volume_name,
            "volume_size_gb": volume_size_gb,
            "image_type": "qcow2"
        }
        if correlation_id:
            payload["correlation_id"] = correlation_id

        response = requests.post(
            f"{self.base_url}/api/v1/provision",
            json=payload,
            cert=self.cert,
            verify=self.ca
        )
        response.raise_for_status()
        return response.json()["job_id"]

    def get_status(self, job_id):
        """Get job status"""
        response = requests.get(
            f"{self.base_url}/api/v1/status/{job_id}",
            cert=self.cert,
            verify=self.ca
        )
        response.raise_for_status()
        return response.json()

    def wait_for_completion(self, job_id, timeout=3600):
        """Wait for job to complete"""
        start_time = time.time()
        while True:
            status = self.get_status(job_id)
            if status["status"] in ["completed", "failed"]:
                return status
            if time.time() - start_time > timeout:
                raise TimeoutError(f"Job {job_id} timed out after {timeout}s")
            time.sleep(5)

    def cancel_job(self, job_id):
        """Cancel a running job"""
        response = requests.delete(
            f"{self.base_url}/api/v1/cancel/{job_id}",
            cert=self.cert,
            verify=self.ca
        )
        response.raise_for_status()

# Usage
provisioner = LibvirtProvisioner(
    base_url="https://hypervisor.example.com:8080",
    cert_path=Path("/etc/provisioner/client.crt"),
    key_path=Path("/etc/provisioner/client.key"),
    ca_path=Path("/etc/provisioner/ca.crt")
)

job_id = provisioner.provision_volume(
    image_url="https://minio.example.com/images/ubuntu-20.04.qcow2",
    volume_name="my-vm-root",
    volume_size_gb=50,
    correlation_id="my-provision-123"
)

status = provisioner.wait_for_completion(job_id)
print(f"Job completed: {status}")
```

## Event Webhooks (Future)

Future versions may support webhooks for event notifications:

```bash
# Register webhook for job completion events
curl -X POST https://hypervisor.example.com:8080/api/v1/webhooks \
  -H "Authorization: Bearer TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "event": "job.completed",
    "url": "https://myapp.example.com/provisioner-webhook",
    "secret": "webhook-signing-secret"
  }'
```

