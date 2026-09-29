// Package libvirt provides functionality for managing libvirt storage pools and volumes,
// including image caching and allocation for the libvirt-volume-provisioner.
package libvirt

import (
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/libvirt/libvirt-go"
	"github.com/rossigee/libvirt-volume-provisioner/internal/config"
	"github.com/sirupsen/logrus"
)

// ImageCache represents a cached image in the libvirt storage pool
type ImageCache struct {
	Path     string
	Size     uint64
	Checksum string
	ModTime  time.Time // mtime of the .sha256 sidecar — reliable "last cached at"
}

// PoolManager handles libvirt storage pool operations for image caching
type PoolManager struct {
	conn     *libvirt.Connect
	poolName string
	poolPath string
}

// poolTargetXML is used to parse the pool's target path out of its XML descriptor.
type poolTargetXML struct {
	Target struct {
		Path string `xml:"path"`
	} `xml:"target"`
}

// NewPoolManager creates a new libvirt pool manager from the provided configuration.
func NewPoolManager(cfg config.LibvirtConfig) (*PoolManager, error) {
	conn, err := libvirt.NewConnect(cfg.URI)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to libvirt at %s: %w", cfg.URI, err)
	}

	pm := &PoolManager{
		conn:     conn,
		poolName: cfg.Pool,
		poolPath: fmt.Sprintf("/var/lib/libvirt/%s", cfg.Pool), // default used only if pool must be created
	}

	// Ensure the pool exists and is active
	if err := pm.ensurePool(); err != nil {
		_, _ = conn.Close()
		return nil, fmt.Errorf("failed to ensure pool exists: %w", err)
	}

	// Resolve the actual pool target path from the libvirt pool definition.
	actualPath, err := pm.resolvePoolPath()
	if err != nil {
		_, _ = conn.Close()
		return nil, fmt.Errorf("failed to resolve pool path: %w", err)
	}
	pm.poolPath = actualPath

	return pm, nil
}

// resolvePoolPath queries the pool's XML descriptor and returns its target path.
func (pm *PoolManager) resolvePoolPath() (string, error) {
	pool, err := pm.conn.LookupStoragePoolByName(pm.poolName)
	if err != nil {
		return "", fmt.Errorf("failed to look up pool %q: %w", pm.poolName, err)
	}
	defer func() { _ = pool.Free() }()

	xmlDesc, err := pool.GetXMLDesc(0)
	if err != nil {
		return "", fmt.Errorf("failed to get XML for pool %q: %w", pm.poolName, err)
	}

	var parsed poolTargetXML
	if err := xml.Unmarshal([]byte(xmlDesc), &parsed); err != nil {
		return "", fmt.Errorf("failed to parse XML for pool %q: %w", pm.poolName, err)
	}
	if parsed.Target.Path == "" {
		return "", fmt.Errorf("pool %q XML has empty target path", pm.poolName)
	}
	return parsed.Target.Path, nil
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

// AllocateImageFile allocates a file path for caching an image without creating a libvirt volume.
// This preserves compression in QCOW2 images by storing them as plain files instead of RAW volumes.
// cacheKey must be a 64-character lowercase hex string (SHA-256 of the source URL).
func (pm *PoolManager) AllocateImageFile(cacheKey string) (string, error) {
	if !isValidCacheKey(cacheKey) {
		return "", fmt.Errorf("invalid cache key %q: must be 64 lowercase hex characters", cacheKey)
	}

	if err := os.MkdirAll(pm.poolPath, 0o750); err != nil {
		return "", fmt.Errorf("failed to create cache directory: %w", err)
	}

	return filepath.Join(pm.poolPath, cacheKey), nil
}

// isValidCacheKey reports whether k is a 64-character lowercase hex string.
func isValidCacheKey(k string) bool {
	if len(k) != 64 {
		return false
	}
	for _, r := range k {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// CheckCache checks if an image is already cached by looking for the checksum file.
// Returns cached image metadata if found, nil if not cached, or error on failure.
func (pm *PoolManager) CheckCache(cacheKey string) (*ImageCache, error) {
	// Ensure cache directory exists
	if err := os.MkdirAll(pm.poolPath, 0o750); err != nil {
		return nil, fmt.Errorf("failed to access cache directory: %w", err)
	}

	// Look for checksum file in the cache directory
	checksumFile := filepath.Join(pm.poolPath, cacheKey+".sha256")

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
				"cache_key":     cacheKey,
				"checksum_file": checksumFile,
				"image_path":    imagePath,
			}).Warn("Orphaned checksum file - image file missing")
			return nil, nil //nolint:nilnil // Image not cached
		}
		return nil, fmt.Errorf("failed to stat image file: %w", err)
	}

	// Read stored checksum from .sha256 file
	//nolint:gosec // checksumFile is constructed from controlled pool path + hex cache key
	storedChecksumData, err := os.ReadFile(checksumFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read checksum file: %w", err)
	}

	size := fileInfo.Size()
	if size < 0 {
		return nil, fmt.Errorf("invalid file size: %d", size)
	}

	sidecarInfo, err := os.Stat(checksumFile)
	if err != nil {
		return nil, fmt.Errorf("failed to stat checksum file: %w", err)
	}

	cache := &ImageCache{
		Path:     imagePath,
		Size:     uint64(size),
		Checksum: strings.TrimSpace(string(storedChecksumData)),
		ModTime:  sidecarInfo.ModTime(),
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

// CalculateChecksum calculates SHA256 checksum of a file.
// filePath must be an absolute path with no ".." components.
func CalculateChecksum(filePath string) (string, error) {
	if strings.Contains(filePath, "..") {
		return "", fmt.Errorf("invalid file path: %s", filePath)
	}

	file, err := os.Open(filePath) // #nosec G304 -- path is caller-controlled; traversal checked above
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

// ListCachedImages returns a list of all cached images
func (pm *PoolManager) ListCachedImages() ([]*ImageCache, error) {
	// Ensure cache directory exists
	if err := os.MkdirAll(pm.poolPath, 0o750); err != nil {
		return nil, fmt.Errorf("failed to access cache directory: %w", err)
	}

	// Read directory entries
	entries, err := os.ReadDir(pm.poolPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read cache directory: %w", err)
	}

	var cachedImages []*ImageCache

	for _, entry := range entries {
		// Look for checksum files (.sha256 extension)
		if !strings.HasSuffix(entry.Name(), ".sha256") {
			continue
		}

		// Extract cache key from checksum filename (remove .sha256 suffix)
		cacheKey := strings.TrimSuffix(entry.Name(), ".sha256")

		// Get full path of checksum file
		checksumFile := filepath.Join(pm.poolPath, entry.Name())

		// Get corresponding image path
		imagePath := filepath.Join(pm.poolPath, cacheKey)

		// Check if image file exists
		fileInfo, err := os.Stat(imagePath)
		if err != nil {
			if os.IsNotExist(err) {
				// Orphaned checksum file - log warning but skip
				logrus.WithFields(logrus.Fields{
					"cache_key":     cacheKey,
					"checksum_file": checksumFile,
					"image_path":    imagePath,
				}).Warn("Orphaned checksum file - image file missing")
			}
			continue
		}

		// Create ImageCache entry
		size := fileInfo.Size()
		if size < 0 {
			logrus.WithFields(logrus.Fields{
				"cache_key":  cacheKey,
				"image_path": imagePath,
			}).Warn("Invalid file size for cached image")
			continue
		}

		sidecarInfo, err := entry.Info()
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"cache_key":     cacheKey,
				"checksum_file": checksumFile,
			}).Warn("Failed to stat checksum file for modtime")
			continue
		}

		cache := &ImageCache{
			Path: imagePath,
			// #nosec G115 // size is checked to be non-negative above
			Size:     uint64(size),
			Checksum: cacheKey,
			ModTime:  sidecarInfo.ModTime(),
		}

		cachedImages = append(cachedImages, cache)
	}

	return cachedImages, nil
}

// UploadVolumeContent uploads raw content directly to a libvirt storage volume.
// poolName is the name of the storage pool (e.g., "cloud-init").
// volumeName is the name of the volume within that pool (e.g., "cidata-bankrut-master-cp-cd456.bankrut.lan").
// content is the raw bytes to write to the volume.
func (pm *PoolManager) UploadVolumeContent(poolName, volumeName string, content io.Reader, contentSize int64) error {
	conn := pm.conn

	pool, err := conn.LookupStoragePoolByName(poolName)
	if err != nil {
		return fmt.Errorf("failed to look up storage pool %q: %w", poolName, err)
	}
	defer func() {
		if err := pool.Free(); err != nil {
			logrus.WithError(err).Warning("failed to free pool")
		}
	}()

	vol, err := pool.LookupStorageVolByName(volumeName)
	if err != nil {
		return fmt.Errorf("failed to look up volume %q in pool %q: %w", volumeName, poolName, err)
	}
	defer func() {
		if err := vol.Free(); err != nil {
			logrus.WithError(err).Warning("failed to free volume")
		}
	}()

	volPath, err := vol.GetPath()
	if err != nil {
		return fmt.Errorf("failed to get volume path for %s: %w", volumeName, err)
	}

	stream, err := conn.NewStream(0)
	if err != nil {
		return fmt.Errorf("failed to create libvirt stream: %w", err)
	}
	defer func() {
		if err := stream.Free(); err != nil {
			logrus.WithError(err).Warning("failed to free stream")
		}
	}()

	if err := vol.Upload(stream, 0, uint64(contentSize), 0); err != nil {
		return fmt.Errorf("failed to initiate volume upload for %s: %w", volumeName, err)
	}

	buf := make([]byte, 32*1024)
	var written int64
	for {
		n, readErr := content.Read(buf)
		if n > 0 {
			sent, sendErr := stream.Send(buf[:n])
			if sendErr != nil {
				return fmt.Errorf("failed to send data to stream for volume %s: %w", volumeName, sendErr)
			}
			written += int64(sent)
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return fmt.Errorf("failed to read content for volume %s: %w", volumeName, readErr)
		}
	}

	if err := stream.Finish(); err != nil {
		return fmt.Errorf("failed to finish stream for volume %s (wrote %d bytes): %w", volumeName, written, err)
	}

	_ = volPath // path available for debugging if needed

	logrus.WithFields(logrus.Fields{
		"pool":   poolName,
		"volume": volumeName,
		"bytes":  written,
	}).Info("Volume content uploaded successfully")
	return nil
}

// EvictExpiredImages removes cached images whose .sha256 sidecar mtime is
// older than maxAge. Individual delete failures are logged but non-fatal.
func (pm *PoolManager) EvictExpiredImages(maxAge time.Duration) (int, error) {
	images, err := pm.ListCachedImages()
	if err != nil {
		return 0, fmt.Errorf("eviction: failed to list cached images: %w", err)
	}
	cutoff := time.Now().Add(-maxAge)
	evicted := 0
	for _, img := range images {
		if img.ModTime.Before(cutoff) {
			logrus.WithFields(logrus.Fields{
				"image_path": img.Path,
				"age":        time.Since(img.ModTime).Round(time.Second),
				"max_age":    maxAge,
			}).Info("Evicting expired cached image")
			if err := pm.DeleteImage(img.Path); err != nil {
				logrus.WithError(err).WithField("image_path", img.Path).
					Error("Failed to evict expired image")
				continue
			}
			evicted++
		}
	}
	// Evict image files that have no .sha256 sidecar.
	//
	// ListCachedImages only yields entries that have a sidecar, because the
	// sidecar is what makes a cached image usable and it supplies the modtime
	// used for expiry above. A download that fails partway leaves the image
	// file behind with no sidecar, so it never appears in that list and the
	// sweep above could never reclaim it. The caller does delete the file when
	// DownloadImageToPath returns an error, but not when a later stage aborts
	// the job, and the cache path is under a tmpfs on these hypervisors, so
	// every abandoned multi-GB file counts against RAM until someone removes it
	// by hand. On itx-001/002/003 that accumulated into 30GB of orphans and
	// then began failing new downloads with "insufficient disk space".
	//
	// Treat a sidecar-less file as expired regardless of age: it cannot be a
	// valid cache entry, so there is nothing worth keeping.
	orphans, err := pm.evictSidecarlessImages()
	if err != nil {
		return evicted, err
	}

	logrus.WithFields(logrus.Fields{"evicted": evicted, "orphans_removed": orphans, "total": len(images)}).
		Info("Cache eviction sweep completed")
	return evicted, nil
}

// evictSidecarlessImages removes image files in the cache that have no
// accompanying .sha256 sidecar. These are abandoned partial downloads: a
// complete download always has its checksum sidecar written alongside it.
func (pm *PoolManager) evictSidecarlessImages() (int, error) {
	entries, err := os.ReadDir(pm.poolPath)
	if err != nil {
		return 0, fmt.Errorf("eviction: failed to read cache directory: %w", err)
	}

	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Sidecars are the marker of a complete image, not orphans themselves.
		if strings.HasSuffix(name, ".sha256") {
			continue
		}
		// Only 64-character lowercase hex cache keys are image files
		// (AllocateImageFile rejects anything else), so anything else in here is
		// not ours to delete.
		if !isValidCacheKey(name) {
			continue
		}

		imagePath := filepath.Join(pm.poolPath, name)
		sidecarPath := imagePath + ".sha256"
		if _, err := os.Stat(sidecarPath); err == nil {
			continue // complete image, leave it to the expiry sweep
		} else if !os.IsNotExist(err) {
			logrus.WithError(err).WithField("image_path", sidecarPath).
				Warn("Failed to stat checksum sidecar; skipping orphan check")
			continue
		}

		var size int64
		if info, statErr := entry.Info(); statErr == nil {
			size = info.Size()
		}
		if err := os.Remove(imagePath); err != nil {
			logrus.WithError(err).WithField("image_path", imagePath).
				Error("Failed to remove orphaned partial image")
			continue
		}
		logrus.WithFields(logrus.Fields{
			"image_path": imagePath,
			"size":       size,
		}).Info("Removed orphaned partial image (no checksum sidecar)")
		removed++
	}
	return removed, nil
}
