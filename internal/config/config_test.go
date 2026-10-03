package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clearCredentialEnv unsets every env var Load consults so a test is not affected
// by the ambient environment or by a previous case in the same run.
func clearCredentialEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"MINIO_ACCESS_KEY", "MINIO_ACCESS_KEY_ID",
		"MINIO_SECRET_KEY", "MINIO_SECRET_ACCESS_KEY",
	} {
		t.Setenv(k, "")
	}
}

// writeConfig writes content to a temp file and returns its path.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestDefaults(t *testing.T) {
	cfg := defaults()

	assert.Equal(t, 8080, cfg.Server.Port)
	assert.Equal(t, "/etc/libvirt-volume-provisioner/tokens", cfg.Server.APITokensFile)
	assert.Equal(t, "./provisioner.db", cfg.Server.DBPath)

	assert.Equal(t, 3, cfg.MinIO.RetryAttempts)
	assert.Equal(t, []int{100, 1000, 10000}, cfg.MinIO.RetryBackoffMS)

	assert.Equal(t, "qemu:///system", cfg.Libvirt.URI)
	assert.Equal(t, "images", cfg.Libvirt.Pool)
	assert.Equal(t, 2, cfg.Libvirt.MaxConcurrent)
	assert.Equal(t, 180, cfg.Libvirt.JobTimeoutMinutes)

	assert.Equal(t, "vg0", cfg.LVM.VolumeGroup)
	assert.Equal(t, 2, cfg.LVM.RetryAttempts)
	assert.Equal(t, []int{100, 1000}, cfg.LVM.RetryBackoffMS)

	assert.Equal(t, "168h", cfg.Cache.MaxAge)
	assert.Equal(t, "1h", cfg.Cache.EvictionInterval)
	// The pool directory is devtmpfs on the hypervisors, so this bound is what
	// stops the cache exhausting RAM. It must not silently change.
	//
	// Lowered from 20GB: the pool directory is not only a cache, an in-flight
	// provisioning writes its image there too, so the tmpfs carries the cache plus
	// one image per concurrent job. 19.8GB of cache sits under a 20GB cap, so the
	// sweep evicted nothing and the next download failed anyway. See
	// TestDefaultCacheSizeLeavesRoomForConcurrentDownloads for the arithmetic.
	assert.Equal(t, int64(8*1024*1024*1024), cfg.Cache.MaxSizeBytes)

	assert.Equal(t, "info", cfg.Logging.Level)
	assert.Equal(t, "json", cfg.Logging.Format)
	assert.Equal(t, "stdout", cfg.Logging.File)

	assert.True(t, cfg.Metrics.Enabled)
	assert.Equal(t, 1.0, cfg.Tracing.SamplingRate)
	assert.Equal(t, []string{"otlp"}, cfg.Tracing.Exporters)
}

// A missing config file is not an error: the daemon is expected to start on
// built-in defaults.
func TestLoad_MissingFileReturnsDefaults(t *testing.T) {
	clearCredentialEnv(t)

	cfg, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, defaults(), *cfg)
}

// A read failure that is not "not exist" must surface, otherwise a permissions
// problem silently starts the daemon with defaults.
func TestLoad_UnreadablePathReturnsError(t *testing.T) {
	clearCredentialEnv(t)

	// A directory reads as EISDIR rather than ENOENT, so this exercises the
	// non-IsNotExist branch without depending on uid or file modes.
	_, err := Load(t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading config file")
}

func TestLoad_MalformedYAMLReturnsError(t *testing.T) {
	clearCredentialEnv(t)

	_, err := Load(writeConfig(t, "server:\n  port: not-a-number\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing config file")
}

func TestLoad_UnclosedYAMLReturnsError(t *testing.T) {
	clearCredentialEnv(t)

	_, err := Load(writeConfig(t, "server: {\n  port: 8080\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing config file")
}

func TestLoad_PopulatesEverySection(t *testing.T) {
	clearCredentialEnv(t)

	cfg, err := Load(writeConfig(t, `
server:
  port: 9443
  tls_cert: /etc/tls/server.crt
  tls_key: /etc/tls/server.key
  ca_cert: /etc/tls/ca.crt
  api_tokens_file: /etc/lvp/tokens
  db_path: /var/lib/lvp/provisioner.db
minio:
  endpoint: minio.example.com:9000
  access_key: file-access
  secret_key: file-secret
  region: eu-west-2
  ca_cert: /etc/tls/minio-ca.crt
  retry_attempts: 5
  retry_backoff_ms: [50, 500, 5000]
libvirt:
  uri: qemu+ssh://hypervisor/system
  pool: fast-images
  max_concurrent: 8
  job_timeout_minutes: 90
lvm:
  volume_group: vg-data
  retry_attempts: 4
  retry_backoff_ms: [250, 2500]
cache:
  max_age: 24h
  eviction_interval: 15m
  max_size_bytes: 1073741824
logging:
  level: debug
  format: text
  file: /var/log/lvp.log
  sampling_rate: 5
  loki_url: http://loki:3100
  webhook_url: http://hooks:8080/hook
metrics:
  enabled: false
tracing:
  endpoint: otel:4317
  sampling_rate: 0.25
  exporters: [otlp, stdout]
`))
	require.NoError(t, err)

	assert.Equal(t, 9443, cfg.Server.Port)
	assert.Equal(t, "/etc/tls/server.crt", cfg.Server.TLSCert)
	assert.Equal(t, "/etc/tls/server.key", cfg.Server.TLSKey)
	assert.Equal(t, "/etc/tls/ca.crt", cfg.Server.CACert)
	assert.Equal(t, "/etc/lvp/tokens", cfg.Server.APITokensFile)
	assert.Equal(t, "/var/lib/lvp/provisioner.db", cfg.Server.DBPath)

	assert.Equal(t, "minio.example.com:9000", cfg.MinIO.Endpoint)
	assert.Equal(t, "file-access", cfg.MinIO.AccessKey)
	assert.Equal(t, "file-secret", cfg.MinIO.SecretKey)
	assert.Equal(t, "eu-west-2", cfg.MinIO.Region)
	assert.Equal(t, "/etc/tls/minio-ca.crt", cfg.MinIO.CACert)
	assert.Equal(t, 5, cfg.MinIO.RetryAttempts)
	assert.Equal(t, []int{50, 500, 5000}, cfg.MinIO.RetryBackoffMS)

	assert.Equal(t, "qemu+ssh://hypervisor/system", cfg.Libvirt.URI)
	assert.Equal(t, "fast-images", cfg.Libvirt.Pool)
	assert.Equal(t, 8, cfg.Libvirt.MaxConcurrent)
	assert.Equal(t, 90, cfg.Libvirt.JobTimeoutMinutes)

	assert.Equal(t, "vg-data", cfg.LVM.VolumeGroup)
	assert.Equal(t, 4, cfg.LVM.RetryAttempts)
	assert.Equal(t, []int{250, 2500}, cfg.LVM.RetryBackoffMS)

	assert.Equal(t, "24h", cfg.Cache.MaxAge)
	assert.Equal(t, "15m", cfg.Cache.EvictionInterval)
	assert.Equal(t, int64(1073741824), cfg.Cache.MaxSizeBytes)

	assert.Equal(t, "debug", cfg.Logging.Level)
	assert.Equal(t, "text", cfg.Logging.Format)
	assert.Equal(t, "/var/log/lvp.log", cfg.Logging.File)
	assert.Equal(t, 5, cfg.Logging.SamplingRate)
	assert.Equal(t, "http://loki:3100", cfg.Logging.LokiURL)
	assert.Equal(t, "http://hooks:8080/hook", cfg.Logging.WebhookURL)

	assert.False(t, cfg.Metrics.Enabled)
	assert.Equal(t, "otel:4317", cfg.Tracing.Endpoint)
	assert.InDelta(t, 0.25, cfg.Tracing.SamplingRate, 1e-9)
	assert.Equal(t, []string{"otlp", "stdout"}, cfg.Tracing.Exporters)
}

// A partial file must not reset the fields it does not mention - operators are
// expected to override single settings without restating the whole file.
func TestLoad_PartialConfigPreservesDefaults(t *testing.T) {
	clearCredentialEnv(t)

	cfg, err := Load(writeConfig(t, "server:\n  port: 9999\n"))
	require.NoError(t, err)

	assert.Equal(t, 9999, cfg.Server.Port)
	assert.Equal(t, defaults().Server.DBPath, cfg.Server.DBPath)
	assert.Equal(t, defaults().Server.APITokensFile, cfg.Server.APITokensFile)
	assert.Equal(t, defaults().Libvirt, cfg.Libvirt)
	assert.Equal(t, defaults().LVM, cfg.LVM)
	assert.Equal(t, defaults().Cache, cfg.Cache)
	assert.Equal(t, defaults().Logging.Level, cfg.Logging.Level)
	assert.True(t, cfg.Metrics.Enabled)
}

// An explicit zero in the file is an operator decision, not an absent value, so
// it must win over the default. max_size_bytes: 0 in particular disables the
// size-based cache sweep entirely.
func TestLoad_ExplicitZeroOverridesDefault(t *testing.T) {
	clearCredentialEnv(t)

	cfg, err := Load(writeConfig(t, `
server:
  port: 0
cache:
  max_size_bytes: 0
metrics:
  enabled: false
logging:
  sampling_rate: 0
libvirt:
  max_concurrent: 0
`))
	require.NoError(t, err)

	assert.Equal(t, 0, cfg.Server.Port)
	assert.Equal(t, int64(0), cfg.Cache.MaxSizeBytes)
	assert.False(t, cfg.Metrics.Enabled)
	assert.Equal(t, 0, cfg.Logging.SamplingRate)
	assert.Equal(t, 0, cfg.Libvirt.MaxConcurrent)

	// Untouched neighbours keep their defaults.
	assert.Equal(t, defaults().Cache.MaxAge, cfg.Cache.MaxAge)
	assert.Equal(t, defaults().Libvirt.URI, cfg.Libvirt.URI)
}

// An empty or null section is treated as "not specified" and keeps the default
// block, which is what an operator writing "cache:" expects.
func TestLoad_EmptySectionPreservesDefaults(t *testing.T) {
	clearCredentialEnv(t)

	for _, doc := range []string{"cache:\n", "cache: {}\n", "{}\n"} {
		cfg, err := Load(writeConfig(t, doc))
		require.NoError(t, err, "doc %q", doc)
		assert.Equal(t, defaults().Cache, cfg.Cache, "doc %q", doc)
		assert.Equal(t, defaults().Server, cfg.Server, "doc %q", doc)
	}
}

// Unknown keys are silently ignored rather than rejected, so a typo such as
// "prot:" does not stop the daemon from starting.
func TestLoad_UnknownFieldsIgnored(t *testing.T) {
	clearCredentialEnv(t)

	cfg, err := Load(writeConfig(t, "server:\n  prot: 1234\nnot_a_section: true\n"))
	require.NoError(t, err)
	assert.Equal(t, defaults().Server.Port, cfg.Server.Port)
}

func TestLoad_EnvOverridesCredentials(t *testing.T) {
	clearCredentialEnv(t)
	t.Setenv("MINIO_ACCESS_KEY", "env-access")
	t.Setenv("MINIO_SECRET_KEY", "env-secret")

	cfg, err := Load(writeConfig(t, "minio:\n  access_key: file-access\n  secret_key: file-secret\n"))
	require.NoError(t, err)

	assert.Equal(t, "env-access", cfg.MinIO.AccessKey)
	assert.Equal(t, "env-secret", cfg.MinIO.SecretKey)
}

// The *_ID / *_ACCESS_KEY long forms are accepted for callers that use the AWS
// naming convention.
func TestLoad_EnvAlternateNames(t *testing.T) {
	clearCredentialEnv(t)
	t.Setenv("MINIO_ACCESS_KEY_ID", "env-access-id")
	t.Setenv("MINIO_SECRET_ACCESS_KEY", "env-secret-legacy")

	cfg, err := Load(writeConfig(t, ""))
	require.NoError(t, err)

	assert.Equal(t, "env-access-id", cfg.MinIO.AccessKey)
	assert.Equal(t, "env-secret-legacy", cfg.MinIO.SecretKey)
}

// The short names take precedence over the long ones.
func TestLoad_EnvShortNameTakesPrecedence(t *testing.T) {
	clearCredentialEnv(t)
	t.Setenv("MINIO_ACCESS_KEY", "short-access")
	t.Setenv("MINIO_ACCESS_KEY_ID", "long-access")
	t.Setenv("MINIO_SECRET_KEY", "short-secret")
	t.Setenv("MINIO_SECRET_ACCESS_KEY", "long-secret")

	cfg, err := Load(writeConfig(t, ""))
	require.NoError(t, err)

	assert.Equal(t, "short-access", cfg.MinIO.AccessKey)
	assert.Equal(t, "short-secret", cfg.MinIO.SecretKey)
}

// An env var set to the empty string counts as unset, so exporting an empty
// variable cannot blank out a value from the config file.
func TestLoad_EmptyEnvDoesNotOverride(t *testing.T) {
	clearCredentialEnv(t)

	cfg, err := Load(writeConfig(t, "minio:\n  access_key: file-access\n  secret_key: file-secret\n"))
	require.NoError(t, err)

	assert.Equal(t, "file-access", cfg.MinIO.AccessKey)
	assert.Equal(t, "file-secret", cfg.MinIO.SecretKey)
}

// Credentials must still be applied when there is no config file at all.
func TestLoad_EnvOverridesApplyWithMissingFile(t *testing.T) {
	clearCredentialEnv(t)
	t.Setenv("MINIO_ACCESS_KEY", "env-access")
	t.Setenv("MINIO_SECRET_KEY", "env-secret")

	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	require.NoError(t, err)

	assert.Equal(t, "env-access", cfg.MinIO.AccessKey)
	assert.Equal(t, "env-secret", cfg.MinIO.SecretKey)
	assert.Equal(t, defaults().Server, cfg.Server)
}

// A read error must not be masked by credential overrides.
func TestLoad_ReadErrorSkipsEnvOverrides(t *testing.T) {
	clearCredentialEnv(t)
	t.Setenv("MINIO_ACCESS_KEY", "env-access")

	cfg, err := Load(t.TempDir())
	require.Error(t, err)
	assert.Nil(t, cfg)
}

func TestFirstEnv(t *testing.T) {
	clearCredentialEnv(t)

	assert.Equal(t, "", firstEnv("MINIO_ACCESS_KEY", "MINIO_ACCESS_KEY_ID"))

	t.Setenv("MINIO_ACCESS_KEY_ID", "second")
	assert.Equal(t, "second", firstEnv("MINIO_ACCESS_KEY", "MINIO_ACCESS_KEY_ID"))

	t.Setenv("MINIO_ACCESS_KEY", "first")
	assert.Equal(t, "first", firstEnv("MINIO_ACCESS_KEY", "MINIO_ACCESS_KEY_ID"))

	// An empty earlier name is skipped rather than returned.
	assert.Equal(t, "second", firstEnv("MINIO_SECRET_KEY", "MINIO_ACCESS_KEY_ID"))

	assert.Equal(t, "", firstEnv())
}

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
