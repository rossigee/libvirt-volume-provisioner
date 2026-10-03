package config

import "testing"

// The cache lives in the libvirt pool directory, which on these hypervisors is a
// directory inside the root devtmpfs. That makes it RAM shared with in-flight
// provisioning downloads, not scratch space with room to spare, so the default
// has to fit both.
//
// Observed on itx-001 with a 40GB tmpfs and max_concurrent: 2:
//
//	cache (3 images)          19.8 GB
//	one download, buffered    6.88 GB
//	reported available        2.24 GB  -> "insufficient disk space"
//
// A cap at or above that cache size evicts nothing, so the next download fails
// exactly as before. This test exists to stop the default drifting back up into
// the range where the sweep cannot help.
func TestDefaultCacheSizeLeavesRoomForConcurrentDownloads(t *testing.T) {
	cfg := defaults()

	const tmpfsGB = 40
	const concurrentDownloads = 2
	const imageBytes = 6_879_009_177 // the figure the provisioner reported for one download

	tmpfsBytes := int64(tmpfsGB) * 1024 * 1024 * 1024
	downloadsBytes := int64(concurrentDownloads) * imageBytes

	if cfg.Cache.MaxSizeBytes <= 0 {
		t.Fatal("default MaxSizeBytes must bound the cache; zero disables the size sweep " +
			"entirely and leaves MaxAge as the only bound, which cannot constrain a tmpfs")
	}

	if cfg.Cache.MaxSizeBytes >= downloadsBytes {
		t.Errorf("default MaxSizeBytes = %d bytes (%.1fGB) leaves less than one image of room "+
			"for %d concurrent downloads needing %.1fGB; the sweep would not evict early enough "+
			"to make room", cfg.Cache.MaxSizeBytes, float64(cfg.Cache.MaxSizeBytes)/(1<<30),
			concurrentDownloads, float64(downloadsBytes)/(1<<30))
	}

	if headroom := tmpfsBytes - cfg.Cache.MaxSizeBytes; headroom < downloadsBytes {
		t.Errorf("default MaxSizeBytes = %.1fGB leaves %.1fGB on a %dGB tmpfs, but %d concurrent "+
			"downloads need %.1fGB; provisioning fails with \"insufficient disk space\"",
			float64(cfg.Cache.MaxSizeBytes)/(1<<30), float64(headroom)/(1<<30),
			tmpfsGB, concurrentDownloads, float64(downloadsBytes)/(1<<30))
	}
}

// The cache must also hold at least one image, or every provisioning re-downloads
// and the cache stops earning its space.
func TestDefaultCacheHoldsAtLeastOneImage(t *testing.T) {
	cfg := defaults()

	const oneImage = 6_879_009_177

	if cfg.Cache.MaxSizeBytes < oneImage {
		t.Errorf("default MaxSizeBytes = %d bytes cannot hold a single %d byte image, so the "+
			"cache would never be reused", cfg.Cache.MaxSizeBytes, oneImage)
	}
}