package libvirt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// TestCheckCache_ReturnsStoredChecksumContent verifies that CheckCache returns the content
// of the .sha256 file as Checksum, not the cache key used to locate the file.
// This is the regression test for the bug where cacheKey was returned instead of the stored
// checksum, which broke stale-cache detection when the URL-hash key differs from the image hash.
func TestCheckCache_ReturnsStoredChecksumContent(t *testing.T) {
	tmpDir := t.TempDir()
	pm := &PoolManager{poolPath: tmpDir}

	cacheKey := "urlhash_stable_filename_key"
	storedChecksum := "a1b2c3d4e5f6789012345678901234567890123456789012345678901234abcd"

	require.NotEqual(t, cacheKey, storedChecksum, "test requires key and content to differ")

	imagePath := filepath.Join(tmpDir, cacheKey)
	require.NoError(t, os.WriteFile(imagePath, []byte("fake image data"), 0o600))
	require.NoError(t, os.WriteFile(imagePath+".sha256", []byte(storedChecksum), 0o600))

	cache, err := pm.CheckCache(cacheKey)

	assert.NoError(t, err)
	assert.NotNil(t, cache)
	assert.Equal(t, storedChecksum, cache.Checksum, "Checksum must be file content, not the cache key")
	assert.NotEqual(t, cacheKey, cache.Checksum, "must not return the lookup key as the checksum")
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

func TestCacheKeyConsistency(t *testing.T) {
	tmpDir := t.TempDir()
	pm := &PoolManager{
		poolPath: tmpDir,
	}

	checksum := "a1b2c3d4e5f6789012345678901234567890123456789012345678901234abcd"

	imagePath, err := pm.AllocateImageFile(checksum)
	assert.NoError(t, err)
	assert.Equal(t, filepath.Join(tmpDir, checksum), imagePath)

	err = os.WriteFile(imagePath, []byte("fake image data"), 0600)
	assert.NoError(t, err)

	err = pm.CreateCacheEntry(imagePath, checksum)
	assert.NoError(t, err)

	cache, err := pm.CheckCache(checksum)
	assert.NoError(t, err)
	assert.NotNil(t, cache, "Cache should find the image using the same checksum key")
	assert.Equal(t, imagePath, cache.Path)
	assert.Equal(t, checksum, cache.Checksum)
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

func TestEvictExpiredImages_ListError(t *testing.T) {
	// Point poolPath at a file (not a directory) so ReadDir fails inside ListCachedImages
	tmpDir := t.TempDir()
	notADir := filepath.Join(tmpDir, "notadir")
	require.NoError(t, os.WriteFile(notADir, []byte("x"), 0o600))

	pm := &PoolManager{poolPath: notADir}

	_, err := pm.EvictExpiredImages(24 * time.Hour)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "eviction")
}

func TestEvictExpiredImages(t *testing.T) {
	t.Run("evicts old entries, keeps new ones", func(t *testing.T) {
		tmpDir := t.TempDir()
		pm := &PoolManager{poolPath: tmpDir}

		oldKey := "oldcachedimage"
		newKey := "newcachedimage"

		for _, key := range []string{oldKey, newKey} {
			imgPath := filepath.Join(tmpDir, key)
			require.NoError(t, os.WriteFile(imgPath, []byte("data"), 0o600))
			require.NoError(t, os.WriteFile(imgPath+".sha256", []byte(key), 0o600))
		}

		// Backdate the old entry's sidecar to 8 days ago
		oldTime := time.Now().Add(-8 * 24 * time.Hour)
		require.NoError(t, os.Chtimes(filepath.Join(tmpDir, oldKey+".sha256"), oldTime, oldTime))

		evicted, err := pm.EvictExpiredImages(7 * 24 * time.Hour)
		assert.NoError(t, err)
		assert.Equal(t, 1, evicted)

		_, err = os.Stat(filepath.Join(tmpDir, oldKey))
		assert.True(t, os.IsNotExist(err), "old image should be deleted")

		_, err = os.Stat(filepath.Join(tmpDir, newKey))
		assert.NoError(t, err, "new image should survive")
	})

	t.Run("empty cache returns zero evictions", func(t *testing.T) {
		tmpDir := t.TempDir()
		pm := &PoolManager{poolPath: tmpDir}

		evicted, err := pm.EvictExpiredImages(24 * time.Hour)
		assert.NoError(t, err)
		assert.Equal(t, 0, evicted)
	})

	t.Run("no entries old enough leaves cache unchanged", func(t *testing.T) {
		tmpDir := t.TempDir()
		pm := &PoolManager{poolPath: tmpDir}

		key := "recentimage"
		imgPath := filepath.Join(tmpDir, key)
		require.NoError(t, os.WriteFile(imgPath, []byte("data"), 0o600))
		require.NoError(t, os.WriteFile(imgPath+".sha256", []byte(key), 0o600))

		evicted, err := pm.EvictExpiredImages(24 * time.Hour)
		assert.NoError(t, err)
		assert.Equal(t, 0, evicted)

		_, err = os.Stat(imgPath)
		assert.NoError(t, err, "recent image should not be evicted")
	})
}
