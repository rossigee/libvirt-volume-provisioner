// Package libvirt provides functionality for managing libvirt storage pools and volumes,
// including image caching and allocation for the libvirt-volume-provisioner.
package libvirt

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/libvirt/libvirt-go"
	"github.com/sirupsen/logrus"
)

// ImageCache represents a cached image in the libvirt storage pool
type ImageCache struct {
	Path     string
	Size     uint64
	Checksum string
}

// PoolManager handles libvirt storage pool operations for image caching
type PoolManager struct {
	conn     *libvirt.Connect
	poolName string
	poolPath string
}

// NewPoolManager creates a new libvirt pool manager
func NewPoolManager(poolName string) (*PoolManager, error) {
	// Connect to libvirt
	conn, err := libvirt.NewConnect("qemu:///system")
	if err != nil {
		return nil, fmt.Errorf("failed to connect to libvirt: %w", err)
	}

	pm := &PoolManager{
		conn:     conn,
		poolName: poolName,
		poolPath: fmt.Sprintf("/var/lib/libvirt/%s", poolName),
	}

	// Ensure the pool exists and is active
	if err := pm.ensurePool(); err != nil {
		_, _ = conn.Close() // Ignore close error
		return nil, fmt.Errorf("failed to ensure pool exists: %w", err)
	}

	return pm, nil
}

// Close closes the libvirt connection
func (pm *PoolManager) Close() error {
	if pm.conn != nil {
		_, err := pm.conn.Close()
		if err != nil {
			return fmt.Errorf("failed to close libvirt connection: %w", err)
		}
	}
	return nil
}

// ensurePool ensures the storage pool exists and is active
func (pm *PoolManager) ensurePool() error {
	pool, err := pm.conn.LookupStoragePoolByName(pm.poolName)
	if err != nil {
		// Pool doesn't exist, create it
		poolXML := fmt.Sprintf(`
<pool type="dir">
  <name>%s</name>
  <target>
    <path>%s</path>
  </target>
</pool>`, pm.poolName, pm.poolPath)

		pool, err = pm.conn.StoragePoolDefineXML(poolXML, 0)
		if err != nil {
			return fmt.Errorf("failed to define storage pool: %w", err)
		}
	}

	// Ensure pool is active
	active, err := pool.IsActive()
	if err != nil {
		_ = pool.Free() // Ignore error
		return fmt.Errorf("failed to check pool active status: %w", err)
	}

	if !active {
		err = pool.Create(0)
		if err != nil {
			_ = pool.Free() // Ignore error
			return fmt.Errorf("failed to start storage pool: %w", err)
		}
	}

	_ = pool.Free() // Ignore error
	return nil
}

// AllocateImage allocates space for an image in the libvirt storage pool
// DEPRECATED: Use AllocateImageFile instead for better compression handling
func (pm *PoolManager) AllocateImage(imageName string, sizeBytes uint64) (string, error) {
	pool, err := pm.conn.LookupStoragePoolByName(pm.poolName)
	if err != nil {
		return "", fmt.Errorf("failed to lookup pool: %w", err)
	}
	defer func() { _ = pool.Free() }()

	// Generate volume XML
	volumeXML := fmt.Sprintf(`
<volume>
  <name>%s</name>
  <capacity>%d</capacity>
  <target>
    <format type="raw"/>
  </target>
</volume>`, imageName, sizeBytes)

	// Create the volume
	vol, err := pool.StorageVolCreateXML(volumeXML, 0)
	if err != nil {
		return "", fmt.Errorf("failed to create volume: %w", err)
	}
	defer func() { _ = vol.Free() }()

	// Get the volume path
	volPath, err := vol.GetPath()
	if err != nil {
		return "", fmt.Errorf("failed to get volume path: %w", err)
	}

	return volPath, nil
}

// AllocateImageFile allocates a file path for caching an image without creating a libvirt volume.
// This preserves compression in QCOW2 images by storing them as plain files instead of RAW volumes.
func (pm *PoolManager) AllocateImageFile(imageName string) (string, error) {
	// Ensure cache directory exists
	if err := os.MkdirAll(pm.poolPath, 0o750); err != nil {
		return "", fmt.Errorf("failed to create cache directory: %w", err)
	}

	// Return the full path where the image file will be stored
	imagePath := filepath.Join(pm.poolPath, imageName)
	return imagePath, nil
}

// CheckCache checks if an image is already cached by looking for the checksum file.
// Returns cached image metadata if found, nil if not cached, or error on failure.
func (pm *PoolManager) CheckCache(checksum string) (*ImageCache, error) {
	// Ensure cache directory exists
	if err := os.MkdirAll(pm.poolPath, 0o750); err != nil {
		return nil, fmt.Errorf("failed to access cache directory: %w", err)
	}

	// Look for checksum file in the cache directory
	checksumFile := filepath.Join(pm.poolPath, checksum+".sha256")

	// Check if checksum file exists
	if _, err := os.Stat(checksumFile); err != nil {
		if os.IsNotExist(err) {
			return nil, nil //nolint:nilnil // Image not cached
		}
		return nil, fmt.Errorf("failed to check checksum file: %w", err)
	}

	// Checksum file exists, now find the corresponding image file.
	// Convention: checksum file is "{imagePath}.sha256", so image path is "{checksum_file_path}" minus ".sha256"
	imagePath := strings.TrimSuffix(checksumFile, ".sha256")

	// Verify image file exists
	fileInfo, err := os.Stat(imagePath)
	if err != nil {
		if os.IsNotExist(err) {
			// Checksum file orphaned - image was deleted
			logrus.WithFields(logrus.Fields{
				"checksum":      checksum,
				"checksum_file": checksumFile,
				"image_path":    imagePath,
			}).Warn("Orphaned checksum file - image file missing")
			return nil, nil //nolint:nilnil // Image not cached
		}
		return nil, fmt.Errorf("failed to stat image file: %w", err)
	}

	// Return cached image information
	size := fileInfo.Size()
	if size < 0 {
		return nil, fmt.Errorf("invalid file size: %d", size)
	}
	cache := &ImageCache{
		Path:     imagePath,
		Size:     uint64(size),
		Checksum: checksum,
	}

	return cache, nil
}

// CreateCacheEntry creates a cache entry with checksum file
func (pm *PoolManager) CreateCacheEntry(imagePath, checksum string) error {
	checksumFile := imagePath + ".sha256"

	// Write checksum to file
	err := os.WriteFile(checksumFile, []byte(checksum), 0600)
	if err != nil {
		return fmt.Errorf("failed to write checksum file: %w", err)
	}

	return nil
}

// CalculateChecksum calculates SHA256 checksum of a file
func CalculateChecksum(filePath string) (string, error) {
	// Validate path to prevent directory traversal
	if strings.Contains(filePath, "..") || !strings.HasPrefix(filePath, "/var/lib/libvirt/") {
		return "", fmt.Errorf("invalid file path: %s", filePath)
	}

	file, err := os.Open(filePath) // #nosec G304 -- Path validated above
	if err != nil {
		return "", fmt.Errorf("failed to open file for checksum: %w", err)
	}
	defer func() { _ = file.Close() }()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("failed to calculate checksum: %w", err)
	}

	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// GetImageNameFromURL extracts a suitable volume name from the image URL
func GetImageNameFromURL(imageURL string) string {
	// Extract filename from URL
	parts := strings.Split(imageURL, "/")
	filename := parts[len(parts)-1]

	// Remove file extension and sanitize
	name := strings.TrimSuffix(filename, filepath.Ext(filename))
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.ReplaceAll(name, ".", "_")

	return name
}

// DeleteImage removes an image and its checksum from the cache
func (pm *PoolManager) DeleteImage(imagePath string) error {
	// Remove image file
	if err := os.Remove(imagePath); err != nil && !os.IsNotExist(err) {
		logrus.WithError(err).Warn("Failed to remove cached image file")
	}

	// Remove checksum file
	checksumPath := imagePath + ".sha256"
	if err := os.Remove(checksumPath); err != nil && !os.IsNotExist(err) {
		logrus.WithError(err).Warn("Failed to remove checksum file")
	}

	return nil
}
