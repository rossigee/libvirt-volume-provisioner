#!/bin/bash
set -e

# Manual deployment script for libvirt-volume-provisioner .deb package
# Usage: B2_KEY_ID=key B2_APPLICATION_KEY=key ./deploy-deb.sh

# Check for required environment variables
if [ -z "$B2_KEY_ID" ] || [ -z "$B2_APPLICATION_KEY" ]; then
    echo "Error: B2_KEY_ID and B2_APPLICATION_KEY environment variables must be set"
    echo "Usage: B2_KEY_ID=your-key B2_APPLICATION_KEY=your-secret ./deploy-deb.sh"
    exit 1
fi

DEB_FILE="libvirt-volume-provisioner_0.1.0_amd64.deb"
BUCKET="debs-golder-tech-static"

echo "Configuring B2 access..."
mcli alias set b2 https://s3.us-west-002.backblazeb2.com "$B2_KEY_ID" "$B2_APPLICATION_KEY"

echo "Downloading existing repository..."
mkdir -p repo
mcli cp --recursive "b2/$BUCKET/" repo/

echo "Adding new package to repository..."
./update-repo.sh "$DEB_FILE"

echo "Uploading updated repository..."
cd repo
mcli cp --recursive . "b2/$BUCKET/"

echo "Deployment complete!"
echo "Package available at: https://debs.golder.tech"
echo "Test with: apt update && apt install libvirt-volume-provisioner"