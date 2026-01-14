// Package minio provides MinIO client functionality for the libvirt-volume-provisioner,
// including image download operations and progress tracking.
package minio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/rossigee/libvirt-volume-provisioner/internal/retry"
	"github.com/sirupsen/logrus"
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

// NewClient creates a new MinIO client.
func NewClient() (*Client, error) {
	endpoint := os.Getenv("MINIO_ENDPOINT")
	if endpoint == "" {
		endpoint = "https://minio.example.com"
	}

	accessKey := os.Getenv("MINIO_ACCESS_KEY")
	if accessKey == "" {
		// Also check for AWS/MinIO standard variable name
		accessKey = os.Getenv("MINIO_ACCESS_KEY_ID")
	}

	secretKey := os.Getenv("MINIO_SECRET_KEY")
	if secretKey == "" {
		// Also check for AWS/MinIO standard variable name
		secretKey = os.Getenv("MINIO_SECRET_ACCESS_KEY")
	}

	// Debug logging for environment variables
	logrus.WithFields(logrus.Fields{
		"MINIO_ENDPOINT":              os.Getenv("MINIO_ENDPOINT"),
		"MINIO_ACCESS_KEY_set":        os.Getenv("MINIO_ACCESS_KEY") != "",
		"MINIO_ACCESS_KEY_ID_set":     os.Getenv("MINIO_ACCESS_KEY_ID") != "",
		"MINIO_SECRET_KEY_set":        os.Getenv("MINIO_SECRET_KEY") != "",
		"MINIO_SECRET_ACCESS_KEY_set": os.Getenv("MINIO_SECRET_ACCESS_KEY") != "",
		"accessKey_found":             accessKey != "",
		"secretKey_found":             secretKey != "",
	}).Debug("MinIO environment variable check")

	if accessKey == "" {
		return nil, fmt.Errorf(
			"MINIO_ACCESS_KEY or MINIO_ACCESS_KEY_ID environment variable is required " +
				"(check /etc/default/libvirt-volume-provisioner)")
	}

	if secretKey == "" {
		return nil, fmt.Errorf(
			"MINIO_SECRET_KEY or MINIO_SECRET_ACCESS_KEY environment variable is required " +
				"(check /etc/default/libvirt-volume-provisioner)")
	}

	// Parse endpoint URL
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid MINIO_ENDPOINT '%s': %w (expected format: https://hostname:port)", endpoint, err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("invalid MINIO_ENDPOINT scheme '%s': must be http or https", u.Scheme)
	}

	if u.Host == "" {
		return nil, fmt.Errorf("invalid MINIO_ENDPOINT '%s': missing hostname", endpoint)
	}

	// Create MinIO client
	minioClient, err := minio.New(u.Host, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: u.Scheme == "https",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create MinIO client for %s: %w", u.Host, err)
	}

	// Configure retry logic
	retryConfig := parseRetryConfig(
		os.Getenv("MINIO_RETRY_ATTEMPTS"),
		os.Getenv("MINIO_RETRY_BACKOFF_MS"),
	)

	return &Client{
		minioClient: minioClient,
		retryConfig: retryConfig,
	}, nil
}

// parseRetryConfig parses retry configuration from environment variables
func parseRetryConfig(attemptsStr, backoffStr string) retry.Config {
	// Default values
	maxAttempts := 3
	delays := []time.Duration{100 * time.Millisecond, 1 * time.Second, 10 * time.Second}

	// Parse max attempts
	if attemptsStr != "" {
		if attempts, err := strconv.Atoi(attemptsStr); err == nil && attempts > 0 {
			maxAttempts = attempts
		}
	}

	// Parse backoff delays
	if backoffStr != "" {
		var parsedDelays []time.Duration
		for _, delayStr := range strings.Split(backoffStr, ",") {
			if ms, err := strconv.Atoi(strings.TrimSpace(delayStr)); err == nil && ms > 0 {
				parsedDelays = append(parsedDelays, time.Duration(ms)*time.Millisecond)
			}
		}
		if len(parsedDelays) > 0 {
			delays = parsedDelays
		}
	}

	return retry.Config{
		MaxAttempts: maxAttempts,
		Delays:      delays,
	}
}

// DownloadImage downloads an image from MinIO to a temporary file with exponential backoff retry
func (c *Client) DownloadImage(ctx context.Context, imageURL string, updater ProgressUpdater) (string, error) {
	var tempPath string

	// Wrap download with retry logic
	err := retry.WithRetry(ctx, c.retryConfig, func() error {
		path, downloadErr := c.downloadImageOnce(ctx, imageURL, updater)
		tempPath = path
		return downloadErr
	})
	if err != nil {
		return "", fmt.Errorf("failed to download image from %s after retries: %w", imageURL, err)
	}

	return tempPath, nil
}

// DownloadImageToPath downloads an image from MinIO to a specific file path with exponential backoff retry
func (c *Client) DownloadImageToPath(ctx context.Context, imageURL, destPath string, updater ProgressUpdater) error {
	// Wrap download with retry logic
	err := retry.WithRetry(ctx, c.retryConfig, func() error {
		return c.downloadImageToPathOnce(ctx, imageURL, destPath, updater)
	})
	if err != nil {
		return fmt.Errorf("failed to download image from %s to %s after retries: %w", imageURL, destPath, err)
	}

	return nil
}

// downloadImageToPathOnce performs a single download attempt to a specific path
// without retry logic
func (c *Client) downloadImageToPathOnce(ctx context.Context, imageURL, destPath string,
	updater ProgressUpdater) error {
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

	// Get object info for size
	objInfo, err := c.minioClient.StatObject(ctx, bucketName, objectName, minio.StatObjectOptions{})
	if err != nil {
		return fmt.Errorf("failed to stat object: %w", err)
	}

	totalSize := objInfo.Size

	// Validate destination path
	if strings.Contains(destPath, "..") || !strings.HasPrefix(destPath, "/var/lib/libvirt/") {
		return fmt.Errorf("invalid destination path: %s", destPath)
	}

	// Create or truncate destination file
	destFile, err := os.Create(destPath) // #nosec G304 -- Path validated above
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer func() {
		_ = destFile.Close() // Close errors are not critical
	}()

	// Download object with progress tracking
	object, err := c.minioClient.GetObject(ctx, bucketName, objectName, minio.GetObjectOptions{})
	if err != nil {
		return fmt.Errorf("failed to get object: %w", err)
	}
	defer func() {
		_ = object.Close() // Close errors are not critical
	}()

	// Copy with progress tracking
	buffer := make([]byte, 32*1024*1024) // 32MB buffer
	var downloaded int64

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

			// Update progress
			if updater != nil && totalSize > 0 {
				percent := float64(downloaded) / float64(totalSize) * 30 // 30% of total progress
				updater.UpdateProgress("downloading", 10+percent, downloaded, totalSize)
			}
		}

		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read from MinIO: %w", err)
		}
	}

	// Verify download
	if downloaded != totalSize {
		return fmt.Errorf("download incomplete: got %d bytes, expected %d", downloaded, totalSize)
	}

	return nil
}

// downloadImageOnce performs a single download attempt without retry logic
func (c *Client) downloadImageOnce(ctx context.Context, imageURL string, updater ProgressUpdater) (string, error) {
	// Parse the image URL to extract bucket and object
	u, err := url.Parse(imageURL)
	if err != nil {
		return "", fmt.Errorf("invalid image URL: %w", err)
	}

	// Extract bucket and object from path
	pathParts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(pathParts) < 2 {
		return "", fmt.Errorf("invalid image URL path: %s", u.Path)
	}

	bucketName := pathParts[0]
	objectName := strings.Join(pathParts[1:], "/")

	// Create temporary file
	tempFile, err := os.CreateTemp("", "provision-image-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	defer func() {
		_ = tempFile.Close() // Close errors are not critical
	}()

	tempPath := tempFile.Name()

	// Get object info for size
	objInfo, err := c.minioClient.StatObject(ctx, bucketName, objectName, minio.StatObjectOptions{})
	if err != nil {
		_ = os.Remove(tempPath) // Cleanup errors are not critical
		return "", fmt.Errorf("failed to stat object: %w", err)
	}

	totalSize := objInfo.Size

	// Download object with progress tracking
	object, err := c.minioClient.GetObject(ctx, bucketName, objectName, minio.GetObjectOptions{})
	if err != nil {
		_ = os.Remove(tempPath) // Cleanup errors are not critical
		return "", fmt.Errorf("failed to get object: %w", err)
	}
	defer func() {
		_ = object.Close() // Close errors are not critical
	}()

	// Copy with progress tracking
	buffer := make([]byte, 32*1024*1024) // 32MB buffer
	var downloaded int64

	for {
		select {
		case <-ctx.Done():
			_ = os.Remove(tempPath) // Cleanup errors are not critical
			return "", fmt.Errorf("context cancelled: %w", ctx.Err())
		default:
		}

		n, err := object.Read(buffer)
		if n > 0 {
			if _, writeErr := tempFile.Write(buffer[:n]); writeErr != nil {
				_ = os.Remove(tempPath) // Cleanup errors are not critical
				return "", fmt.Errorf("failed to write to temp file: %w", writeErr)
			}
			downloaded += int64(n)

			// Update progress
			if updater != nil && totalSize > 0 {
				percent := float64(downloaded) / float64(totalSize) * 30 // 30% of total progress
				updater.UpdateProgress("downloading", 10+percent, downloaded, totalSize)
			}
		}

		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			_ = os.Remove(tempPath) // Cleanup errors are not critical
			return "", fmt.Errorf("failed to read from MinIO: %w", err)
		}
	}

	// Verify download
	if downloaded != totalSize {
		_ = os.Remove(tempPath) // Cleanup errors are not critical
		return "", fmt.Errorf("download incomplete: got %d bytes, expected %d", downloaded, totalSize)
	}

	return tempPath, nil
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

// GetObjectContent gets the content of a small object from MinIO
func (c *Client) GetObjectContent(ctx context.Context, bucketName, objectName string) ([]byte, error) {
	object, err := c.minioClient.GetObject(ctx, bucketName, objectName, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get MinIO object: %w", err)
	}
	defer func() { _ = object.Close() }()

	content, err := io.ReadAll(object)
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
