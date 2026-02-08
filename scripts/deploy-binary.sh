#!/bin/bash
set -e

# Deployment script for libvirt-volume-provisioner binary
# Usage: ./deploy-binary.sh host1 [host2 ...]

if [ $# -eq 0 ]; then
    echo "Usage: $0 host1 [host2 ...]"
    echo "Example: $0 vm-host-01.example.com vm-host-02.example.com"
    exit 1
fi

BINARY="libvirt-volume-provisioner_unix"
SERVICE_NAME="libvirt-volume-provisioner"

echo "Starting deployment of $BINARY to $# hosts..."

for host in "$@"; do
    echo "Deploying to $host..."
    
    # Copy binary to host tmp directory
    scp "$BINARY" "$host:/tmp/"
    
    # Stop service, move binary, set permissions, restart service
    ssh "$host" "sudo systemctl stop $SERVICE_NAME && sudo mv /tmp/$BINARY /usr/bin/libvirt-volume-provisioner && sudo chown libvirt-volume-provisioner:libvirt-volume-provisioner /usr/bin/libvirt-volume-provisioner && sudo chmod 755 /usr/bin/libvirt-volume-provisioner && sudo systemctl start $SERVICE_NAME"
    
    # Check status
    if ssh "$host" "sudo systemctl is-active $SERVICE_NAME" > /dev/null; then
        echo "✅ $host: Deployment successful"
    else
        echo "❌ $host: Service failed to start"
        ssh "$host" "sudo systemctl status $SERVICE_NAME"
    fi
done

echo "Deployment complete!"
