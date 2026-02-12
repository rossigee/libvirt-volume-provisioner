// Package main provides the entry point for the libvirt-volume-provisioner application.
// This is a service for provisioning LVM volumes from MinIO-hosted disk images.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rossigee/libvirt-volume-provisioner/internal/api"
	"github.com/rossigee/libvirt-volume-provisioner/internal/auth"
	"github.com/rossigee/libvirt-volume-provisioner/internal/jobs"
	"github.com/rossigee/libvirt-volume-provisioner/internal/libvirt"
	"github.com/rossigee/libvirt-volume-provisioner/internal/lvm"
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

// Build information - set at build time
var (
	version   = "dev"
	buildTime = "unknown"
)

func initOTLP(ctx context.Context) (*sdktrace.TracerProvider, error) {
	// Configure OTLP gRPC exporter
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		return nil, errOTLPNotConfigured // OTLP not configured, skip
	}

	exporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(endpoint))
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP exporter: %w", err)
	}

	// Create resource
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String("libvirt-volume-provisioner"),
			semconv.ServiceVersionKey.String(version),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// Create tracer provider
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
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
	// Configure logrus for structured JSON logging
	logrus.SetFormatter(&logrus.JSONFormatter{
		TimestampFormat: time.RFC3339,
	})
	logrus.SetLevel(logrus.InfoLevel)

	// Add log correlation hook for trace context
	logrus.AddHook(&logCorrelationHook{})

	// Configure Gin
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = logrus.StandardLogger().Writer()

	// Initialize OTLP tracing
	ctx := context.Background()
	tp, err := initOTLP(ctx)
	if err != nil && !errors.Is(err, errOTLPNotConfigured) {
		logrus.WithError(err).Fatal("Failed to initialize OTLP tracing")
	}
	if tp != nil {
		defer func() {
			if err := tp.Shutdown(ctx); err != nil {
				logrus.WithError(err).Error("Failed to shutdown tracer provider")
			}
		}()
		logrus.Info("OTLP tracing initialized")
	} else {
		logrus.Info("OTLP tracing not configured (OTEL_EXPORTER_OTLP_ENDPOINT not set)")
	}

	// Log version information
	logrus.WithFields(logrus.Fields{
		"version":   version,
		"buildTime": buildTime,
	}).Info("Starting libvirt-volume-provisioner")

	// Load configuration from environment
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	host := os.Getenv("HOST")
	if host == "" {
		host = "0.0.0.0"
	}

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "./provisioner.db"
	}

	// Initialize components
	logrus.Info("Initializing MinIO client...")
	minioClient, err := minio.NewClient()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to initialize MinIO client")
	}
	logrus.Info("MinIO client initialized successfully")

	logrus.Info("Initializing LVM manager...")
	lvmManager, err := lvm.NewManager("data")
	if err != nil {
		logrus.WithError(err).Fatal("Failed to initialize LVM manager")
	}
	logrus.Info("LVM manager initialized successfully")

	logrus.Info("Initializing authentication validator...")
	authValidator, err := auth.NewValidator()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to initialize auth validator")
	}
	logrus.Info("Authentication validator initialized successfully")

	logrus.Info("Initializing storage...")
	store, err := storage.NewStore(dbPath)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to initialize storage")
	}
	logrus.Info("Storage initialized successfully")

	logrus.Info("Initializing libvirt pool manager...")
	libvirtPool, err := libvirt.NewPoolManager("images")
	if err != nil {
		logrus.WithError(err).Fatal("Failed to initialize libvirt pool manager")
	}
	logrus.Info("Libvirt pool manager initialized successfully")

	jobManager := jobs.NewManager(minioClient, lvmManager, libvirtPool, store)

	// Initialize Gin router
	router := gin.New()

	// Add global middleware
	router.Use(gin.Recovery())

	// Add OpenTelemetry Gin middleware for automatic HTTP span creation
	if tp != nil {
		router.Use(otelgin.Middleware("libvirt-volume-provisioner"))
	}

	// Initialize API handlers
	apiHandler := api.NewHandler(jobManager, version)

	// Setup routes (includes auth middleware for API routes only)
	api.SetupRoutes(router, apiHandler, authValidator.Middleware())

	// Add authentication middleware to all remaining routes
	router.Use(authValidator.Middleware())

	// Create HTTP server
	var srv *http.Server
	if !authValidator.IsClientCALoaded() {
		// Run HTTP server for development when no client CA is configured
		srv = &http.Server{
			Addr:              fmt.Sprintf("%s:%s", host, port),
			Handler:           router,
			ReadTimeout:       15 * time.Second,
			ReadHeaderTimeout: 15 * time.Second,
			WriteTimeout:      15 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
	} else {
		// Load server certificate and key
		serverCertPath := os.Getenv("SERVER_CERT")
		if serverCertPath == "" {
			serverCertPath = "/etc/ssl/certs/server.crt"
		}
		serverKeyPath := os.Getenv("SERVER_KEY")
		if serverKeyPath == "" {
			serverKeyPath = "/etc/ssl/private/server.key"
		}

		cert, err := tls.LoadX509KeyPair(serverCertPath, serverKeyPath)
		if err != nil {
			logrus.WithError(err).Fatal("Failed to load server certificate")
		}

		// Run HTTPS server when client CA is configured
		srv = &http.Server{
			Addr:              fmt.Sprintf("%s:%s", host, port),
			Handler:           router,
			ReadTimeout:       15 * time.Second,
			ReadHeaderTimeout: 15 * time.Second,
			WriteTimeout:      15 * time.Second,
			IdleTimeout:       60 * time.Second,
			TLSConfig: &tls.Config{
				Certificates: []tls.Certificate{cert},
				ClientAuth:   tls.VerifyClientCertIfGiven, // Optional client certs
				ClientCAs:    authValidator.GetClientCAs(),
				MinVersion:   tls.VersionTLS12,
			},
		}
	}

	// Start server in a goroutine
	go func() {
		if !authValidator.IsClientCALoaded() {
			logrus.WithFields(logrus.Fields{
				"host": host,
				"port": port,
				"mode": "development (HTTP - no client CA)",
			}).Info("Starting libvirt-volume-provisioner server")
			// Run HTTP server for development
			err := srv.ListenAndServe()
			if err != nil && err != http.ErrServerClosed {
				logrus.WithError(err).Fatal("Failed to start HTTP server")
			}
		} else {
			logrus.WithFields(logrus.Fields{
				"host": host,
				"port": port,
				"mode": "production (HTTPS - client CA configured)",
			}).Info("Starting libvirt-volume-provisioner server")
			// Run HTTPS server
			err := srv.ListenAndServeTLS("", "")
			if err != nil && err != http.ErrServerClosed {
				logrus.WithError(err).Fatal("Failed to start HTTPS server")
			}
		}
	}()

	// Wait for interrupt signal to gracefully shutdown the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logrus.Info("Shutting down server...")

	// Give outstanding requests 30 seconds to complete
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logrus.WithError(err).Fatal("Server forced to shutdown")
	}

	logrus.Info("Server exited gracefully")
}
