#!/bin/bash
# Golder Tech Debian Repository Setup Script
# This script automates the setup of the Golder Tech Debian repository

set -e

REPO_URL="https://debs.golder.tech"
KEY_URL="${REPO_URL}/gpg-key.asc"
KEYRING_PATH="/usr/share/keyrings/golder-tech-archive-keyring.gpg"
SOURCES_FILE="/etc/apt/sources.list.d/golder-tech.list"

echo "🔐 Golder Tech Debian Repository Setup"
echo "======================================"

# Check if running as root or with sudo
if [[ $EUID -ne 0 ]]; then
   echo "❌ This script must be run as root or with sudo"
   exit 1
fi

echo "📥 Downloading and importing GPG key..."
if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$KEY_URL" | gpg --dearmor | tee "$KEYRING_PATH" > /dev/null
elif command -v wget >/dev/null 2>&1; then
    wget -qO- "$KEY_URL" | gpg --dearmor | tee "$KEYRING_PATH" > /dev/null
else
    echo "❌ Neither curl nor wget is available. Please install one of them."
    exit 1
fi

echo "✅ GPG key imported successfully"

echo "📝 Adding repository to sources..."
cat > "$SOURCES_FILE" << EOF
deb [signed-by=$KEYRING_PATH] $REPO_URL stable main
EOF

echo "✅ Repository added to $SOURCES_FILE"

echo "🔄 Updating package lists..."
apt update

echo "✅ Repository setup complete!"
echo ""
echo "🚀 You can now install packages with:"
echo "   apt install libvirt-volume-provisioner"
echo ""
echo "📖 For more information, see: REPOSITORY-README.md"