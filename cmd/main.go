package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/pebo/bifrost/internal/engine"
	"github.com/pebo/bifrost/pkg/bifrost"
	"github.com/pebo/bifrost/pkg/config"
	"github.com/pebo/bifrost/pkg/gcplogging"
	"github.com/pebo/bifrost/pkg/telemetry"
)

func sanitizeLogField(v string) string {
	v = strings.ReplaceAll(v, "\r", "")
	v = strings.ReplaceAll(v, "\n", "")
	return v
}

// loggingMiddleware logs the incoming request
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		//nolint:gosec // Request fields are sanitized with CR/LF stripping before logging.
		slog.Info("incoming request",
			"method", sanitizeLogField(r.Method),
			"uri", sanitizeLogField(r.RequestURI),
			"remote_addr", sanitizeLogField(r.RemoteAddr),
		)
		next.ServeHTTP(w, r)
	})
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func run() error {
	// 1. Handle command-line flags
	configPath := flag.String("config", "example-config.yaml", "path to the config file")
	logFormat := flag.String("log-format", "json", "log format to use (json or gcp)")
	logLevel := flag.String("log-level", "", "log level (debug, info, warn, error) - overrides config file")
	validate := flag.Bool("validate", false, "validate the config file and exit")
	flag.Parse()

	// 2. Load the configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	// 3. Determine log level (CLI flag overrides config)
	levelStr := *logLevel
	if levelStr == "" {
		levelStr = cfg.Server.LogLevel
		if levelStr == "" {
			levelStr = "info" // default
		}
	}

	var level slog.Level
	switch levelStr {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		return fmt.Errorf("invalid log level: %s (must be debug, info, warn, or error)", levelStr)
	}

	// 4. Set up the logger
	var logger *slog.Logger
	switch *logFormat {
	case "gcp":
		logger = gcplogging.NewSlogGCPLogger(os.Stdout, level, cfg.Server.GCPProjectID)
	case "json":
		logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	default:
		return fmt.Errorf("invalid log format: %s", *logFormat)
	}
	slog.SetDefault(logger)

	// 5. Handle validate-only mode
	if *validate {
		fmt.Println("Configuration is valid.")
		return nil
	}

	// 6. Initialize telemetry if configured
	var telemetryShutdown telemetry.ShutdownFunc
	if cfg.Telemetry != nil && cfg.Telemetry.Enabled {
		telCfg, err := parseTelemetryConfig(cfg.Telemetry)
		if err != nil {
			return fmt.Errorf("failed to parse telemetry config: %w", err)
		}

		// Create token provider for GCP auth if needed
		var tokenProvider engine.TokenProvider
		if telCfg.OTELCollector != nil && telCfg.OTELCollector.GCPAuth {
			tokenProvider = engine.NewDefaultTokenProvider()
		}

		telemetryShutdown, err = telemetry.Init(telCfg, logger, tokenProvider)
		if err != nil {
			return fmt.Errorf("failed to initialize telemetry: %w", err)
		}
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := telemetryShutdown(ctx); err != nil {
				logger.Error("telemetry shutdown error", "error", err)
			}
		}()
	}

	// 7. Create a new Bifrost instance using the library package
	b, err := bifrost.New(cfg, logger)
	if err != nil {
		return fmt.Errorf("failed to create bifrost instance: %w", err)
	}

	// 8. Start the HTTP server with graceful shutdown
	addr := fmt.Sprintf(":%d", b.Config().Server.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: loggingMiddleware(b.Handler),
		// Mitigate slowloris-style attacks; keep response streaming intact.
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Channel to listen for errors from the server
	serverErrors := make(chan error, 1)

	// Start server in a goroutine
	go func() {
		logger.Info("Bifrost Bridge active", "addr", addr)
		serverErrors <- srv.ListenAndServe()
	}()

	// Channel to listen for interrupt signals
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, unix.SIGTERM)

	// Block until we receive a signal or server error
	select {
	case err := <-serverErrors:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("server error: %w", err)
		}
		return nil
	case sig := <-shutdown:
		logger.Info("shutdown signal received", "signal", sig.String())

		// Give outstanding requests 10 seconds to complete (Cloud Run's grace period)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Shutdown HTTP server (stops accepting new connections)
		if err := srv.Shutdown(ctx); err != nil {
			logger.Error("server shutdown error", "error", err)
			// Force close if graceful shutdown fails
			if err := srv.Close(); err != nil {
				logger.Error("server force close error", "error", err)
			}
		}

		// Wait for in-flight proxy requests to complete
		if err := b.Shutdown(ctx); err != nil {
			logger.Warn("bifrost shutdown incomplete", "error", err)
		}

		logger.Info("shutdown complete")
		return nil
	}
}

// parseTelemetryConfig converts config.Telemetry to telemetry.Config with proper type conversions.
func parseTelemetryConfig(cfg *config.Telemetry) (*telemetry.Config, error) {
	if cfg == nil {
		return nil, nil
	}

	telCfg := &telemetry.Config{
		Enabled:     cfg.Enabled,
		ServiceName: cfg.ServiceName,
	}

	if cfg.OTELCollector != nil {
		timeoutStr := cfg.OTELCollector.Timeout
		if timeoutStr == "" {
			timeoutStr = "10s"
		}

		timeout, err := time.ParseDuration(timeoutStr)
		if err != nil {
			return nil, fmt.Errorf("invalid otel collector timeout: %w", err)
		}

		telCfg.OTELCollector = &telemetry.OTELCollectorConfig{
			Endpoint: cfg.OTELCollector.Endpoint,
			GCPAuth:  cfg.OTELCollector.GCPAuth,
			Timeout:  timeout,
		}
	}

	if cfg.Metrics != nil {
		telCfg.Metrics = &telemetry.MetricsConfig{
			Enabled: cfg.Metrics.Enabled,
		}
	}

	return telCfg, nil
}
