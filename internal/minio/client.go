// Package minio provides MinIO client functionality for the libvirt-volume-provisioner,
// including image download operations and progress tracking.
package minio

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/rossigee/libvirt-volume-provisioner/internal/config"
	"github.com/rossigee/libvirt-volume-provisioner/internal/retry"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// ProgressUpdater interface for updating job progress.
type ProgressUpdater interface {
	UpdateProgress(stage string, percent float64, bytesProcessed, bytesTotal int64)
}

// Client handles MinIO operations.
type Client struct {
	minioClient *minio.Client
	retryConfig retry.Config
}

// NewClient creates a new MinIO client from the provided configuration.
func NewClient(cfg config.MinIOConfig) (*Client, error) {
	if cfg.AccessKey == "" {
		return nil, fmt.Errorf("minio access_key is required (set via config or MINIO_ACCESS_KEY env var)")
	}
	if cfg.SecretKey == "" {
		return nil, fmt.Errorf("minio secret_key is required (set via config or MINIO_SECRET_KEY env var)")
	}

	logrus.WithFields(logrus.Fields{
		"endpoint":         cfg.Endpoint,
		"access_key_set":   cfg.AccessKey != "",
		"secret_key_set":   cfg.SecretKey != "",
	}).Debug("Initialising MinIO client")

	u, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid minio endpoint %q: %w", cfg.Endpoint, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("invalid minio endpoint scheme %q: must be http or https", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("invalid minio endpoint %q: missing hostname", cfg.Endpoint)
	}

	options := &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: u.Scheme == "https",
	}

	if u.Scheme == "https" {
		tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
		if cfg.CACert != "" {
			//nolint:gosec // path is admin-controlled via config file
			caCert, err := os.ReadFile(cfg.CACert)
			if err != nil {
				return nil, fmt.Errorf("failed to read minio ca_cert %q: %w", cfg.CACert, err)
			}
			caPool := x509.NewCertPool()
			if !caPool.AppendCertsFromPEM(caCert) {
				return nil, fmt.Errorf("failed to parse minio ca_cert %q: no valid PEM blocks", cfg.CACert)
			}
			tlsConfig.RootCAs = caPool
		}
		options.Transport = &http.Transport{TLSClientConfig: tlsConfig}
	}

	minioClient, err := minio.New(u.Host, options)
	if err != nil {
		return nil, fmt.Errorf("failed to create MinIO client for %s: %w", u.Host, err)
	}

	_, err = minioClient.ListBuckets(context.Background())
	if err != nil {
		logrus.WithError(err).Warn("Failed to list buckets - MinIO connection test failed")
	} else {
		logrus.Info("MinIO connection test successful")
	}

	return &Client{
		minioClient: minioClient,
		retryConfig: retryConfigFrom(cfg.RetryAttempts, cfg.RetryBackoffMS),
	}, nil
}

func retryConfigFrom(attempts int, backoffMS []int) retry.Config {
	delays := make([]time.Duration, len(backoffMS))
	for i, ms := range backoffMS {
		delays[i] = time.Duration(ms) * time.Millisecond
	}
	return retry.Config{
		MaxAttempts: attempts,
		Delays:      delays,
	}
}

// DownloadImageToPath downloads an image from MinIO to a specific file path with exponential backoff retry
func (c *Client) DownloadImageToPath(ctx context.Context, imageURL, destPath string, updater ProgressUpdater) error {
	// Start span for MinIO download
	tracer := otel.Tracer("libvirt-volume-provisioner")
	ctx, span := tracer.Start(ctx, "DownloadImageToPath",
		trace.WithAttributes(
			attribute.String("minio.image_url", imageURL),
			attribute.String("minio.dest_path", destPath)))
	defer span.End()

	// Wrap download with retry logic
	err := retry.WithRetry(ctx, c.retryConfig, func() error {
		return c.downloadImageToPathOnce(ctx, imageURL, destPath, updater)
	})
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("failed to download image from %s to %s after retries: %w", imageURL, destPath, err)
	}

	span.SetStatus(codes.Ok, "download completed successfully")
	return nil
}

// downloadImageToPathOnce performs a single download attempt to a specific path
// without retry logic
func (c *Client) downloadImageToPathOnce(ctx context.Context, imageURL, destPath string,
	updater ProgressUpdater) error {
	// Report connecting stage
	if updater != nil {
		updater.UpdateProgress("connecting", 0, 0, 0)
	}

	// Parse the image URL to extract bucket and object
	u, err := url.Parse(imageURL)
	if err != nil {
		return fmt.Errorf("invalid image URL: %w", err)
	}

	// Extract bucket and object from path
	pathParts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(pathParts) < 2 {
		return fmt.Errorf("invalid image URL path: %s", u.Path)
	}

	bucketName := pathParts[0]
	objectName := strings.Join(pathParts[1:], "/")

	// Report fetching metadata stage
	if updater != nil {
		updater.UpdateProgress("connecting", 50, 0, 0)
	}

	// Get object info for size
	objInfo, err := c.minioClient.StatObject(ctx, bucketName, objectName, minio.StatObjectOptions{})
	if err != nil {
		return fmt.Errorf("failed to stat object: %w", err)
	}

	totalSize := objInfo.Size

	// Destination path is always provided by AllocateImageFile (poolPath + hex cache key);
	// reject traversal as a belt-and-suspenders guard.
	if strings.Contains(destPath, "..") {
		return fmt.Errorf("invalid destination path: %s", destPath)
	}

	// Create or truncate destination file
	destFile, err := os.Create(destPath) // #nosec G304 -- path comes from AllocateImageFile
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer func() {
		_ = destFile.Close() // Close errors are not critical
	}()

	// Report starting download
	if updater != nil {
		updater.UpdateProgress("downloading", 0, 0, totalSize)
	}

	// Download object with progress tracking
	object, err := c.minioClient.GetObject(ctx, bucketName, objectName, minio.GetObjectOptions{})
	if err != nil {
		return fmt.Errorf("failed to get object: %w", err)
	}
	defer func() {
		_ = object.Close() // Close errors are not critical
	}()

	// Copy with progress tracking
	buffer := make([]byte, 4*1024*1024) // 4MB buffer for more frequent updates
	var downloaded int64
	lastUpdate := time.Now()
	lastPercent := 0.0
	bytesSinceUpdate := int64(0)
	const minUpdateBytes = 1 * 1024 * 1024           // Update at least every 1MB
	const minUpdateInterval = 200 * time.Millisecond // Update at least every 200ms

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled: %w", ctx.Err())
		default:
		}

		n, err := object.Read(buffer)
		if n > 0 {
			if _, writeErr := destFile.Write(buffer[:n]); writeErr != nil {
				return fmt.Errorf("failed to write to destination file: %w", writeErr)
			}
			downloaded += int64(n)
			bytesSinceUpdate += int64(n)

			// Update progress frequently for smooth percentage increments
			now := time.Now()
			elapsed := now.Sub(lastUpdate)
			currentPercent := float64(downloaded) / float64(totalSize) * 100

			// Update if: enough time passed OR enough bytes transferred OR percentage changed by 0.5%+
			shouldUpdate := updater != nil && totalSize > 0 && (elapsed > minUpdateInterval ||
				bytesSinceUpdate >= minUpdateBytes ||
				currentPercent-lastPercent >= 0.5)

			if shouldUpdate {
				updater.UpdateProgress("downloading", currentPercent, downloaded, totalSize)
				lastUpdate = now
				bytesSinceUpdate = 0
				lastPercent = currentPercent
			}
		}

		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read from MinIO: %w", err)
		}
	}

	// Final progress update
	if updater != nil && totalSize > 0 {
		updater.UpdateProgress("downloading", 100, downloaded, totalSize)
	}

	// Verify download
	if downloaded != totalSize {
		return fmt.Errorf("download incomplete: got %d bytes, expected %d", downloaded, totalSize)
	}

	return nil
}

// Cleanup removes a temporary file
func (c *Client) Cleanup(tempPath string) error {
	if tempPath != "" {
		err := os.Remove(tempPath)
		if err != nil {
			return fmt.Errorf("failed to cleanup temp file: %w", err)
		}
	}
	return nil
}

// StatObject gets object information from MinIO
func (c *Client) StatObject(ctx context.Context, bucketName, objectName string) (minio.ObjectInfo, error) {
	objInfo, err := c.minioClient.StatObject(ctx, bucketName, objectName, minio.StatObjectOptions{})
	if err != nil {
		return objInfo, fmt.Errorf("failed to stat MinIO object: %w", err)
	}
	return objInfo, nil
}

// GetObjectContent gets the content of a small object from MinIO (max 64KB).
func (c *Client) GetObjectContent(ctx context.Context, bucketName, objectName string) ([]byte, error) {
	const maxObjectSize = 64 * 1024

	object, err := c.minioClient.GetObject(ctx, bucketName, objectName, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get MinIO object: %w", err)
	}
	defer func() { _ = object.Close() }()

	content, err := io.ReadAll(io.LimitReader(object, maxObjectSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read MinIO object content: %w", err)
	}

	return content, nil
}

// ValidateImageURL validates that an image URL is accessible
func (c *Client) ValidateImageURL(ctx context.Context, imageURL string) error {
	u, err := url.Parse(imageURL)
	if err != nil {
		return fmt.Errorf("invalid image URL: %w", err)
	}

	pathParts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(pathParts) < 2 {
		return fmt.Errorf("invalid image URL path: %s", u.Path)
	}

	bucketName := pathParts[0]
	objectName := strings.Join(pathParts[1:], "/")

	// Check if object exists
	_, err = c.minioClient.StatObject(ctx, bucketName, objectName, minio.StatObjectOptions{})
	if err != nil {
		return fmt.Errorf("image not accessible: %w", err)
	}

	return nil
}
