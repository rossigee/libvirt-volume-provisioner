// Package main provides the entry point for the libvirt-volume-provisioner application.
// This is a service for provisioning LVM volumes from MinIO-hosted disk images.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rossigee/libvirt-volume-provisioner/internal/api"
	"github.com/rossigee/libvirt-volume-provisioner/internal/auth"
	"github.com/rossigee/libvirt-volume-provisioner/internal/config"
	"github.com/rossigee/libvirt-volume-provisioner/internal/jobs"
	"github.com/rossigee/libvirt-volume-provisioner/internal/libvirt"
	"github.com/rossigee/libvirt-volume-provisioner/internal/logging"
	"github.com/rossigee/libvirt-volume-provisioner/internal/lvm"
	"github.com/rossigee/libvirt-volume-provisioner/internal/metrics"
	"github.com/rossigee/libvirt-volume-provisioner/internal/minio"
	"github.com/rossigee/libvirt-volume-provisioner/internal/storage"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

var errOTLPNotConfigured = errors.New("OTLP not configured")

// Build information - set at build time via -ldflags "-X main.version=... -X main.buildTime=..."
var (
	version   = "v0.12.0"
	buildTime = "unknown"
)

// initTracing initializes OpenTelemetry tracing. Tracing is disabled when endpoint is empty.
func initTracing(ctx context.Context, cfg config.TracingConfig) (*sdktrace.TracerProvider, error) {
	if cfg.Endpoint == "" {
		return nil, errOTLPNotConfigured
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String("libvirt-volume-provisioner"),
			semconv.ServiceVersionKey.String(version),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	var sampler sdktrace.Sampler
	switch {
	case cfg.SamplingRate >= 1.0:
		sampler = sdktrace.AlwaysSample()
	case cfg.SamplingRate <= 0.0:
		sampler = sdktrace.NeverSample()
	default:
		sampler = sdktrace.TraceIDRatioBased(cfg.SamplingRate)
	}

	var exporters []sdktrace.SpanExporter
	for _, expType := range cfg.Exporters {
		switch strings.ToLower(expType) {
		case "otlp":
			endpoint := cfg.Endpoint
			if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
				u, err := url.Parse(endpoint)
				if err != nil {
					return nil, fmt.Errorf("failed to parse tracing endpoint: %w", err)
				}
				endpoint = u.Host
			}
			exporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(endpoint))
			if err != nil {
				return nil, fmt.Errorf("failed to create OTLP exporter: %w", err)
			}
			exporters = append(exporters, exporter)
		case "jaeger":
			logrus.Warn("Jaeger exporter not implemented yet")
		case "zipkin":
			logrus.Warn("Zipkin exporter not implemented yet")
		}
	}

	if len(exporters) == 0 {
		return nil, fmt.Errorf("no valid exporters configured")
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporters[0]),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)
	for i := 1; i < len(exporters); i++ {
		tp.RegisterSpanProcessor(sdktrace.NewBatchSpanProcessor(exporters[i]))
	}

	otel.SetTracerProvider(tp)
	return tp, nil
}

// logCorrelationHook injects trace_id and span_id into logrus entries
type logCorrelationHook struct{}

func (h *logCorrelationHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (h *logCorrelationHook) Fire(entry *logrus.Entry) error {
	span := trace.SpanFromContext(entry.Context)
	if span != nil && span.IsRecording() {
		spanContext := span.SpanContext()
		if spanContext.IsValid() {
			entry.Data["trace_id"] = spanContext.TraceID().String()
			entry.Data["span_id"] = spanContext.SpanID().String()
		}
	}
	return nil
}

func main() {
	configPath := flag.String("config", "/etc/libvirt-volume-provisioner/config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Initialize logging from config
	logConfig := logging.Config{
		Level:        cfg.Logging.Level,
		Format:       cfg.Logging.Format,
		Output:       cfg.Logging.File,
		SamplingRate: cfg.Logging.SamplingRate,
	}

	logger, err := logging.NewLogger(logConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize logger: %v\n", err)
		os.Exit(1)
	}

	logrus.SetOutput(logger.Writer())
	logrus.SetFormatter(logger.Formatter)
	logrus.SetLevel(logger.Level)

	if cfg.Logging.LokiURL != "" {
		lokiLabels := map[string]string{
			"service": "libvirt-volume-provisioner",
			"version": version,
		}
		logrus.AddHook(logging.NewLokiHook(cfg.Logging.LokiURL, lokiLabels))
		logger.Info("Loki logging enabled")
	}

	if cfg.Logging.WebhookURL != "" {
		hookConfig := logging.HookConfig{
			Type:          "webhook",
			URL:           cfg.Logging.WebhookURL,
			Headers:       map[string]string{"Content-Type": "application/json"},
			BufferSize:    50,
			FlushInterval: 30 * time.Second,
		}
		webhookHook := logging.NewExternalLogHook(hookConfig)
		defer webhookHook.Close()
		logrus.AddHook(webhookHook)
		logger.Info("Webhook logging enabled")
	}

	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = logger.Writer()

	ctx := context.Background()
	tp, err := initTracing(ctx, cfg.Tracing)
	if err != nil && !errors.Is(err, errOTLPNotConfigured) {
		logrus.WithError(err).Warn("Failed to initialize OTLP tracing; continuing without tracing")
	}
	if tp != nil {
		defer func() {
			if err := tp.Shutdown(ctx); err != nil {
				logrus.WithError(err).Error("Failed to shutdown tracer provider")
			}
		}()
		logrus.AddHook(&logCorrelationHook{})
		logrus.Info("OTLP tracing initialized")
	}

	logrus.WithFields(logrus.Fields{
		"version":   version,
		"buildTime": buildTime,
		"config":    *configPath,
	}).Info("Starting libvirt-volume-provisioner")

	listenAddr := fmt.Sprintf(":%d", cfg.Server.Port)

	logrus.Info("Initializing MinIO client...")
	minioClient, err := minio.NewClient(cfg.MinIO)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to initialize MinIO client")
	}
	logrus.Info("MinIO client initialized successfully")

	logrus.Info("Initializing LVM manager...")
	lvmManager, err := lvm.NewManager(cfg.LVM)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to initialize LVM manager")
	}
	logrus.Info("LVM manager initialized successfully")

	logrus.Info("Initializing authentication validator...")
	authValidator, err := auth.NewValidator(cfg.Server)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to initialize auth validator")
	}
	logrus.Info("Authentication validator initialized successfully")

	logrus.Info("Initializing storage...")
	store, err := storage.NewStore(cfg.Server.DBPath)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to initialize storage")
	}
	logrus.Info("Storage initialized successfully")

	logrus.Info("Initializing libvirt pool manager...")
	libvirtPool, err := libvirt.NewPoolManager(cfg.Libvirt)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to initialize libvirt pool manager")
	}
	logrus.Info("Libvirt pool manager initialized successfully")

	cacheMaxAge, err := time.ParseDuration(cfg.Cache.MaxAge)
	if err != nil {
		logrus.WithError(err).Fatal("Invalid cache.max_age in config")
	}
	cacheEvictionInterval, err := time.ParseDuration(cfg.Cache.EvictionInterval)
	if err != nil {
		logrus.WithError(err).Fatal("Invalid cache.eviction_interval in config")
	}

	appMetrics := metrics.NewMetrics()

	jobTimeout := time.Duration(cfg.Libvirt.JobTimeoutMinutes) * time.Minute
	jobManager := jobs.NewManager(
		minioClient, lvmManager, libvirtPool, store, appMetrics,
		cfg.Libvirt.MaxConcurrent, jobTimeout, cacheMaxAge, cacheEvictionInterval,
	)

	if err := jobManager.RecoverJobs(); err != nil {
		logrus.WithError(err).Fatal("Failed to recover jobs from previous run")
	}

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(logger.RequestLogger())

	if tp != nil {
		router.Use(otelgin.Middleware("libvirt-volume-provisioner", otelgin.WithFilter(func(r *http.Request) bool {
			return r.URL.Path != "/metrics"
		})))
	}

	apiHandler := api.NewHandler(jobManager, libvirtPool, appMetrics, version, cfg.Libvirt.MaxConcurrent)

	api.SetupRoutes(router, apiHandler, authValidator.Middleware())

	var srv *http.Server
	if !authValidator.IsClientCALoaded() {
		srv = &http.Server{
			Addr:              listenAddr,
			Handler:           router,
			ReadTimeout:       15 * time.Second,
			ReadHeaderTimeout: 15 * time.Second,
			WriteTimeout:      15 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
	} else {
		cert, err := tls.LoadX509KeyPair(cfg.Server.TLSCert, cfg.Server.TLSKey)
		if err != nil {
			logrus.WithError(err).Fatal("Failed to load server certificate")
		}

		srv = &http.Server{
			Addr:              listenAddr,
			Handler:           router,
			ReadTimeout:       15 * time.Second,
			ReadHeaderTimeout: 15 * time.Second,
			WriteTimeout:      15 * time.Second,
			IdleTimeout:       60 * time.Second,
			TLSConfig: &tls.Config{
				Certificates: []tls.Certificate{cert},
				ClientAuth:   tls.VerifyClientCertIfGiven,
				ClientCAs:    authValidator.GetClientCAs(),
				MinVersion:   tls.VersionTLS12,
			},
		}
	}

	go func() {
		if !authValidator.IsClientCALoaded() {
			logrus.WithFields(logrus.Fields{
				"addr": listenAddr,
				"mode": "HTTP",
			}).Info("Starting libvirt-volume-provisioner server")
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logrus.WithError(err).Fatal("Failed to start HTTP server")
			}
		} else {
			logrus.WithFields(logrus.Fields{
				"addr": listenAddr,
				"mode": "HTTPS",
			}).Info("Starting libvirt-volume-provisioner server")
			if err := srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
				logrus.WithError(err).Fatal("Failed to start HTTPS server")
			}
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logrus.Info("Shutting down server...")

	jobManager.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logrus.WithError(err).Fatal("Server forced to shutdown")
	}

	logrus.Info("Server exited gracefully")
}
