package main

import (
	"context"
	"crypto/tls"
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
	"github.com/rossigee/libvirt-volume-provisioner/internal/lvm"
	"github.com/rossigee/libvirt-volume-provisioner/internal/minio"
	"github.com/sirupsen/logrus"
)

// Build information - set at build time
var (
	version   = "dev"
	buildTime = "unknown"
)

func main() {
	// Configure logrus
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})
	logrus.SetLevel(logrus.InfoLevel)

	// Configure Gin
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = logrus.StandardLogger().Writer()

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

	// Initialize components
	logrus.Info("Initializing MinIO client...")
	minioClient, err := minio.NewClient()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to initialize MinIO client")
	}
	logrus.Info("MinIO client initialized successfully")

	logrus.Info("Initializing LVM manager...")
	lvmManager, err := lvm.NewManager()
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

	jobManager := jobs.NewManager(minioClient, lvmManager)

	// Initialize Gin router
	router := gin.New()

	// Add middleware
	router.Use(gin.Recovery())
	router.Use(authValidator.Middleware())

	// Initialize API handlers
	apiHandler := api.NewHandler(jobManager)

	// Setup routes
	api.SetupRoutes(router, apiHandler)

	// Create HTTP server
	var srv *http.Server
	if !authValidator.IsClientCALoaded() {
		// Run HTTP server for development when no client CA is configured
		srv = &http.Server{
			Addr:    fmt.Sprintf("%s:%s", host, port),
			Handler: router,
		}
	} else {
		// Run HTTPS server when client CA is configured
		srv = &http.Server{
			Addr:    fmt.Sprintf("%s:%s", host, port),
			Handler: router,
			TLSConfig: &tls.Config{
				ClientAuth: tls.RequireAndVerifyClientCert,
				ClientCAs:  authValidator.GetClientCAs(),
				MinVersion: tls.VersionTLS12,
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
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logrus.WithError(err).Fatal("Failed to start HTTP server")
			}
		} else {
			logrus.WithFields(logrus.Fields{
				"host": host,
				"port": port,
				"mode": "production (HTTPS - client CA configured)",
			}).Info("Starting libvirt-volume-provisioner server")
			// Run HTTPS server
			if err := srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
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
