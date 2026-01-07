# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-01-07

### Added
- Initial release of Bifrost reverse proxy
- Declarative YAML-based configuration with JSON schema validation
- Dynamic routing with path parameter mapping (e.g., `/users/{id}`)
- HTTP method-based routing (GET, POST, PUT, DELETE, PATCH, HEAD, OPTIONS)
- GCP OIDC authentication for Cloud Run and other GCP services
- Configurable request timeouts (global and per-route)
- Request body size limits with configurable maximum (default 10MB)
- Header filtering with allowlist support (global and per-route)
- Health check endpoint at `GET /health`
- Graceful shutdown with in-flight request tracking
- OpenTelemetry instrumentation for distributed tracing
- OpenTelemetry metrics export (requests total, duration, active requests)
- Environment variable expansion in configuration files
- GCP Cloud Logging compatible log format
- Comprehensive test coverage across all packages
- Complete package documentation with godoc
- Basic usage example in `examples/basic/`

### Configuration
- Server-level settings: port, timeout, allowed headers, max body size
- Route-level settings: path, methods, target URL, GCP auth, timeout
- Policy support for JWT claim logging
- Telemetry configuration for OTLP exporters

### Packages
- `pkg/bifrost` - Main proxy implementation
- `pkg/config` - Configuration loading and validation
- `pkg/gcplogging` - GCP Cloud Logging format support
- `pkg/telemetry` - OpenTelemetry setup and instrumentation
- `internal/engine` - Core proxy engine with authentication

[Unreleased]: https://github.com/pebo/bifrost/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/pebo/bifrost/releases/tag/v0.1.0
