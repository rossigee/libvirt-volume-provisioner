# Build stage
FROM golang:1.26.3-alpine AS builder

# Set working directory
WORKDIR /app

# Install build dependencies required for CGO (libvirt bindings)
RUN apk add --no-cache build-base libvirt-dev

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the binary (CGO required for libvirt-go)
RUN CGO_ENABLED=1 GOOS=linux go build -o libvirt-volume-provisioner ./cmd/provisioner

# Final stage
FROM alpine:latest

# Install ca-certificates and libvirt runtime library
RUN apk --no-cache add ca-certificates libvirt

# Create non-root user
RUN adduser -D -s /bin/sh appuser

WORKDIR /root/

# Copy the binary from builder stage
COPY --from=builder /app/libvirt-volume-provisioner .

# Change ownership
RUN chown appuser:appuser libvirt-volume-provisioner

# Switch to non-root user
USER appuser

# Expose port
EXPOSE 8080

# Run the binary
CMD ["./libvirt-volume-provisioner"]