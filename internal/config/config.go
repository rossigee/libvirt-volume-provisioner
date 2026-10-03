// Package config loads and validates daemon configuration from a YAML file,
// with env-var overrides for credentials.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the root configuration structure.
type Config struct {
	Server  ServerConfig  `yaml:"server"`
	MinIO   MinIOConfig   `yaml:"minio"`
	Libvirt LibvirtConfig `yaml:"libvirt"`
	LVM     LVMConfig     `yaml:"lvm"`
	Cache   CacheConfig   `yaml:"cache"`
	Logging LoggingConfig `yaml:"logging"`
	Metrics MetricsConfig `yaml:"metrics"`
	Tracing TracingConfig `yaml:"tracing"`
}

// ServerConfig holds HTTP/TLS server settings.
type ServerConfig struct {
	Port          int    `yaml:"port"`
	TLSCert       string `yaml:"tls_cert"`
	TLSKey        string `yaml:"tls_key"`
	CACert        string `yaml:"ca_cert"`
	APITokensFile string `yaml:"api_tokens_file"`
	DBPath        string `yaml:"db_path"`
}

// MinIOConfig holds MinIO/S3 client settings.
// AccessKey and SecretKey may be overridden via MINIO_ACCESS_KEY / MINIO_SECRET_KEY env vars.
type MinIOConfig struct {
	Endpoint       string `yaml:"endpoint"`
	AccessKey      string `yaml:"access_key"`
	SecretKey      string `yaml:"secret_key"`
	Region         string `yaml:"region"`
	CACert         string `yaml:"ca_cert"`
	RetryAttempts  int    `yaml:"retry_attempts"`
	RetryBackoffMS []int  `yaml:"retry_backoff_ms"`
}

// LibvirtConfig holds libvirt connection and pool settings.
type LibvirtConfig struct {
	URI               string `yaml:"uri"`
	Pool              string `yaml:"pool"`
	MaxConcurrent     int    `yaml:"max_concurrent"`
	JobTimeoutMinutes int    `yaml:"job_timeout_minutes"`
}

// LVMConfig holds LVM manager settings.
type LVMConfig struct {
	VolumeGroup    string `yaml:"volume_group"`
	RetryAttempts  int    `yaml:"retry_attempts"`
	RetryBackoffMS []int  `yaml:"retry_backoff_ms"`
}

// CacheConfig holds image cache lifecycle settings.
type CacheConfig struct {
	MaxAge           string `yaml:"max_age"`
	EvictionInterval string `yaml:"eviction_interval"`
	// MaxSizeBytes bounds the total size of the image cache. Zero disables the
	// size-based sweep and leaves MaxAge as the only bound.
	//
	// The cache lives in the libvirt pool directory, which on these hypervisors is
	// a directory inside the root devtmpfs -- that is, RAM. MaxAge alone cannot
	// bound it: at roughly 6.5GB per image, seven days of images cannot fit in a
	// 40GB RAM disk, so the cache grew until new downloads failed with
	// "insufficient disk space". A size bound is what actually constrains a cache
	// living on a space-constrained filesystem.
	//
	// The bound has to leave room for the downloads themselves. The pool
	// directory is not only a cache: an in-flight provisioning writes its image
	// there too, so with max_concurrent jobs the tmpfs has to carry the cache plus
	// one image per concurrent job at the same time. Observed on itx-001 with a
	// 40GB tmpfs: three cached images totalled 19.8GB, a fourth download needed
	// 6.88GB with buffer, and 2.24GB was reported available. A cap at or above
	// that cache size evicts nothing and the next download still fails.
	MaxSizeBytes int64 `yaml:"max_size_bytes"`
}

// LoggingConfig holds logging output settings.
type LoggingConfig struct {
	Level        string `yaml:"level"`
	Format       string `yaml:"format"`
	File         string `yaml:"file"`
	SamplingRate int    `yaml:"sampling_rate"`
	LokiURL      string `yaml:"loki_url"`
	WebhookURL   string `yaml:"webhook_url"`
}

// MetricsConfig holds Prometheus metrics settings.
type MetricsConfig struct {
	Enabled bool `yaml:"enabled"`
}

// TracingConfig holds OpenTelemetry tracing settings.
type TracingConfig struct {
	Endpoint     string   `yaml:"endpoint"`
	SamplingRate float64  `yaml:"sampling_rate"`
	Exporters    []string `yaml:"exporters"`
}

// defaults returns a Config with the same defaults the code previously hardcoded.
func defaults() Config {
	return Config{
		Server: ServerConfig{
			Port:          8080,
			APITokensFile: "/etc/libvirt-volume-provisioner/tokens",
			DBPath:        "./provisioner.db",
		},
		MinIO: MinIOConfig{
			RetryAttempts:  3,
			RetryBackoffMS: []int{100, 1000, 10000},
		},
		Libvirt: LibvirtConfig{
			URI:               "qemu:///system",
			Pool:              "images",
			MaxConcurrent:     2,
			JobTimeoutMinutes: 180,
		},
		LVM: LVMConfig{
			VolumeGroup:    "vg0",
			RetryAttempts:  2,
			RetryBackoffMS: []int{100, 1000},
		},
		Cache: CacheConfig{
			MaxAge:           "168h",
			EvictionInterval: "1h",
			// The pool directory is devtmpfs on these hosts, so the cache competes
			// with in-flight downloads for the same RAM: each concurrent job
			// (max_concurrent: 2) needs roughly 7GB there while it runs. 8GB holds
			// the one image the steady state reuses and leaves a 40GB tmpfs room
			// for two of those at once. A larger cap evicts too late to help --
			// on a 40GB tmpfs anything above ~12GB is already too big to leave
			// room for the downloads it is supposed to make possible.
			MaxSizeBytes: 8 * 1024 * 1024 * 1024,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
			File:   "stdout",
		},
		Metrics: MetricsConfig{
			Enabled: true,
		},
		Tracing: TracingConfig{
			SamplingRate: 1.0,
			Exporters:    []string{"otlp"},
		},
	}
}

// Load reads the YAML config file at path and applies credential env-var overrides.
// If the file does not exist the built-in defaults are used without error.
func Load(path string) (*Config, error) {
	cfg := defaults()

	data, err := os.ReadFile(path) //nolint:gosec // path is admin-controlled
	if err != nil {
		if os.IsNotExist(err) {
			applyCredentialEnvOverrides(&cfg)
			return &cfg, nil
		}
		return nil, fmt.Errorf("reading config file %s: %w", path, err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file %s: %w", path, err)
	}

	applyCredentialEnvOverrides(&cfg)
	return &cfg, nil
}

// applyCredentialEnvOverrides overlays MINIO_ACCESS_KEY / MINIO_SECRET_KEY env vars
// so that secrets can stay out of the config file.
func applyCredentialEnvOverrides(cfg *Config) {
	if v := firstEnv("MINIO_ACCESS_KEY", "MINIO_ACCESS_KEY_ID"); v != "" {
		cfg.MinIO.AccessKey = v
	}
	if v := firstEnv("MINIO_SECRET_KEY", "MINIO_SECRET_ACCESS_KEY"); v != "" {
		cfg.MinIO.SecretKey = v
	}
}

func firstEnv(names ...string) string {
	for _, name := range names {
		if v := os.Getenv(name); v != "" {
			return v
		}
	}
	return ""
}
