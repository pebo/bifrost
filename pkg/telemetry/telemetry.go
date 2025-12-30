package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"

	"github.com/pebo/bifrost/internal/engine"
)

// Config holds the telemetry configuration.
type Config struct {
	Enabled       bool
	ServiceName   string
	OTELCollector *OTELCollectorConfig
	Metrics       *MetricsConfig
}

// OTELCollectorConfig configures the OTLP HTTP exporter.
type OTELCollectorConfig struct {
	Endpoint string
	GCPAuth  bool
	Timeout  time.Duration
}

// MetricsConfig controls metric export.
type MetricsConfig struct {
	Enabled bool
}

// ShutdownFunc is returned by Init and should be called to cleanly shutdown telemetry.
type ShutdownFunc func(context.Context) error

// Init initializes OpenTelemetry tracing based on the provided configuration.
// It sets up a global trace provider and returns a shutdown function.
// If telemetry is not enabled, it returns a no-op shutdown function.
func Init(cfg *Config, logger *slog.Logger, tokenProvider engine.TokenProvider) (ShutdownFunc, error) {
	if cfg == nil || !cfg.Enabled {
		logger.Info("telemetry disabled")
		return func(context.Context) error { return nil }, nil
	}

	if cfg.OTELCollector == nil {
		return nil, fmt.Errorf("otel_collector configuration is required when telemetry is enabled")
	}

	// Create resource with service name
	res, err := resource.New(
		context.Background(),
		resource.WithAttributes(
			semconv.ServiceNameKey.String(cfg.ServiceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create telemetry resource: %w", err)
	}

	var shutdownFuncs []func(context.Context) error

	// Setup trace provider
	tp, err := initTraceProvider(cfg, res, logger, tokenProvider)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize trace provider: %w", err)
	}

	otel.SetTracerProvider(tp)
	shutdownFuncs = append(shutdownFuncs, tp.Shutdown)
	logger.Info("trace provider initialized", "endpoint", cfg.OTELCollector.Endpoint)

	// Setup metric provider if enabled
	if cfg.Metrics != nil && cfg.Metrics.Enabled {
		mp, err := initMeterProvider(cfg, res, logger, tokenProvider)
		if err != nil {
			// Shutdown trace provider
			tp.Shutdown(context.Background())
			return nil, fmt.Errorf("failed to initialize meter provider: %w", err)
		}
		otel.SetMeterProvider(mp)
		shutdownFuncs = append(shutdownFuncs, mp.Shutdown)
		logger.Info("meter provider initialized", "endpoint", cfg.OTELCollector.Endpoint)
	}

	// Set global propagator for context propagation
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// Return combined shutdown function
	return func(ctx context.Context) error {
		var errs []error
		for _, fn := range shutdownFuncs {
			if err := fn(ctx); err != nil {
				errs = append(errs, err)
			}
		}
		if len(errs) > 0 {
			return fmt.Errorf("telemetry shutdown errors: %w", errors.Join(errs...))
		}
		logger.Info("telemetry shutdown complete")
		return nil
	}, nil
}

type otlpEndpoint struct {
	hostPort string
	insecure bool
	audience string
}

func parseOTLPEndpoint(raw string) (otlpEndpoint, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return otlpEndpoint{}, fmt.Errorf("otel collector endpoint cannot be empty")
	}

	// If a scheme is provided, validate it and extract the host:port.
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return otlpEndpoint{}, fmt.Errorf("invalid otel collector endpoint %q: %w", raw, err)
		}

		scheme := strings.ToLower(u.Scheme)
		if scheme != "http" && scheme != "https" {
			return otlpEndpoint{}, fmt.Errorf("unsupported otel collector endpoint scheme %q in %q (use http or https)", u.Scheme, raw)
		}
		if u.Host == "" {
			return otlpEndpoint{}, fmt.Errorf("invalid otel collector endpoint %q: missing host", raw)
		}
		if u.Path != "" && u.Path != "/" {
			return otlpEndpoint{}, fmt.Errorf("invalid otel collector endpoint %q: path must be empty (got %q)", raw, u.Path)
		}
		if u.RawQuery != "" || u.Fragment != "" {
			return otlpEndpoint{}, fmt.Errorf("invalid otel collector endpoint %q: query/fragment not supported", raw)
		}

		return otlpEndpoint{
			hostPort: u.Host,
			insecure: scheme == "http",
			audience: scheme + "://" + u.Host,
		}, nil
	}

	// Backwards-compatible: treat a bare host:port as an insecure (http) endpoint.
	return otlpEndpoint{hostPort: raw, insecure: true, audience: raw}, nil
}

// initTraceProvider creates and configures a trace provider with OTLP HTTP exporter.
func initTraceProvider(cfg *Config, res *resource.Resource, logger *slog.Logger, tokenProvider engine.TokenProvider) (*trace.TracerProvider, error) {
	ep, err := parseOTLPEndpoint(cfg.OTELCollector.Endpoint)
	if err != nil {
		return nil, err
	}

	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(ep.hostPort),
		otlptracehttp.WithTimeout(cfg.OTELCollector.Timeout),
	}
	if ep.insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}

	// Add authenticated HTTP client if GCP auth is enabled
	if cfg.OTELCollector.GCPAuth {
		if !strings.Contains(cfg.OTELCollector.Endpoint, "://") {
			return nil, fmt.Errorf("gcp_auth requires otel collector endpoint to include a scheme (e.g. https://collector:4318)")
		}
		if tokenProvider == nil {
			return nil, fmt.Errorf("gcp_auth enabled but no token provider available")
		}
		client := newAuthenticatedHTTPClient(ep.audience, tokenProvider, logger)
		opts = append(opts, otlptracehttp.WithHTTPClient(client))
		logger.Debug("using GCP authenticated HTTP client for trace exporter")
	}

	exporter, err := otlptracehttp.New(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP trace exporter: %w", err)
	}

	tp := trace.NewTracerProvider(
		trace.WithBatcher(exporter),
		trace.WithResource(res),
	)

	return tp, nil
}

// initMeterProvider creates and configures a meter provider with OTLP HTTP exporter.
func initMeterProvider(cfg *Config, res *resource.Resource, logger *slog.Logger, tokenProvider engine.TokenProvider) (*metric.MeterProvider, error) {
	ep, err := parseOTLPEndpoint(cfg.OTELCollector.Endpoint)
	if err != nil {
		return nil, err
	}

	opts := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpoint(ep.hostPort),
		otlpmetrichttp.WithTimeout(cfg.OTELCollector.Timeout),
	}
	if ep.insecure {
		opts = append(opts, otlpmetrichttp.WithInsecure())
	}

	// Add authenticated HTTP client if GCP auth is enabled
	if cfg.OTELCollector.GCPAuth {
		if !strings.Contains(cfg.OTELCollector.Endpoint, "://") {
			return nil, fmt.Errorf("gcp_auth requires otel collector endpoint to include a scheme (e.g. https://collector:4318)")
		}
		if tokenProvider == nil {
			return nil, fmt.Errorf("gcp_auth enabled but no token provider available")
		}
		client := newAuthenticatedHTTPClient(ep.audience, tokenProvider, logger)
		opts = append(opts, otlpmetrichttp.WithHTTPClient(client))
		logger.Debug("using GCP authenticated HTTP client for metric exporter")
	}

	exporter, err := otlpmetrichttp.New(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP metric exporter: %w", err)
	}

	mp := metric.NewMeterProvider(
		metric.WithReader(metric.NewPeriodicReader(exporter, metric.WithInterval(30*time.Second))),
		metric.WithResource(res),
	)

	return mp, nil
}
