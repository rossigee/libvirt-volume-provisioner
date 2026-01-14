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

// CheckCache checks if an image is already cached
func (pm *PoolManager) CheckCache(checksum string) (*ImageCache, error) {
	pool, err := pm.conn.LookupStoragePoolByName(pm.poolName)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup pool: %w", err)
	}
	defer func() { _ = pool.Free() }()

	// List all volumes in the pool
	vols, err := pool.ListAllStorageVolumes(0)
	if err != nil {
		return nil, fmt.Errorf("failed to list volumes: %w", err)
	}

	// Look for volume with matching checksum
	checksumFile := checksum + ".sha256"
	for _, vol := range vols {
		volName, err := vol.GetName()
		if err != nil {
			_ = vol.Free()
			continue
		}

		if volName == checksumFile {
			// Found checksum file, get the image path
			volPath, err := vol.GetPath()
			if err != nil {
				_ = vol.Free()
				continue
			}

			// Image should be in same directory with .img extension removed from checksum
			imagePath := strings.TrimSuffix(volPath, ".sha256")

			// Check if image file exists
			if _, err := os.Stat(imagePath); err == nil {
				volInfo, err := vol.GetInfo()
				if err != nil {
					_ = vol.Free()
					continue
				}

				cache := &ImageCache{
					Path:     imagePath,
					Size:     volInfo.Capacity,
					Checksum: checksum,
				}

				_ = vol.Free()
				return cache, nil
			}
		}

		_ = vol.Free()
	}

	return nil, nil //nolint:nilnil // Image not cached
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
