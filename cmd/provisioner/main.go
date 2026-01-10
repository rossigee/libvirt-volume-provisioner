package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
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
)

func main() {
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
	minioClient, err := minio.NewClient()
	if err != nil {
		log.Fatalf("Failed to initialize MinIO client: %v", err)
	}

	lvmManager, err := lvm.NewManager()
	if err != nil {
		log.Fatalf("Failed to initialize LVM manager: %v", err)
	}

	authValidator, err := auth.NewValidator()
	if err != nil {
		log.Fatalf("Failed to initialize auth validator: %v", err)
	}

	jobManager := jobs.NewManager(minioClient, lvmManager)

	// Initialize Gin router
	router := gin.Default()

	// Add middleware
	router.Use(gin.Logger())
	router.Use(gin.Recovery())
	router.Use(authValidator.Middleware())

	// Initialize API handlers
	apiHandler := api.NewHandler(jobManager)

	// Setup routes
	api.SetupRoutes(router, apiHandler)

	// Create HTTP server
	srv := &http.Server{
		Addr:    fmt.Sprintf("%s:%s", host, port),
		Handler: router,
		TLSConfig: &tls.Config{
			ClientAuth: tls.RequireAndVerifyClientCert,
			ClientCAs:  authValidator.GetClientCAs(),
			MinVersion: tls.VersionTLS12,
		},
		ReadTimeout:       15 * time.Second,
		ReadHeaderTimeout: 15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Start server in a goroutine
	go func() {
		log.Printf("Starting libvirt-volume-provisioner server on %s:%s", host, port)
		if err := srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// Wait for interrupt signal to gracefully shutdown the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")

	// Give outstanding requests 30 seconds to complete
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exited")
}
