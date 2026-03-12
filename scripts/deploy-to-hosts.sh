#!/bin/bash

DEB_FILE="$1"
HOSTS_FILE="vm-hosts.conf"
LOG_FILE="deploy.log"

if [ -z "$DEB_FILE" ]; then
    echo "Usage: $0 <deb-file>"
    exit 1
fi

if [ ! -f "$DEB_FILE" ]; then
    echo "Error: $DEB_FILE not found"
    exit 1
fi

if [ ! -f "$HOSTS_FILE" ]; then
    echo "Error: $HOSTS_FILE not found"
    exit 1
fi

echo "Deploying $DEB_FILE to hosts in $HOSTS_FILE..."
echo "Logging to $LOG_FILE"

exec > "$LOG_FILE" 2>&1

echo "Contents of $HOSTS_FILE:"
cat "$HOSTS_FILE"
echo "End of file"

while read -r line; do
    echo "Processing line: '$line'"
    # Skip empty lines
    [ -z "$line" ] && continue

    # Parse host and user
    HOST=$(echo "$line" | awk '{print $1}')
    USER=$(echo "$line" | awk '{print $2}')

    if [ -z "$HOST" ] || [ -z "$USER" ]; then
        echo "Warning: Invalid line in $HOSTS_FILE: $line"
        continue
    fi

    echo "Deploying to $USER@$HOST..."

    # Copy the deb file
    if scp "$DEB_FILE" "$USER@$HOST:/tmp/"; then
        echo "Copied $DEB_FILE to $HOST"
    else
        echo "Failed to copy to $HOST, skipping"
        continue
    fi

    # Install the package
    if ssh "$USER@$HOST" "sudo dpkg -i /tmp/$(basename "$DEB_FILE")" >/dev/null 2>&1; then
        echo "Installed package on $HOST"
    else
        echo "Failed to install on $HOST, skipping restart"
        continue
    fi

    # Restart the service
    if ssh "$USER@$HOST" "sudo systemctl daemon-reload && sudo systemctl restart libvirt-volume-provisioner" >/dev/null 2>&1; then
        echo "Restarted service on $HOST"
    else
        echo "Failed to restart service on $HOST"
    fi

    echo "Successfully deployed to $HOST"
done < "$HOSTS_FILE"

echo "Deployment completed."