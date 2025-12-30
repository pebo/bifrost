// Package main demonstrates a basic Bifrost proxy setup.
//
// This example shows how to:
//   - Load configuration from a YAML file
//   - Create a Bifrost proxy instance
//   - Start an HTTP server
//   - Handle graceful shutdown
package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pebo/bifrost/pkg/bifrost"
	"github.com/pebo/bifrost/pkg/config"
)

func main() {
	// Create a structured logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Load configuration from file
	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Create the Bifrost proxy
	proxy, err := bifrost.New(cfg, logger)
	if err != nil {
		log.Fatalf("Failed to create proxy: %v", err)
	}

	// Create HTTP server
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Server.Port),
		Handler: proxy.Handler,
	}

	// Start server in a goroutine
	go func() {
		logger.Info("Starting Bifrost proxy", "port", cfg.Server.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Server error", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down server...")

	// Gracefully shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Shutdown the HTTP server
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("Server forced to shutdown", "error", err)
	}

	// Shutdown Bifrost (wait for in-flight requests)
	if err := proxy.Shutdown(ctx); err != nil {
		logger.Error("Proxy shutdown error", "error", err)
	}

	logger.Info("Server stopped")
}
