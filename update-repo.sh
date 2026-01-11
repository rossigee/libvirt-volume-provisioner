#!/bin/bash
set -e

# Script to update Debian repository with new package
# Usage: ./update-repo.sh <deb-file>

DEB_FILE="$1"

if [ -z "$DEB_FILE" ]; then
    echo "Usage: $0 <deb-file>"
    exit 1
fi

if [ ! -f "$DEB_FILE" ]; then
    echo "Error: DEB file '$DEB_FILE' does not exist"
    exit 1
fi

# We're already in the repo directory (called from workflow)
mkdir -p "pool/main"
mkdir -p "dists/stable/main/binary-amd64"

echo "Copying $DEB_FILE to pool/main/"
cp "$DEB_FILE" "pool/main/"

if [ ! -f "pool/main/$(basename "$DEB_FILE")" ]; then
    echo "Error: Failed to copy DEB file to repository"
    exit 1
fi

# Generate Packages file
dpkg-scanpackages pool/main /dev/null > dists/stable/main/binary-amd64/Packages

# Compress Packages
gzip -9c dists/stable/main/binary-amd64/Packages > dists/stable/main/binary-amd64/Packages.gz

# Generate Release file with proper hashes
cat > dists/stable/Release << EOF
Origin: Golder Tech
Label: Golder Tech Debian Repository
Suite: stable
Codename: stable
Version: 0.1
Architectures: amd64
Components: main
Description: Golder Tech Debian Repository
Date: $(date -R)
EOF

# Add MD5Sum, SHA1, SHA256 entries
echo "MD5Sum:" >> dists/stable/Release
for file in dists/stable/main/binary-amd64/Packages*; do
    if [ -f "$file" ]; then
        size=$(stat -c%s "$file")
        md5=$(md5sum "$file" | cut -d' ' -f1)
        path=${file#dists/}
        printf " %s %8d %s\n" "$md5" "$size" "$path" >> dists/stable/Release
    fi
done

echo "SHA1:" >> dists/stable/Release
for file in dists/stable/main/binary-amd64/Packages*; do
    if [ -f "$file" ]; then
        size=$(stat -c%s "$file")
        sha1=$(sha1sum "$file" | cut -d' ' -f1)
        path=${file#dists/}
        printf " %s %8d %s\n" "$sha1" "$size" "$path" >> dists/stable/Release
    fi
done

echo "SHA256:" >> dists/stable/Release
for file in dists/stable/main/binary-amd64/Packages*; do
    if [ -f "$file" ]; then
        size=$(stat -c%s "$file")
        sha256=$(sha256sum "$file" | cut -d' ' -f1)
        path=${file#dists/}
        printf " %s %8d %s\n" "$sha256" "$size" "$path" >> dists/stable/Release
    fi
done

        # Sign Release file (skip for now - using trusted repo)
        # gpg --detach-sign --armor --sign --default-key 20879EBE6582F6BF1506DE02DB5CF7EA238FE114 dists/stable/Release

echo "Repository updated successfully"