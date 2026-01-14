//go:build integration
// +build integration

package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// TestSuite holds the integration test suite
type TestSuite struct {
	suite.Suite
	minioClient *minio.Client
	provisioner *ProvisionerClient
	testBucket  string
	testImages  []string
}

// ProvisionerClient handles HTTP communication with the provisioner
type ProvisionerClient struct {
	baseURL    string
	httpClient *http.Client
}

func (pc *ProvisionerClient) ProvisionVolume(req ProvisionRequest) (*ProvisionResponse, error) {
	jsonData, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	resp, err := pc.httpClient.Post(pc.baseURL+"/api/v1/provision", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	var response ProvisionResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}

	return &response, nil
}

func (pc *ProvisionerClient) GetJobStatus(jobID string) (*StatusResponse, error) {
	resp, err := pc.httpClient.Get(pc.baseURL + "/api/v1/status/" + jobID)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var response StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}

	return &response, nil
}

func (pc *ProvisionerClient) WaitForCompletion(jobID string, timeout time.Duration) (*StatusResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timeout waiting for job completion")
		case <-ticker.C:
			status, err := pc.GetJobStatus(jobID)
			if err != nil {
				return nil, err
			}

			if status.Status == "completed" || status.Status == "failed" {
				return status, nil
			}
		}
	}
}

// SetupSuite initializes the test suite
func (suite *TestSuite) SetupSuite() {
	suite.T().Log("Setting up integration test suite...")

	// Initialize MinIO client
	endpoint := os.Getenv("TEST_MINIO_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://localhost:9000"
	}

	accessKey := os.Getenv("TEST_MINIO_ACCESS_KEY")
	if accessKey == "" {
		accessKey = "testminio"
	}

	secretKey := os.Getenv("TEST_MINIO_SECRET_KEY")
	if secretKey == "" {
		secretKey = "testminio123"
	}

	minioClient, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: strings.HasPrefix(endpoint, "https://"),
	})
	require.NoError(suite.T(), err, "Failed to create MinIO client")
	suite.minioClient = minioClient

	// Create test bucket
	suite.testBucket = "test-vm-images-" + fmt.Sprintf("%d", time.Now().Unix())
	err = minioClient.MakeBucket(context.Background(), suite.testBucket, minio.MakeBucketOptions{})
	require.NoError(suite.T(), err, "Failed to create test bucket")

	// Initialize provisioner client
	provisionerURL := os.Getenv("TEST_PROVISIONER_URL")
	if provisionerURL == "" {
		provisionerURL = "http://localhost:8080"
	}

	suite.provisioner = &ProvisionerClient{
		baseURL: provisionerURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}

	// Create test images
	suite.setupTestImages()
}

// TearDownSuite cleans up the test suite
func (suite *TestSuite) TearDownSuite() {
	suite.T().Log("Cleaning up integration test suite...")

	// Remove test bucket and contents
	if suite.minioClient != nil && suite.testBucket != "" {
		// Remove all objects first
		objectsCh := make(chan minio.ObjectInfo)
		go func() {
			defer close(objectsCh)
			for object := range suite.minioClient.ListObjects(context.Background(), suite.testBucket, minio.ListObjectsOptions{}) {
				if object.Err != nil {
					continue
				}
				objectsCh <- object
			}
		}()

		for object := range objectsCh {
			err := suite.minioClient.RemoveObject(context.Background(), suite.testBucket, object.Key, minio.RemoveObjectOptions{})
			if err != nil {
				suite.T().Logf("Failed to remove object %s: %v", object.Key, err)
			}
		}

		// Remove bucket
		err := suite.minioClient.RemoveBucket(context.Background(), suite.testBucket)
		if err != nil {
			suite.T().Logf("Failed to remove test bucket: %v", err)
		}
	}
}

// setupTestImages creates test QCOW2 images in MinIO
func (suite *TestSuite) setupTestImages() {
	suite.T().Log("Creating test QCOW2 images...")

	// Create a small test QCOW2 image (1GB sparse)
	testImagePath := "/tmp/test-image.qcow2"
	defer os.Remove(testImagePath)

	// Create a basic QCOW2 image using qemu-img (if available)
	// For now, create a dummy file to simulate the image
	dummyContent := make([]byte, 1024*1024) // 1MB dummy content
	for i := range dummyContent {
		dummyContent[i] = byte(i % 256)
	}

	// Upload test images with different sizes
	imageSizes := []int64{
		100 * 1024 * 1024,  // 100MB
		500 * 1024 * 1024,  // 500MB
		1024 * 1024 * 1024, // 1GB
	}

	for i, size := range imageSizes {
		objectName := fmt.Sprintf("ubuntu-20.04-test-%d.qcow2", i+1)
		suite.testImages = append(suite.testImages, objectName)

		// Create content of specified size
		content := make([]byte, size)
		_, err := rand.Read(content)
		require.NoError(suite.T(), err, "Failed to generate test content")

		// Calculate SHA256 checksum
		hash := sha256.Sum256(content)
		checksum := fmt.Sprintf("%x", hash)

		// Upload to MinIO
		reader := bytes.NewReader(content)
		_, err = suite.minioClient.PutObject(context.Background(), suite.testBucket, objectName, reader, size, minio.PutObjectOptions{})
		require.NoError(suite.T(), err, "Failed to upload test image")

		// Upload checksum file
		checksumContent := []byte(checksum)
		checksumReader := bytes.NewReader(checksumContent)
		checksumName := objectName + ".sha256"
		_, err = suite.minioClient.PutObject(context.Background(), suite.testBucket, checksumName, checksumReader, int64(len(checksumContent)), minio.PutObjectOptions{})
		require.NoError(suite.T(), err, "Failed to upload checksum file")

		suite.T().Logf("Uploaded test image: %s (%s)", objectName, checksum)
	}
}

// TestFullProvisioningWorkflow tests the complete volume provisioning process
func (suite *TestSuite) TestFullProvisioningWorkflow() {
	suite.T().Log("Testing full provisioning workflow...")

	// Use the first test image
	imageURL := fmt.Sprintf("http://minio:9000/%s/%s", suite.testBucket, suite.testImages[0])

	// Submit provisioning request
	req := ProvisionRequest{
		ImageURL:      imageURL,
		VolumeName:    fmt.Sprintf("test-volume-%d", time.Now().Unix()),
		VolumeSizeGB:  10,
		ImageType:     "qcow2",
		CorrelationID: fmt.Sprintf("test-%d", time.Now().Unix()),
	}

	resp, err := suite.provisioner.ProvisionVolume(req)
	require.NoError(suite.T(), err, "Failed to submit provisioning request")
	require.NotEmpty(suite.T(), resp.JobID, "Job ID should not be empty")

	suite.T().Logf("Submitted provisioning job: %s", resp.JobID)

	// Wait for completion (with reasonable timeout)
	status, err := suite.provisioner.WaitForCompletion(resp.JobID, 10*time.Minute)
	require.NoError(suite.T(), err, "Failed to wait for job completion")

	// Verify successful completion
	assert.Equal(suite.T(), "completed", status.Status, "Job should complete successfully")
	assert.NotNil(suite.T(), status.CacheHit, "Cache hit status should be present")
	assert.NotEmpty(suite.T(), status.ImagePath, "Image path should be present")

	suite.T().Logf("Job completed successfully: cache_hit=%v, image_path=%s", *status.CacheHit, status.ImagePath)
}

// TestImageCaching tests that images are properly cached and reused
func (suite *TestSuite) TestImageCaching() {
	suite.T().Log("Testing image caching functionality...")

	imageURL := fmt.Sprintf("http://minio:9000/%s/%s", suite.testBucket, suite.testImages[1])

	// First provisioning (should download and cache)
	req1 := ProvisionRequest{
		ImageURL:      imageURL,
		VolumeName:    fmt.Sprintf("cache-test-1-%d", time.Now().Unix()),
		VolumeSizeGB:  5,
		ImageType:     "qcow2",
		CorrelationID: "cache-test-1",
	}

	resp1, err := suite.provisioner.ProvisionVolume(req1)
	require.NoError(suite.T(), err)

	status1, err := suite.provisioner.WaitForCompletion(resp1.JobID, 5*time.Minute)
	require.NoError(suite.T(), err)
	assert.Equal(suite.T(), "completed", status1.Status)
	assert.False(suite.T(), *status1.CacheHit, "First request should not be a cache hit")

	suite.T().Logf("First provisioning completed: cache_hit=%v", *status1.CacheHit)

	// Second provisioning with same image (should use cache)
	req2 := ProvisionRequest{
		ImageURL:      imageURL,
		VolumeName:    fmt.Sprintf("cache-test-2-%d", time.Now().Unix()),
		VolumeSizeGB:  5,
		ImageType:     "qcow2",
		CorrelationID: "cache-test-2",
	}

	resp2, err := suite.provisioner.ProvisionVolume(req2)
	require.NoError(suite.T(), err)

	status2, err := suite.provisioner.WaitForCompletion(resp2.JobID, 5*time.Minute)
	require.NoError(suite.T(), err)
	assert.Equal(suite.T(), "completed", status2.Status)
	assert.True(suite.T(), *status2.CacheHit, "Second request should be a cache hit")

	suite.T().Logf("Second provisioning completed: cache_hit=%v", *status2.CacheHit)
}

// TestErrorScenarios tests various error conditions
func (suite *TestSuite) TestErrorScenarios() {
	suite.T().Log("Testing error scenarios...")

	// Test with invalid image URL
	req := ProvisionRequest{
		ImageURL:      "http://minio:9000/nonexistent-bucket/nonexistent-image.qcow2",
		VolumeName:    fmt.Sprintf("error-test-%d", time.Now().Unix()),
		VolumeSizeGB:  1,
		ImageType:     "qcow2",
		CorrelationID: "error-test",
	}

	resp, err := suite.provisioner.ProvisionVolume(req)
	require.NoError(suite.T(), err, "Request submission should succeed even with invalid image")

	status, err := suite.provisioner.WaitForCompletion(resp.JobID, 2*time.Minute)
	require.NoError(suite.T(), err)
	assert.Equal(suite.T(), "failed", status.Status, "Job should fail with invalid image")
	assert.NotEmpty(suite.T(), status.Error, "Error message should be present")

	suite.T().Logf("Error scenario test completed: error=%s", status.Error)
}

// TestPerformance benchmarks the provisioning performance
func (suite *TestSuite) TestPerformance() {
	suite.T().Log("Running performance tests...")

	imageURL := fmt.Sprintf("http://minio:9000/%s/%s", suite.testBucket, suite.testImages[2])

	// Measure cold start time (first provisioning)
	start := time.Now()
	req := ProvisionRequest{
		ImageURL:      imageURL,
		VolumeName:    fmt.Sprintf("perf-test-%d", time.Now().Unix()),
		VolumeSizeGB:  2,
		ImageType:     "qcow2",
		CorrelationID: "perf-test",
	}

	resp, err := suite.provisioner.ProvisionVolume(req)
	require.NoError(suite.T(), err)

	status, err := suite.provisioner.WaitForCompletion(resp.JobID, 5*time.Minute)
	require.NoError(suite.T(), err)
	assert.Equal(suite.T(), "completed", status.Status)

	coldStartTime := time.Since(start)
	suite.T().Logf("Cold start provisioning time: %v", coldStartTime)

	// Measure cached provisioning time
	start = time.Now()
	req2 := ProvisionRequest{
		ImageURL:      imageURL,
		VolumeName:    fmt.Sprintf("perf-test-cached-%d", time.Now().Unix()),
		VolumeSizeGB:  2,
		ImageType:     "qcow2",
		CorrelationID: "perf-test-cached",
	}

	resp2, err := suite.provisioner.ProvisionVolume(req2)
	require.NoError(suite.T(), err)

	status2, err := suite.provisioner.WaitForCompletion(resp2.JobID, 2*time.Minute)
	require.NoError(suite.T(), err)
	assert.Equal(suite.T(), "completed", status2.Status)

	cachedTime := time.Since(start)
	suite.T().Logf("Cached provisioning time: %v", cachedTime)

	// Cached provisioning should be significantly faster
	assert.True(suite.T(), cachedTime < coldStartTime/2, "Cached provisioning should be much faster than cold start")
}

// TestMain sets up the integration test environment
func TestMain(m *testing.M) {
	// Wait for services to be ready
	if err := waitForServices(); err != nil {
		fmt.Printf("Failed to wait for services: %v\n", err)
		os.Exit(1)
	}

	// Run tests
	code := m.Run()

	// Cleanup
	os.Exit(code)
}

// waitForServices waits for all dependent services to be ready
func waitForServices() error {
	services := []struct {
		name string
		url  string
	}{
		{"MinIO", "http://localhost:9000/minio/health/live"},
		{"PostgreSQL", "postgres://testuser:testpass@localhost:5432/libvirt_test?sslmode=disable"},
		{"Redis", "redis://localhost:6379"},
	}

	for _, service := range services {
		if err := waitForService(service.name, service.url, 60*time.Second); err != nil {
			return fmt.Errorf("service %s not ready: %w", service.name, err)
		}
	}

	return nil
}

// waitForService waits for a specific service to be ready
func waitForService(name, url string, timeout time.Duration) error {
	client := &http.Client{Timeout: 5 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for %s", name)
		case <-ticker.C:
			resp, err := client.Get(url)
			if err == nil && resp.StatusCode == 200 {
				resp.Body.Close()
				fmt.Printf("%s is ready\n", name)
				return nil
			}
		}
	}
}

// Run the test suite
func TestIntegrationSuite(t *testing.T) {
	suite.Run(t, new(TestSuite))
}
