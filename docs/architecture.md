# Architecture

## System Design

The `libvirt-volume-provisioner` is a critical component in a complete VM deployment system. This document explains how it fits into the larger infrastructure.

### Complete Workflow

```
1. IMAGE PREPARATION
   User/CI Pipeline
        ↓
   Build VM Image (cloud-init enabled)
        ↓
   Upload to MinIO Bucket
   (ubuntu-20.04.qcow2 + .sha256 checksum)

2. VM DEFINITION
   Infrastructure-as-Code (Terraform/Ansible/etc)
        ↓
   Define VM in libvirtd:
   - vCPU, Memory, Network
   - Root volume attachment (empty or placeholder)
   - Cloud-init user-data config

3. ROOT VOLUME PROVISIONING ← libvirt-volume-provisioner starts here
   Infrastructure Automation
        ↓
   Call: POST /api/v1/provision
   - Image URL: MinIO bucket location
   - Volume: LVM device for root disk
   - Size: Desired disk size
        ↓
   Wait for provisioning to complete
   (Check cache → Download → Populate LVM volume)

4. VM STARTUP
   Infrastructure Automation
        ↓
   Start VM via libvirtd
        ↓
   Cloud-init runs (first boot)
   - Reads user-data configuration
   - Provisions VM with desired state:
     * User accounts
     * SSH keys
     * Packages
     * Configuration management setup
   - Configures networking
   - Runs custom provisioning scripts
        ↓
   VM fully operational

5. SUBSEQUENT REPROVISIONING
   To reprovision existing VM:
   ↓
   Shut down VM
   ↓
   Call: POST /api/v1/provision (same volume)
   - Volume is reused (size validated)
   - Image re-populated with fresh base
   ↓
   Start VM
   ↓
   Cloud-init re-provisions with new user-data
```

### Component Interaction Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│ Infrastructure Orchestration (infrastructure-builder, etc)       │
└──────────────────────┬──────────────────────────────────────────┘
                       │
        ┌──────────────┼──────────────┬──────────────┐
        │              │              │              │
        ▼              ▼              ▼              ▼
    ┌────────┐   ┌──────────┐   ┌─────────┐   ┌─────────────────┐
    │ MinIO  │   │ libvirtd │   │   LVM   │   │ Cloud-Init      │
    │Bucket  │   │ VM Mgmt  │   │Volumes  │   │Configuration   │
    │        │   │          │   │         │   │                 │
    │Images  │   │ VM Defs  │   │ Storage │   │ User-data       │
    └────────┘   └──────────┘   └─────────┘   │ Provisioning    │
        ▲              ▲              ▲         └─────────────────┘
        │              │              │              ▲
        │              └──────────────┼──────────────┘
        │                             │
        └─────────────────────────────┼───────────────────┐
                                      │                   │
                        ┌─────────────▼──────────────┐    │
                        │ libvirt-volume-provisioner │    │
                        │                            │    │
                        │ • Check cache              │    │
                        │ • Download images          │    │
                        │ • Populate LVM volumes     │    │
                        │ • Convert QCOW2 → RAW     │    │
                        └────────────────────────────┘    │
                                      ▲                   │
                                      │                   │
                                      └───────────────────┘
                              Infrastructure API Calls
```

### Key Design Concepts

1. **Image Immutability**: Base images in MinIO never change; reprovisioning gets fresh copy
2. **Idempotent Provisioning**: Cloud-init ensures VM reaches desired state regardless of history
3. **Volume Reuse**: Same LVM volume can be repopulated multiple times (for reprovisioning)
4. **Separation of Concerns**:
   - MinIO: Stores base images
   - libvirtd: Manages VM lifecycle and resources
   - libvirt-volume-provisioner: Bridges the gap (populates volumes from images)
   - Cloud-init: Final configuration and customization

## Core Architecture

```
Client (infrastructure-builder)
    ↓ HTTP API
libvirt-volume-provisioner (daemon)
    ↓ Check Cache & Download
MinIO (.sha256 checksums) → libvirt Pool Cache → LVM Volume
    ↓
VM Definition → libvirt → Running VM
```

## Image Caching

The provisioner implements intelligent image caching with compression preservation:

- **Checksum-based caching**: Uses SHA256 checksums from MinIO `.sha256` files as cache keys
- **Compression-preserving storage**: Images are cached as plain files in `/var/lib/libvirt/images/`, preserving QCOW2 compression instead of expanding to raw format
- **Cache directory**: Managed by libvirt's `images` storage pool
- **Fallback behavior**: Falls back to URL-based caching if checksums aren't available
- **Cache validation**: Verifies cached images against checksums before use
- **Storage efficiency**: Cached QCOW2 images remain compressed, significantly reducing disk space usage

### Cache Hit Workflow

When the same image is requested multiple times:

1. First provisioning request: Image is downloaded from MinIO, cached with compression preserved
2. Second provisioning request: Cached image is found via checksum, no download needed
3. Result: 50-70% faster provisioning for cache hits, with 50-70% less storage consumed

