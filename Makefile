.PHONY: build build-linux clean test lint run deps deb \
        docker-build docker-run build-docker build-docker-dev \
        docker-compose-up docker-compose-down docker-compose-logs \
        integration-up integration-down integration-logs integration-test integration-clean \
        deploy install-systemd uninstall-systemd help

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
DEB_VERSION ?= 0.11.4
DEB_ARCH ?= $(shell dpkg --print-architecture 2>/dev/null || echo amd64)
DEB_BUILD_DIR=deb-build

# Help
.PHONY: help
help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# Build the binary
build: ## Build binary for current platform
	$(GOMOD) tidy
	$(GOBUILD) -o $(BINARY_NAME) -v ./$(MAIN_PACKAGE)

# Build for Linux (native architecture)
build-linux: ## Build binary for current Linux architecture (used by deb target)
	CGO_ENABLED=1 CGO_CFLAGS="-Wno-discarded-qualifiers" GOOS=linux $(GOBUILD) -tags libsqlite3 -ldflags "-X main.version=$(DEB_VERSION) -X 'main.buildTime=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")'" -o $(BINARY_UNIX) -v ./$(MAIN_PACKAGE)

# Test
test: ## Run unit tests
	$(GOTEST) -v ./...

# Lint
lint: ## Run golangci-lint
	golangci-lint run

# Docker build
docker-build: ## Build development Docker image
	docker build -t libvirt-volume-provisioner .

# Docker run
docker-run: ## Run service via Docker using .env file
	docker run -p 8080:8080 --env-file .env libvirt-volume-provisioner

# Build Debian package
deb: build-linux ## Build Debian .deb package
	@echo "Building Debian package..."
	@rm -rf $(DEB_BUILD_DIR)
	@mkdir -p $(DEB_BUILD_DIR)/DEBIAN
	@mkdir -p $(DEB_BUILD_DIR)/usr/bin
	@mkdir -p $(DEB_BUILD_DIR)/etc/libvirt-volume-provisioner
	@mkdir -p $(DEB_BUILD_DIR)/lib/systemd/system
	@mkdir -p $(DEB_BUILD_DIR)/usr/share/doc/$(DEB_NAME)

	# Copy binary
	cp $(BINARY_UNIX) $(DEB_BUILD_DIR)/usr/bin/$(BINARY_NAME)

	# Copy backup script
	mkdir -p $(DEB_BUILD_DIR)/usr/local/bin
	cp scripts/backup-db.sh $(DEB_BUILD_DIR)/usr/local/bin/$(DEB_NAME)-backup
	chmod 755 $(DEB_BUILD_DIR)/usr/local/bin/$(DEB_NAME)-backup

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

	# Copy systemd service file
	cp libvirt-volume-provisioner.service $(DEB_BUILD_DIR)/lib/systemd/system/$(DEB_NAME).service

	# Create database backup service
	@echo "[Unit]" > $(DEB_BUILD_DIR)/lib/systemd/system/$(DEB_NAME)-backup.service
	@echo "Description=Libvirt Volume Provisioner Database Backup" >> $(DEB_BUILD_DIR)/lib/systemd/system/$(DEB_NAME)-backup.service
	@echo "" >> $(DEB_BUILD_DIR)/lib/systemd/system/$(DEB_NAME)-backup.service
	@echo "[Service]" >> $(DEB_BUILD_DIR)/lib/systemd/system/$(DEB_NAME)-backup.service
	@echo "Type=oneshot" >> $(DEB_BUILD_DIR)/lib/systemd/system/$(DEB_NAME)-backup.service
	@echo "ExecStart=/usr/local/bin/$(DEB_NAME)-backup" >> $(DEB_BUILD_DIR)/lib/systemd/system/$(DEB_NAME)-backup.service

	# Create database backup timer
	@echo "[Unit]" > $(DEB_BUILD_DIR)/lib/systemd/system/$(DEB_NAME)-backup.timer
	@echo "Description=Daily Libvirt Volume Provisioner Database Backup" >> $(DEB_BUILD_DIR)/lib/systemd/system/$(DEB_NAME)-backup.timer
	@echo "" >> $(DEB_BUILD_DIR)/lib/systemd/system/$(DEB_NAME)-backup.timer
	@echo "[Timer]" >> $(DEB_BUILD_DIR)/lib/systemd/system/$(DEB_NAME)-backup.timer
	@echo "OnCalendar=daily" >> $(DEB_BUILD_DIR)/lib/systemd/system/$(DEB_NAME)-backup.timer
	@echo "Persistent=true" >> $(DEB_BUILD_DIR)/lib/systemd/system/$(DEB_NAME)-backup.timer
	@echo "" >> $(DEB_BUILD_DIR)/lib/systemd/system/$(DEB_NAME)-backup.timer
	@echo "[Install]" >> $(DEB_BUILD_DIR)/lib/systemd/system/$(DEB_NAME)-backup.timer
	@echo "WantedBy=timers.target" >> $(DEB_BUILD_DIR)/lib/systemd/system/$(DEB_NAME)-backup.timer

	# Install sudoers rules for unprivileged service user
	# Removed sudoers for v0.9 direct exec

	# Create postinst script
	@echo "#!/bin/bash" > $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "set -e" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "# Create service user if it doesn't exist" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "if ! id -u libvirt-volume-provisioner > /dev/null 2>&1; then" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "    useradd --system --shell /bin/false libvirt-volume-provisioner" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "fi" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "# Create runtime directories" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "mkdir -p /var/lib/libvirt-volume-provisioner" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "chown libvirt-volume-provisioner:libvirt-volume-provisioner /var/lib/libvirt-volume-provisioner" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "chmod 700 /var/lib/libvirt-volume-provisioner" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "mkdir -p /var/log/libvirt-volume-provisioner" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "chown libvirt-volume-provisioner:libvirt-volume-provisioner /var/log/libvirt-volume-provisioner" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "chmod 750 /var/log/libvirt-volume-provisioner" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "mkdir -p /etc/libvirt-volume-provisioner" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "chown root:libvirt-volume-provisioner /etc/libvirt-volume-provisioner" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "chmod 750 /etc/libvirt-volume-provisioner" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "# Fix log file ownership if it exists but is owned by root" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "if [ -f /var/log/libvirt-volume-provisioner/provisioner.log ]; then" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "    chown libvirt-volume-provisioner:libvirt-volume-provisioner /var/log/libvirt-volume-provisioner/provisioner.log" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "fi" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "# Create skeleton config.yaml if not present" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "if [ ! -f /etc/libvirt-volume-provisioner/config.yaml ]; then" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "    cat > /etc/libvirt-volume-provisioner/config.yaml << 'EOF'" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "# libvirt-volume-provisioner configuration" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "# See /usr/share/doc/libvirt-volume-provisioner/configuration.md for all options." >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "server:" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "  port: 8080" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "  api_tokens_file: /etc/libvirt-volume-provisioner/tokens" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "  db_path: /var/lib/libvirt-volume-provisioner/jobs.db" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "minio:" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "  endpoint: https://minio.example.com" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "libvirt:" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "  uri: qemu:///system" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "  pool: images" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "  max_concurrent: 2" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "lvm:" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "  volume_group: vg0" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "logging:" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "  level: info" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "  format: json" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "  file: /var/log/libvirt-volume-provisioner/provisioner.log" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "EOF" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "    chmod 640 /etc/libvirt-volume-provisioner/config.yaml" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "    chown root:libvirt-volume-provisioner /etc/libvirt-volume-provisioner/config.yaml" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "fi" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "# Create credentials env file if not present (holds MINIO_ACCESS_KEY / MINIO_SECRET_KEY only)" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "if [ ! -f /etc/default/libvirt-volume-provisioner ]; then" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "    cat > /etc/default/libvirt-volume-provisioner << 'EOF'" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "# MinIO credentials - keep secrets out of config.yaml" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "MINIO_ACCESS_KEY=your-access-key" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "MINIO_SECRET_KEY=your-secret-key" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "EOF" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "    chmod 600 /etc/default/libvirt-volume-provisioner" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "    chown root:root /etc/default/libvirt-volume-provisioner" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "fi" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "# Set binary permissions" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
	@echo "chown root:libvirt-volume-provisioner /usr/bin/$(BINARY_NAME)" >> $(DEB_BUILD_DIR)/DEBIAN/postinst
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
clean: ## Remove build artefacts and packages
	$(GOCLEAN)
	rm -f $(BINARY_NAME)
	rm -f $(BINARY_UNIX)
	rm -f $(DEB_NAME)_$(DEB_VERSION)_$(DEB_ARCH).deb
	rm -rf $(DEB_BUILD_DIR)

# Run
run: ## Build and run the service locally
	$(GOBUILD) -o $(BINARY_NAME) -v ./$(MAIN_PACKAGE)
	./$(BINARY_NAME)

# Dependencies
deps: ## Download Go module dependencies
	$(GOMOD) download
	$(GOGET) github.com/gin-gonic/gin
	$(GOGET) github.com/google/uuid
	$(GOGET) github.com/minio/minio-go/v7

# Docker targets
build-docker: ## Build production Docker image
	docker build -f Dockerfile.production -t $(BINARY_NAME):latest .

build-docker-dev: ## Build development Docker image
	docker build -f Dockerfile -t $(BINARY_NAME):dev .

docker-compose-up: ## Start with docker-compose
	docker compose up -d

docker-compose-down: ## Stop docker-compose
	docker compose down

docker-compose-logs: ## Show docker-compose logs
	docker compose logs -f

# Integration testing targets
integration-up: ## Start integration test environment
	cd integration && docker compose -f docker-compose.test.yml up -d

integration-down: ## Stop integration test environment
	cd integration && docker compose -f docker-compose.test.yml down -v

integration-logs: ## Show integration test logs
	cd integration && docker compose -f docker-compose.test.yml logs -f

integration-test: ## Run integration tests
	cd integration && docker compose -f docker-compose.test.yml run --rm integration-tests

integration-clean: ## Clean up integration test environment
	cd integration && docker compose -f docker-compose.test.yml down -v --rmi local

# Deploy to VM hosts
deploy: ## Deploy .deb package to configured VM hosts
	@echo "Deploying $(DEB_NAME)_$(DEB_VERSION)_$(DEB_ARCH).deb to VM hosts..."
	@./scripts/deploy-to-hosts.sh $(DEB_NAME)_$(DEB_VERSION)_$(DEB_ARCH).deb

# Systemd targets
install-systemd: ## Install systemd service files
	sudo cp systemd/$(BINARY_NAME).service /etc/systemd/system/
	sudo cp systemd/$(BINARY_NAME).socket /etc/systemd/system/
	sudo cp systemd/$(BINARY_NAME).default /etc/default/$(BINARY_NAME)
	sudo systemctl daemon-reload
	@echo "Systemd files installed. Run 'sudo systemctl enable $(BINARY_NAME).socket && sudo systemctl start $(BINARY_NAME).socket' to start the service."

uninstall-systemd: ## Remove systemd service files
	sudo systemctl stop $(BINARY_NAME).socket || true
	sudo systemctl stop $(BINARY_NAME).service || true
	sudo systemctl disable $(BINARY_NAME).socket || true
	sudo systemctl disable $(BINARY_NAME).service || true
	sudo rm -f /etc/systemd/system/$(BINARY_NAME).service
	sudo rm -f /etc/systemd/system/$(BINARY_NAME).socket
	sudo rm -f /etc/default/$(BINARY_NAME)
	sudo systemctl daemon-reload
	@echo "Systemd files removed."
