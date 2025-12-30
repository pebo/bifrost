// Package config provides configuration structures and loading capabilities
// for the Bifrost reverse proxy.
//
// Configuration is defined in YAML format and includes server settings,
// routing rules, and optional telemetry configuration. The package handles
// YAML parsing, JSON schema validation, and environment variable expansion.
//
// # Configuration Structure
//
// A typical configuration file includes:
//
//	server:
//	  port: 9000
//	  timeout: 30s
//	  allowed_headers:
//	    - "Content-Type"
//	    - "Authorization"
//	  max_body_size: 10485760  # 10MB
//
//	routes:
//	  - id: "users-api"
//	    path: "/api/users/{id}"
//	    methods: ["GET", "POST"]
//	    target:
//	      url: "https://users-service.example.com"
//	      path: "/v1/users/{id}"
//	      gcp_auth: true
//	      timeout: 5s
//	    allowed_headers:
//	      - "X-Custom-Header"
//
// # Environment Variable Expansion
//
// Configuration files support environment variable expansion with strict mode:
//
//	server:
//	  port: ${PORT}                    # Required to be set
//	  timeout: ${TIMEOUT:-30s}         # Defaults to 30s if unset
//
// Variables without defaults must be set or loading will fail.
//
// # Loading Configuration
//
//	cfg, err := config.Load("config.yaml")
//	if err != nil {
//	    log.Fatalf("failed to load config: %v", err)
//	}
package config

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"time"

	"github.com/a8m/envsubst"
	"github.com/goccy/go-yaml"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

//go:embed config-schema.json
var configSchemaJSON []byte

// Config represents the complete Bifrost proxy configuration.
//
// It includes server settings, routing rules, and optional telemetry
// configuration for OpenTelemetry integration.
type Config struct {
	// Version is an optional configuration version identifier
	Version string `yaml:"version"`
	// Server contains global server configuration
	Server Server `yaml:"server"`
	// Routes defines all routing rules for the proxy
	Routes []Route `yaml:"routes"`
	// Telemetry configures OpenTelemetry tracing and metrics (optional)
	Telemetry *Telemetry `yaml:"telemetry,omitempty"`
}

// Server contains global server configuration settings.
type Server struct {
	// Port is the HTTP port to listen on (required, 1-65535)
	Port int `yaml:"port"`
	// LogLevel sets the logging level (debug, info, warn, error)
	// Defaults to "info" if not specified
	LogLevel string `yaml:"log_level"`
	// AllowedHeaders lists HTTP headers to pass through to backend services
	AllowedHeaders []string `yaml:"allowed_headers"`
	// Timeout is the default request timeout for all routes (e.g., "30s", "1m")
	// Individual routes can override this. Defaults to 30s if not specified.
	Timeout time.Duration `yaml:"timeout"`
	// MaxBodySize is the maximum request body size in bytes
	// Defaults to 10MB (10485760 bytes) if not specified or set to 0
	MaxBodySize int64 `yaml:"max_body_size"`
}

// Route defines a routing rule for proxying requests to a backend service.
type Route struct {
	// ID is a unique identifier for this route (required)
	ID string `yaml:"id"`
	// Path is the request path pattern, e.g., "/api/users/{id}"
	// Supports path parameters in curly braces (required)
	Path string `yaml:"path"`
	// Methods lists allowed HTTP methods (e.g., ["GET", "POST"])
	// If empty, all methods are allowed
	Methods []string `yaml:"methods"`
	// Target specifies the backend service to proxy to (required)
	Target Target `yaml:"target"`
	// Policies defines route-specific policies like claim logging
	Policies Policies `yaml:"policies"`
	// AllowedHeaders lists additional headers to allow for this route
	// These are merged with server-level allowed headers
	AllowedHeaders []string `yaml:"allowed_headers"`
}

// Target specifies the backend service configuration for a route.
type Target struct {
	// URL is the base URL of the backend service (required)
	// Example: "https://service.a.run.app"
	URL string `yaml:"url"`
	// Path is the target path template, supporting parameter substitution
	// Example: "/v1/users/{id}" maps the {id} from the route path
	Path string `yaml:"path"`
	// GCPAuth enables automatic OIDC token injection for GCP services
	// Set to true for Cloud Run, Cloud Functions, etc.
	GCPAuth bool `yaml:"gcp_auth"`
	// Timeout overrides the server-level timeout for this route
	// Falls back to server timeout, then 30s default if not specified
	Timeout time.Duration `yaml:"timeout"`
}

// Policies defines route-specific policy settings.
type Policies struct {
	// LogClaims lists JWT claim names to extract and log for audit purposes
	// Example: ["sub", "email", "name"]
	LogClaims []string `yaml:"log_claims"`
}

// Telemetry configures OpenTelemetry tracing and metrics.
type Telemetry struct {
	// Enabled turns on OpenTelemetry instrumentation
	Enabled bool `yaml:"enabled"`
	// ServiceName identifies this service in traces and metrics
	ServiceName string `yaml:"service_name"`
	// OTELCollector configures the OpenTelemetry collector endpoint
	OTELCollector *OTELCollector `yaml:"otel_collector,omitempty"`
	// Metrics enables metrics collection and export
	Metrics *Metrics `yaml:"metrics,omitempty"`
}

// OTELCollector configures the OpenTelemetry collector connection.
type OTELCollector struct {
	// Endpoint is the OTLP collector URL (e.g., "http://otel-collector:4318")
	Endpoint string `yaml:"endpoint"`
	// GCPAuth enables OIDC token authentication for GCP-hosted collectors
	GCPAuth bool `yaml:"gcp_auth"`
	// Timeout for exporter operations (e.g., "10s")
	Timeout string `yaml:"timeout"`
}

// Metrics configures metrics collection settings.
type Metrics struct {
	// Enabled turns on metrics collection and export
	Enabled bool `yaml:"enabled"`
}

func validateConfig(configYAML []byte) error {
	compiler := jsonschema.NewCompiler()
	const schemaURL = "embedded://bifrost/config-schema.json"
	if err := compiler.AddResource(schemaURL, bytes.NewReader(configSchemaJSON)); err != nil {
		return fmt.Errorf("add embedded config schema: %w", err)
	}
	schema, err := compiler.Compile(schemaURL)
	if err != nil {
		return fmt.Errorf("compile embedded config schema: %w", err)
	}

	var v any
	if err := yaml.Unmarshal(configYAML, &v); err != nil {
		return fmt.Errorf("decode config yaml for validation: %w", err)
	}

	if err := schema.Validate(v); err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}

	return nil
}

func expandEnvStrict(configYAML []byte) ([]byte, error) {
	// Strict mode: fail if any referenced env var is unset or empty.
	return envsubst.BytesRestricted(configYAML, true, true)
}

// Load reads, validates, and parses a Bifrost configuration file.
//
// It performs the following operations:
//  1. Reads the YAML file from the specified path
//  2. Expands environment variables (with strict mode)
//  3. Validates against the embedded JSON schema
//  4. Parses the YAML into a Config struct
//
// Returns an error if the file cannot be read, environment variables
// are missing, validation fails, or the YAML is invalid.
//
// Example:
//
//	cfg, err := config.Load("config.yaml")
//	if err != nil {
//	    log.Fatalf("failed to load config: %v", err)
//	}
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	expanded, err := expandEnvStrict(data)
	if err != nil {
		return nil, fmt.Errorf("expand env vars in config %q: %w", path, err)
	}

	if err := validateConfig(expanded); err != nil {
		return nil, fmt.Errorf("validate config %q: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(expanded, &cfg); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	return &cfg, nil
}
