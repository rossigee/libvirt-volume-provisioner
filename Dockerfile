# Build stage
FROM golang:1.25.5-alpine AS builder

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o libvirt-volume-provisioner ./cmd/provisioner

# Final stage
FROM alpine:latest

# Install ca-certificates for HTTPS requests
RUN apk --no-cache add ca-certificates

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