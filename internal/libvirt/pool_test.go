package libvirt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAllocateImageFile(t *testing.T) {
	tests := []struct {
		expectError      bool
		expectPathSuffix string
		imageName        string
		name             string
	}{
		{
			name:             "simple image name",
			imageName:        "ubuntu_20_04_qcow2",
			expectError:      false,
			expectPathSuffix: "ubuntu_20_04_qcow2",
		},
		{
			name:             "image name with extension",
			imageName:        "debian_11_qcow2.img",
			expectError:      false,
			expectPathSuffix: "debian_11_qcow2.img",
		},
		{
			name:             "image name with spaces",
			imageName:        "my image name",
			expectError:      false,
			expectPathSuffix: "my image name",
		},
		{
			name:             "empty image name",
			imageName:        "",
			expectError:      false,
			expectPathSuffix: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary pool directory
			tmpDir := t.TempDir()
			pm := &PoolManager{
				poolPath: tmpDir,
			}

			// Test AllocateImageFile
			imagePath, err := pm.AllocateImageFile(tt.imageName)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotEmpty(t, imagePath)
				assert.True(t, filepath.IsAbs(imagePath), "Path should be absolute")
				if tt.expectPathSuffix != "" {
					assert.True(t, strings.HasPrefix(imagePath, tmpDir), "Path should be under pool directory")
					assert.Equal(t, tt.imageName, filepath.Base(imagePath))
				}
			}
		})
	}
}

func TestAllocateImageFileCreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	nonExistentPool := filepath.Join(tmpDir, "cache", "images")

	pm := &PoolManager{
		poolPath: nonExistentPool,
	}

	// Verify directory doesn't exist yet
	_, err := os.Stat(nonExistentPool)
	require.True(t, os.IsNotExist(err), "Directory should not exist initially")

	// Allocate image file
	imagePath, err := pm.AllocateImageFile("test_image")

	// Verify directory was created
	assert.NoError(t, err)
	assert.NotEmpty(t, imagePath)
	info, err := os.Stat(nonExistentPool)
	assert.NoError(t, err)
	assert.True(t, info.IsDir(), "Directory should be created")
}

func TestCheckCacheCacheHit(t *testing.T) {
	tmpDir := t.TempDir()
	pm := &PoolManager{
		poolPath: tmpDir,
	}

	checksum := "abc123def456"
	imagePath := filepath.Join(tmpDir, checksum)
	checksumFile := imagePath + ".sha256"

	// Create image file first, then checksum file (checksum points to image by convention)
	require.NoError(t, os.WriteFile(imagePath, []byte("fake image data"), 0o600))
	require.NoError(t, os.WriteFile(checksumFile, []byte(checksum), 0o600))

	// Test cache hit
	cache, err := pm.CheckCache(checksum)

	assert.NoError(t, err)
	assert.NotNil(t, cache)
	assert.Equal(t, imagePath, cache.Path)
	assert.Equal(t, checksum, cache.Checksum)
	assert.Greater(t, cache.Size, uint64(0))
}

func TestCheckCacheMiss(t *testing.T) {
	tmpDir := t.TempDir()
	pm := &PoolManager{
		poolPath: tmpDir,
	}

	// Test cache miss - no checksum file exists
	cache, err := pm.CheckCache("nonexistent_checksum")

	assert.NoError(t, err)
	assert.Nil(t, cache)
}

func TestCheckCacheOrphanedChecksumFile(t *testing.T) {
	tmpDir := t.TempDir()
	pm := &PoolManager{
		poolPath: tmpDir,
	}

	checksum := "orphaned_checksum"
	checksumFile := filepath.Join(tmpDir, checksum+".sha256")

	// Create checksum file but NO image file
	require.NoError(t, os.WriteFile(checksumFile, []byte(checksum), 0600))

	// Test orphaned checksum detection
	cache, err := pm.CheckCache(checksum)

	// Should return nil (not an error) for orphaned checksums
	assert.NoError(t, err)
	assert.Nil(t, cache)
}

func TestCheckCacheImageFileSizeAccuracy(t *testing.T) {
	tmpDir := t.TempDir()
	pm := &PoolManager{
		poolPath: tmpDir,
	}

	checksum := "size_test_checksum"
	imagePath := filepath.Join(tmpDir, checksum)
	checksumFile := imagePath + ".sha256"

	imageData := make([]byte, 5*1024*1024) // 5MB
	require.NoError(t, os.WriteFile(imagePath, imageData, 0o600))
	require.NoError(t, os.WriteFile(checksumFile, []byte(checksum), 0o600))

	// Test that size is correctly reported
	cache, err := pm.CheckCache(checksum)

	assert.NoError(t, err)
	assert.NotNil(t, cache)
	assert.Equal(t, uint64(5*1024*1024), cache.Size)
}

func TestCheckCacheCreatesMissingDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	nonExistentPool := filepath.Join(tmpDir, "missing", "cache")

	pm := &PoolManager{
		poolPath: nonExistentPool,
	}

	// Should not error even if directory doesn't exist
	cache, err := pm.CheckCache("any_checksum")

	assert.NoError(t, err)
	assert.Nil(t, cache)

	// Directory should be created
	info, err := os.Stat(nonExistentPool)
	assert.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestCreateCacheEntry(t *testing.T) {
	tmpDir := t.TempDir()
	pm := &PoolManager{
		poolPath: tmpDir,
	}

	imagePath := filepath.Join(tmpDir, "test_image")
	checksum := "test_checksum_value"

	// Create the image file first
	require.NoError(t, os.WriteFile(imagePath, []byte("image data"), 0o600))

	// Create cache entry
	err := pm.CreateCacheEntry(imagePath, checksum)

	assert.NoError(t, err)

	// Verify checksum file was created
	checksumFile := imagePath + ".sha256"
	//nolint:gosec // checksumFile is constructed from controlled imagePath in test
	data, err := os.ReadFile(checksumFile)
	assert.NoError(t, err)
	assert.Equal(t, checksum, string(data))
}

func TestGetImageNameFromURL(t *testing.T) {
	tests := []struct {
		expectedName string
		imageURL     string
		name         string
	}{
		{
			expectedName: "ubuntu_20_04",
			imageURL:     "https://minio.example.com/bucket/ubuntu-20.04.qcow2",
			name:         "simple QCOW2 URL",
		},
		{
			expectedName: "debian_11_0",
			imageURL:     "https://minio.example.com/bucket/debian.11.0.raw",
			name:         "URL with multiple dots",
		},
		{
			expectedName: "centos_8_stream",
			imageURL:     "https://minio.example.com/bucket/centos-8-stream.img",
			name:         "URL with dashes",
		},
		{
			expectedName: "image",
			imageURL:     "https://minio.example.com/bucket/image",
			name:         "URL with no extension",
		},
		{
			expectedName: "ubuntu",
			imageURL:     "https://minio.example.com/bucket/images/v1.0/ubuntu.qcow2",
			name:         "URL with path components",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := GetImageNameFromURL(tt.imageURL)
			assert.Equal(t, tt.expectedName, name)
		})
	}
}

func TestDeleteImage(t *testing.T) {
	tmpDir := t.TempDir()
	pm := &PoolManager{
		poolPath: tmpDir,
	}

	imagePath := filepath.Join(tmpDir, "test_image")
	checksumPath := imagePath + ".sha256"

	// Create image and checksum files
	require.NoError(t, os.WriteFile(imagePath, []byte("image data"), 0o600))
	require.NoError(t, os.WriteFile(checksumPath, []byte("checksum"), 0o600))

	// Verify files exist
	_, err := os.Stat(imagePath)
	require.NoError(t, err)
	_, err = os.Stat(checksumPath)
	require.NoError(t, err)

	// Delete image
	err = pm.DeleteImage(imagePath)
	assert.NoError(t, err)

	// Verify both files are deleted
	_, err = os.Stat(imagePath)
	assert.True(t, os.IsNotExist(err), "Image file should be deleted")
	_, err = os.Stat(checksumPath)
	assert.True(t, os.IsNotExist(err), "Checksum file should be deleted")
}

func TestDeleteImageNonExistent(t *testing.T) {
	tmpDir := t.TempDir()
	pm := &PoolManager{
		poolPath: tmpDir,
	}

	imagePath := filepath.Join(tmpDir, "nonexistent_image")

	// Deleting non-existent file should not error
	err := pm.DeleteImage(imagePath)
	assert.NoError(t, err)
}

func TestCalculateChecksum(t *testing.T) {
	// CalculateChecksum validates that file path is under /var/lib/libvirt/
	// For testing, we create a test file under that path structure
	// This test verifies the function works with valid paths

	// Note: CalculateChecksum has path validation that prevents testing with arbitrary temp directories
	// The function correctly rejects paths outside /var/lib/libvirt/ for security
	// This test is skipped as it would require running with elevated privileges
	t.Skip("CalculateChecksum requires files under /var/lib/libvirt/, skipping in unit tests")
}

func TestCalculateChecksumPathTraversal(t *testing.T) {
	// Attempt to use path traversal should fail
	checksum, err := CalculateChecksum("../../../etc/passwd")
	assert.Error(t, err)
	assert.Empty(t, checksum)
	assert.Contains(t, err.Error(), "invalid file path")
}

func TestCalculateChecksumNonExistent(t *testing.T) {
	checksum, err := CalculateChecksum("/var/lib/libvirt/nonexistent_file")
	assert.Error(t, err)
	assert.Empty(t, checksum)
}
