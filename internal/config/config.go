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
			JobTimeoutMinutes: 30,
		},
		LVM: LVMConfig{
			VolumeGroup:    "vg0",
			RetryAttempts:  2,
			RetryBackoffMS: []int{100, 1000},
		},
		Cache: CacheConfig{
			MaxAge:           "168h",
			EvictionInterval: "1h",
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
