// Package bifrost provides a cloud-native reverse proxy with support for
// dynamic routing, GCP authentication, and OpenTelemetry instrumentation.
//
// Bifrost is designed for routing traffic between microservices, particularly
// in cloud environments like Google Cloud Platform. It offers declarative
// YAML-based configuration, path parameter mapping, automatic OIDC token
// injection for service-to-service authentication, and built-in observability
// through OpenTelemetry.
//
// # Basic Usage
//
// Create a configuration and initialize Bifrost:
//
//	cfg := &config.Config{
//	    Server: config.Server{
//	        Port: 9000,
//	        AllowedHeaders: []string{"Content-Type", "Authorization"},
//	    },
//	    Routes: []config.Route{
//	        {
//	            ID:   "users-api",
//	            Path: "/api/users/{id}",
//	            Methods: []string{"GET", "POST"},
//	            Target: config.Target{
//	                URL: "https://users-service.example.com",
//	                Path: "/v1/users/{id}",
//	            },
//	        },
//	    },
//	}
//
//	logger := slog.Default()
//	proxy, err := bifrost.New(cfg, logger)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	http.ListenAndServe(":9000", proxy.Handler)
//
// # Features
//
// - Declarative routing with path parameter support
// - Automatic GCP OIDC token injection for Cloud Run and other GCP services
// - Request timeout configuration (global and per-route)
// - Header filtering with allowlist support
// - Request body size limits
// - Graceful shutdown with in-flight request tracking
// - OpenTelemetry tracing and metrics integration
// - Health check endpoint
//
// # Configuration
//
// Configuration can be loaded from a YAML file using the config package:
//
//	cfg, err := config.Load("config.yaml")
//	if err != nil {
//	    log.Fatal(err)
//	}
//
// See the config package documentation for full configuration options.
package bifrost

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/pebo/bifrost/internal/engine"
	"github.com/pebo/bifrost/pkg/config"
)

const (
	// shutdownPollInterval is how often we check for active requests during shutdown
	shutdownPollInterval = 100 * time.Millisecond
)

var validHTTPMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "DELETE": true,
	"PATCH": true, "HEAD": true, "OPTIONS": true,
}

// Bifrost represents a configured reverse proxy server instance.
//
// It maintains the HTTP handler, configuration, logger, and tracks active requests
// for graceful shutdown. The Handler field can be used directly with http.Server
// or other HTTP server implementations.
type Bifrost struct {
	// Handler is the HTTP handler that processes incoming requests.
	// Use this with http.ListenAndServe or http.Server.
	Handler        http.Handler
	cfg            *config.Config
	logger         *slog.Logger
	activeRequests int64
}

// New creates a new Bifrost instance from a configuration and logger.
//
// It validates the configuration, sets up all routes with their handlers,
// configures OpenTelemetry instrumentation for tracing and metrics, and
// registers a health check endpoint at GET /health.
//
// The returned Bifrost instance is ready to handle HTTP requests through
// its Handler field.
//
// Returns an error if:
//   - cfg or logger is nil
//   - the port is invalid (not between 1 and 65535)
//   - any route configuration is invalid
//   - route handlers cannot be created
//   - duplicate routes are detected
//
// Example:
//
//	cfg := &config.Config{Server: config.Server{Port: 8080}}
//	logger := slog.Default()
//	proxy, err := New(cfg, logger)
//	if err != nil {
//	    return err
//	}
//	http.ListenAndServe(":8080", proxy.Handler)
func New(cfg *config.Config, logger *slog.Logger) (*Bifrost, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}
	if logger == nil {
		return nil, fmt.Errorf("logger cannot be nil")
	}
	if cfg.Server.Port <= 0 || cfg.Server.Port > 65535 {
		return nil, fmt.Errorf("port must be between 1 and 65535, got %d", cfg.Server.Port)
	}

	proxy := engine.NewProxy(cfg, logger)
	mux := http.NewServeMux()
	registeredRoutes := make(map[string]bool)

	for i, r := range cfg.Routes {
		// Validate route configuration
		if err := validateRoute(r, i); err != nil {
			return nil, err
		}

		handler, err := proxy.CreateHandler(r)
		if err != nil {
			return nil, fmt.Errorf("failed to create handler for route %s: %w", r.Path, err)
		}

		// If no methods are specified, it defaults to all methods.
		if len(r.Methods) == 0 {
			routeKey := fmt.Sprintf("* %s", r.Path)
			if registeredRoutes[routeKey] {
				return nil, fmt.Errorf("route %s (all methods) is already registered", r.Path)
			}
			registeredRoutes[routeKey] = true
			mux.HandleFunc(r.Path, handler)
			logger.Info("registered route", "path", r.Path, "methods", "all")
		} else {
			// Combine methods with the path for the pattern, e.g., "GET /path/{id}"
			for _, method := range r.Methods {
				pattern := fmt.Sprintf("%s %s", strings.ToUpper(method), r.Path)
				if registeredRoutes[pattern] {
					return nil, fmt.Errorf("route %s is already registered", pattern)
				}
				registeredRoutes[pattern] = true
				mux.HandleFunc(pattern, handler)
			}
			logger.Info("registered route", "path", r.Path, "methods", r.Methods)
		}
	}

	// Register health check endpoint
	mux.HandleFunc("GET /health", healthCheckHandler)
	logger.Info("registered health check endpoint", "path", "/health")

	b := &Bifrost{
		cfg:    cfg,
		logger: logger,
	}

	// Get tracer and meter for instrumentation
	tracer := otel.Tracer("bifrost")
	meter := otel.Meter("bifrost")

	// Create metrics
	requestCounter, err := meter.Int64Counter(
		"bifrost_requests_total",
		metric.WithDescription("Total number of HTTP requests"),
		metric.WithUnit("1"),
	)
	if err != nil {
		logger.Warn("failed to create request counter metric", "error", err)
	}

	requestDuration, err := meter.Float64Histogram(
		"bifrost_request_duration_milliseconds",
		metric.WithDescription("Duration of HTTP requests in milliseconds"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		logger.Warn("failed to create request duration metric", "error", err)
	}

	activeRequests, err := meter.Int64UpDownCounter(
		"bifrost_requests_active",
		metric.WithDescription("Number of active HTTP requests"),
		metric.WithUnit("1"),
	)
	if err != nil {
		logger.Warn("failed to create active requests metric", "error", err)
	}

	// Wrap handler with request tracking, tracing, and metrics
	b.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Start a span for the incoming request
		ctx, span := tracer.Start(r.Context(), "http.request",
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("http.method", r.Method),
				attribute.String("http.target", r.URL.Path),
				attribute.String("http.host", r.Host),
			),
		)
		defer span.End()

		// Update request with span context
		r = r.WithContext(ctx)

		// Track active requests
		atomic.AddInt64(&b.activeRequests, 1)
		if activeRequests != nil {
			activeRequests.Add(ctx, 1)
		}
		defer func() {
			atomic.AddInt64(&b.activeRequests, -1)
			if activeRequests != nil {
				activeRequests.Add(ctx, -1)
			}
		}()

		// Capture response status
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		mux.ServeHTTP(rw, r)

		// Prefer matched pattern to avoid high-cardinality metrics/spans.
		routeLabel := r.Pattern
		if routeLabel == "" {
			routeLabel = r.URL.Path
		}

		// Record metrics
		duration := time.Since(start)
		attrs := []attribute.KeyValue{
			attribute.String("http.method", r.Method),
			attribute.String("http.route", routeLabel),
			attribute.Int("http.status_code", rw.statusCode),
		}

		if requestCounter != nil {
			requestCounter.Add(ctx, 1, metric.WithAttributes(attrs...))
		}
		if requestDuration != nil {
			requestDuration.Record(ctx, float64(duration.Milliseconds()), metric.WithAttributes(attrs...))
		}

		// Add span attributes
		span.SetName(r.Method + " " + routeLabel)
		span.SetAttributes(
			attribute.Int("http.status_code", rw.statusCode),
			attribute.String("http.route", routeLabel),
			attribute.Int64("http.response_content_length", rw.bytesWritten),
		)
	})

	return b, nil
}

// responseWriter wraps http.ResponseWriter to capture status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int64
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += int64(n)
	return n, err
}

// Flush implements the http.Flusher interface to allow streaming responses
func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap returns the underlying http.ResponseWriter, allowing
// http.NewResponseController to access optional interfaces (e.g.,
// http.Hijacker) supported by the wrapped writer.
func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

// healthCheckHandler provides a health check endpoint for monitoring and orchestration.
// It returns HTTP 200 OK with a JSON response body indicating the service is running.
// This endpoint is automatically registered at GET /health and can be used for:
//   - Kubernetes liveness/readiness probes
//   - Load balancer health checks
//   - Monitoring systems
func healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(`{"status":"ok"}`)); err != nil {
		slog.Error("failed to write health check response", "error", err)
	}
}

// validateRoute checks that a route has valid configuration.
func validateRoute(r config.Route, index int) error {
	if strings.TrimSpace(r.ID) == "" {
		return fmt.Errorf("route %d: id cannot be empty", index)
	}

	if strings.TrimSpace(r.Path) == "" {
		return fmt.Errorf("route %d: path cannot be empty", index)
	}

	if !strings.HasPrefix(r.Path, "/") {
		return fmt.Errorf("route %d: path must start with '/', got %q", index, r.Path)
	}

	if strings.TrimSpace(r.Target.URL) == "" {
		return fmt.Errorf("route %d: target URL cannot be empty", index)
	}

	targetURL, err := url.Parse(r.Target.URL)
	if err != nil || targetURL.Scheme == "" || targetURL.Host == "" {
		return fmt.Errorf("route %d: target URL must be absolute with scheme and host, got %q", index, r.Target.URL)
	}

	// Validate HTTP methods if specified
	for _, method := range r.Methods {
		normalizedMethod := strings.ToUpper(method)
		if !validHTTPMethods[normalizedMethod] {
			return fmt.Errorf("route %d: invalid HTTP method %q", index, normalizedMethod)
		}
	}

	return nil
}

// Config returns the configuration used by the Bifrost instance.
//
// This provides read-only access to the configuration that was provided
// during initialization. Modifying the returned configuration will not
// affect the running proxy.
func (b *Bifrost) Config() *config.Config {
	return b.cfg
}

// Shutdown gracefully shuts down the Bifrost instance.
//
// It waits for all in-flight requests to complete before returning.
// The shutdown process polls active requests every 100ms until all
// requests have finished or the provided context is cancelled.
//
// Returns nil when all requests complete successfully, or ctx.Err()
// if the context deadline/cancellation occurs before all requests finish.
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
//	defer cancel()
//	if err := proxy.Shutdown(ctx); err != nil {
//	    log.Printf("shutdown error: %v", err)
//	}
func (b *Bifrost) Shutdown(ctx context.Context) error {
	b.logger.Info("initiating graceful shutdown", "active_requests", atomic.LoadInt64(&b.activeRequests))

	// Poll active requests until they're all done or context is cancelled
	ticker := time.NewTicker(shutdownPollInterval)
	defer ticker.Stop()

	for {
		active := atomic.LoadInt64(&b.activeRequests)
		if active == 0 {
			b.logger.Info("all requests completed, shutdown complete")
			return nil
		}

		select {
		case <-ctx.Done():
			b.logger.Warn("shutdown deadline exceeded", "active_requests", active)
			return ctx.Err()
		case <-ticker.C:
			// Continue polling
		}
	}
}

// ActiveRequests returns the current number of in-flight HTTP requests.
//
// This can be useful for monitoring and determining when it's safe to
// shut down the server. The count is updated atomically and is
// thread-safe.
func (b *Bifrost) ActiveRequests() int64 {
	return atomic.LoadInt64(&b.activeRequests)
}
