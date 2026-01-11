# Debian Repository Setup Guide

This guide explains how to add the Golder Tech Debian repository to your system and install packages with GPG signature verification.

## Prerequisites

- Debian-based Linux distribution (Ubuntu, Debian, etc.)
- `curl` or `wget` for downloading files
- `gnupg` for GPG key management (usually pre-installed)

## Step 1: Import the GPG Public Key

First, import the repository's GPG public key to verify package signatures:

```bash
# Option 1: Import directly from URL (replace with actual URL)
curl -fsSL https://debs.golder.tech/gpg-key.asc | gpg --dearmor | sudo tee /usr/share/keyrings/golder-tech-archive-keyring.gpg > /dev/null

# Option 2: Download and import manually
wget -O- https://debs.golder.tech/gpg-key.asc | gpg --dearmor | sudo tee /usr/share/keyrings/golder-tech-archive-keyring.gpg > /dev/null

# Option 3: Manual download and import
wget https://debs.golder.tech/gpg-key.asc
gpg --dearmor golder-tech-gpg-key.asc
sudo mv golder-tech-gpg-key.asc.gpg /usr/share/keyrings/golder-tech-archive-keyring.gpg
```

Verify the key was imported:
```bash
gpg --list-keys --keyring /usr/share/keyrings/golder-tech-archive-keyring.gpg
```

## Step 2: Add the Repository

Create a new sources file:

```bash
sudo tee /etc/apt/sources.list.d/golder-tech.list > /dev/null << EOF
deb [signed-by=/usr/share/keyrings/golder-tech-archive-keyring.gpg] https://debs.golder.tech stable main
EOF
```

## Step 3: Update Package Lists

Update your package index:

```bash
sudo apt update
```

If you encounter GPG verification errors, ensure the key was imported correctly and try again.

## Step 4: Install Packages

Install the libvirt-volume-provisioner package:

```bash
sudo apt install libvirt-volume-provisioner
```

## Troubleshooting

### GPG Key Issues

If you see errors like "The following signatures couldn't be verified":

1. **Verify key import:**
   ```bash
   gpg --list-keys --keyring /usr/share/keyrings/golder-tech-archive-keyring.gpg
   ```

2. **Re-import the key:**
   ```bash
   curl -fsSL https://debs.golder.tech/gpg-key.asc | gpg --dearmor | sudo tee /usr/share/keyrings/golder-tech-archive-keyring.gpg > /dev/null
   sudo apt update
   ```

### Repository Issues

If `apt update` fails:

1. **Check sources file syntax:**
   ```bash
   cat /etc/apt/sources.list.d/golder-tech.list
   ```

2. **Verify repository URL is accessible:**
   ```bash
   curl -I https://debs.golder.tech/dists/stable/Release
   ```

3. **Check for network/firewall issues**

### Package Installation Issues

If package installation fails:

1. **Check package availability:**
   ```bash
   apt search libvirt-volume-provisioner
   ```

2. **Clear apt cache:**
   ```bash
   sudo apt clean && sudo apt update
   ```

## Repository Information

- **Repository URL:** https://debs.golder.tech
- **Suite:** stable
- **Components:** main
- **Architecture:** amd64
- **GPG Key ID:** 20879EBE6582F6BF1506DE02DB5CF7EA238FE114

## Security Notes

- This repository uses GPG signature verification for package integrity
- Always verify GPG key fingerprints when importing keys
- The repository serves packages for libvirt volume provisioning tools

## Support

For issues with this repository:
- Check the [GitHub repository](https://github.com/rossigee/libvirt-volume-provisioner)
- Verify your system meets the prerequisites
- Ensure network connectivity to the repository

## Key Fingerprint

The GPG key has the following fingerprint:
```
2087 9EBE 6582 F6BF 1506 DE02 DB5C F7EA 238F E114
```

Verify this matches when importing the key.