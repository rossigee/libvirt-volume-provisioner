#!/bin/bash
# Database backup script for libvirt-volume-provisioner
set -e

BACKUP_DIR="/var/backups/libvirt-volume-provisioner"
DB_PATH="/var/lib/libvirt-volume-provisioner/jobs.db"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
BACKUP_FILE="${BACKUP_DIR}/jobs_${TIMESTAMP}.db"

# Create backup directory if it doesn't exist
sudo mkdir -p "$BACKUP_DIR"

# Create backup
sudo cp "$DB_PATH" "$BACKUP_FILE"

# Set proper permissions
sudo chown root:root "$BACKUP_FILE"
sudo chmod 600 "$BACKUP_FILE"

# Clean up old backups (keep last 30 days)
sudo find "$BACKUP_DIR" -name "jobs_*.db" -mtime +30 -delete

echo "Database backup created: $BACKUP_FILE"
echo "Old backups cleaned up (kept last 30 days)"