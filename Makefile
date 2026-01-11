.PHONY: build clean test lint docker-build docker-run deb

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod

# Main package
MAIN_PACKAGE=cmd/provisioner
BINARY_NAME=libvirt-volume-provisioner
BINARY_UNIX=$(BINARY_NAME)_unix

# Debian package parameters
DEB_NAME=libvirt-volume-provisioner
DEB_VERSION ?= 0.2.3
DEB_ARCH=amd64
DEB_BUILD_DIR=deb-build

# Build the binary
build:
	$(GOMOD) tidy
	$(GOBUILD) -o $(BINARY_NAME) -v ./$(MAIN_PACKAGE)

# Build for Linux
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOBUILD) -ldflags "-X main.version=$(DEB_VERSION) -X 'main.buildTime=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")'" -o $(BINARY_UNIX) -v ./$(MAIN_PACKAGE)

# Test
test:
	$(GOTEST) -v ./...

# Lint
lint:
	golangci-lint run

# Docker build
docker-build:
	docker build -t libvirt-volume-provisioner .

# Docker run
docker-run:
	docker run -p 8080:8080 --env-file .env libvirt-volume-provisioner

# Build Debian package
deb: build-linux
	@echo "Building Debian package..."
	@rm -rf $(DEB_BUILD_DIR)
	@mkdir -p $(DEB_BUILD_DIR)/DEBIAN
	@mkdir -p $(DEB_BUILD_DIR)/usr/bin
	@mkdir -p $(DEB_BUILD_DIR)/etc/libvirt-volume-provisioner
	@mkdir -p $(DEB_BUILD_DIR)/lib/systemd/system
	@mkdir -p $(DEB_BUILD_DIR)/usr/share/doc/$(DEB_NAME)

	# Copy binary
	cp $(BINARY_UNIX) $(DEB_BUILD_DIR)/usr/bin/$(BINARY_NAME)

	# Create control file
	@echo "Package: $(DEB_NAME)" > $(DEB_BUILD_DIR)/DEBIAN/control
	@echo "Version: $(DEB_VERSION)" >> $(DEB_BUILD_DIR)/DEBIAN/control
	@echo "Section: admin" >> $(DEB_BUILD_DIR)/DEBIAN/control
	@echo "Priority: optional" >> $(DEB_BUILD_DIR)/DEBIAN/control
	@echo "Architecture: $(DEB_ARCH)" >> $(DEB_BUILD_DIR)/DEBIAN/control
	@echo "Depends: libc6 (>= 2.4)" >> $(DEB_BUILD_DIR)/DEBIAN/control
	@echo "Maintainer: Your Name <your.email@example.com>" >> $(DEB_BUILD_DIR)/DEBIAN/control
	@echo "Description: Daemon service for provisioning LVM volumes with VM images on libvirt hypervisor hosts" >> $(DEB_BUILD_DIR)/DEBIAN/control
	@echo " This service provides an HTTP API for downloading VM images from MinIO" >> $(DEB_BUILD_DIR)/DEBIAN/control
	@echo " object storage, converting QCOW2 images to raw format, and populating" >> $(DEB_BUILD_DIR)/DEBIAN/control
	@echo " LVM volumes with VM disk data." >> $(DEB_BUILD_DIR)/DEBIAN/control

	# Create systemd service file
	@echo "[Unit]" > $(DEB_BUILD_DIR)/lib/systemd/system/$(DEB_NAME).service
	@echo "Description=Libvirt Volume Provisioner" >> $(DEB_BUILD_DIR)/lib/systemd/system/$(DEB_NAME).service
	@echo "After=network.target" >> $(DEB_BUILD_DIR)/lib/systemd/system/$(DEB_NAME).service
	@echo "" >> $(DEB_BUILD_DIR)/lib/systemd/system/$(DEB_NAME).service
	@echo "[Service]" >> $(DEB_BUILD_DIR)/lib/systemd/system/$(DEB_NAME).service
	@echo "Type=simple" >> $(DEB_BUILD_DIR)/lib/systemd/system/$(DEB_NAME).service
	@echo "User=libvirt-volume-provisioner" >> $(DEB_BUILD_DIR)/lib/systemd/system/$(DEB_NAME).service
	@echo "Group=libvirt-volume-provisioner" >> $(DEB_BUILD_DIR)/lib/systemd/system/$(DEB_NAME).service
	@echo "EnvironmentFile=/etc/default/libvirt-volume-provisioner" >> $(DEB_BUILD_DIR)/lib/systemd/system/$(DEB_NAME).service
	@echo "ExecStart=/usr/bin/$(BINARY_NAME)" >> $(DEB_BUILD_DIR)/lib/systemd/system/$(DEB_NAME).service
	@echo "Restart=always" >> $(DEB_BUILD_DIR)/lib/systemd/system/$(DEB_NAME).service
	@echo "RestartSec=5" >> $(DEB_BUILD_DIR)/lib/systemd/system/$(DEB_NAME).service
	@echo "" >> $(DEB_BUILD_DIR)/lib/systemd/system/$(DEB_NAME).service
	@echo "[Install]" >> $(DEB_BUILD_DIR)/lib/systemd/system/$(DEB_NAME).service
	@echo "WantedBy=multi-user.target" >> $(DEB_BUILD_DIR)/lib/systemd/system/$(DEB_NAME).service

	# Create postinst script
	@echo "#!/bin/bash" > $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "set -e" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "# Create user if it doesn't exist" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "if ! id -u libvirt-volume-provisioner > /dev/null 2>&1; then" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "    useradd --system --shell /bin/false libvirt-volume-provisioner" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "fi" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "# Create database directory" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "mkdir -p /var/lib/libvirt-volume-provisioner" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "chown libvirt-volume-provisioner:libvirt-volume-provisioner /var/lib/libvirt-volume-provisioner" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "chmod 700 /var/lib/libvirt-volume-provisioner" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "# Create environment file if it doesn't exist" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "if [ ! -f /etc/default/libvirt-volume-provisioner ]; then" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "    cat > /etc/default/libvirt-volume-provisioner << EOF" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "PORT=8080" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "HOST=0.0.0.0" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "MINIO_ENDPOINT=https://minio.example.com" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "MINIO_ACCESS_KEY=your-access-key" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "MINIO_SECRET_KEY=your-secret-key" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "MINIO_RETRY_ATTEMPTS=3" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "MINIO_RETRY_BACKOFF_MS=100,1000,10000" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "# LVM configuration" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "LVM_VOLUME_GROUP=data" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "LVM_RETRY_ATTEMPTS=2" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "LVM_RETRY_BACKOFF_MS=100,1000" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "# Database configuration" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "DB_PATH=/var/lib/libvirt-volume-provisioner/jobs.db" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "EOF" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "    chmod 600 /etc/default/libvirt-volume-provisioner" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "    chown root:root /etc/default/libvirt-volume-provisioner" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "fi" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "# Set permissions" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "chown libvirt-volume-provisioner:libvirt-volume-provisioner /usr/bin/$(BINARY_NAME)" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "chmod 755 /usr/bin/$(BINARY_NAME)" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "# Reload systemd and enable service" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "systemctl daemon-reload" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "systemctl enable $(DEB_NAME)" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@chmod 755 $(DEB_BUILD_DIR)/DEBIAN/postinst

	# Create prerm script
	@echo "#!/bin/bash" > $(DEB_BUILD_DIR)/DEBIAN/prerm
	@echo "set -e" >> $(DEB_BUILD_DIR)/DEBIAN/prerm
	@echo "" >> $(DEB_BUILD_DIR)/DEBIAN/prerm
	@echo "# Stop and disable service" >> $(DEB_BUILD_DIR)/DEBIAN/prerm
	@echo "systemctl stop $(DEB_NAME) || true" >> $(DEB_BUILD_DIR)/DEBIAN/prerm
	@echo "systemctl disable $(DEB_NAME) || true" >> $(DEB_BUILD_DIR)/DEBIAN/prerm
	@chmod 755 $(DEB_BUILD_DIR)/DEBIAN/prerm

	# Create copyright file
	@echo "Format: https://www.debian.org/doc/packaging-manuals/copyright-format/1.0/" > $(DEB_BUILD_DIR)/usr/share/doc/$(DEB_NAME)/copyright
	@echo "Upstream-Name: $(DEB_NAME)" >> $(DEB_BUILD_DIR)/usr/share/doc/$(DEB_NAME)/copyright
	@echo "Source: https://github.com/rossigee/libvirt-volume-provisioner" >> $(DEB_BUILD_DIR)/usr/share/doc/$(DEB_NAME)/copyright
	@echo "" >> $(DEB_BUILD_DIR)/usr/share/doc/$(DEB_NAME)/copyright
	@echo "Files: *" >> $(DEB_BUILD_DIR)/usr/share/doc/$(DEB_NAME)/copyright
	@echo "Copyright: $(shell date +%Y) Ross Gee" >> $(DEB_BUILD_DIR)/usr/share/doc/$(DEB_NAME)/copyright
	@echo "License: MIT" >> $(DEB_BUILD_DIR)/usr/share/doc/$(DEB_NAME)/copyright

	# Build the package
	dpkg-deb --build $(DEB_BUILD_DIR) $(DEB_NAME)_$(DEB_VERSION)_$(DEB_ARCH).deb

	# Clean up
	@rm -rf $(DEB_BUILD_DIR)

	@echo "Debian package created: $(DEB_NAME)_$(DEB_VERSION)_$(DEB_ARCH).deb"

# Clean
clean:
	$(GOCLEAN)
	rm -f $(BINARY_NAME)
	rm -f $(BINARY_UNIX)
	rm -f $(DEB_NAME)_$(DEB_VERSION)_$(DEB_ARCH).deb
	rm -rf $(DEB_BUILD_DIR)

# Run
run:
	$(GOBUILD) -o $(BINARY_NAME) -v ./$(MAIN_PACKAGE)
	./$(BINARY_NAME)

# Dependencies
deps:
	$(GOMOD) download
	$(GOGET) github.com/gin-gonic/gin
	$(GOGET) github.com/google/uuid
	$(GOGET) github.com/minio/minio-go/v7