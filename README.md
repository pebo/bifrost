# Bifrost - A Cloud-Native Reverse Proxy

[![CI](https://github.com/pebo/bifrost/workflows/CI/badge.svg)](https://github.com/pebo/bifrost/actions)
[![Go Reference](https://pkg.go.dev/badge/github.com/pebo/bifrost.svg)](https://pkg.go.dev/github.com/pebo/bifrost)
[![Go Report Card](https://goreportcard.com/badge/github.com/pebo/bifrost)](https://goreportcard.com/report/github.com/pebo/bifrost)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/github/go-mod/go-version/pebo/bifrost)](https://github.com/pebo/bifrost)

<img src="./bifrost-logo.png" alt="Bifrost logo" width="128" />

Bifrost is a lightweight, configuration-driven reverse proxy optimized for routing traffic between microservices, particularly in a cloud environment like Google Cloud Platform.

The name Bifrost is taken from Norse mythology, where it is the mythical rainbow bridge connecting the world of mortals (Midgard) to the realm of the gods (Asgard). It symbolizes the proxy's role as a gateway, connecting incoming client requests to the various backend microservices.

## Features

*   **Declarative Routing**: Configure all routes in a single YAML file.
*   **Path Parameter Mapping**: Map parameters from incoming requests to target paths (e.g., `/api/users/{id}` to `/internal/user-service/{id}`).
*   **GCP Authentication**: Automatically injects GCP OIDC identity tokens for secure service-to-service communication on Google Cloud.
*   **Configurable Timeouts**: Set global and per-route timeouts to prevent hanging requests.
*   **Header Filtering**: Control which headers are passed to backend services using an allowlist.
*   **Health Check Endpoint**: Built-in `/health` endpoint for liveness and readiness probes.
*   **Production Ready**: Includes robust error handling and configurable HTTP transport for resilient connections.

## Usage

To run the Bifrost proxy, build and run the main command, providing a path to your configuration file.

```bash
go run ./cmd/main.go -config ./example-config.yaml
```

### Logging

You can control the logging output format using the `--log-format` flag.

*   `--log-format=json` (default): Standard structured JSON logging.
*   `--log-format=gcp`: JSON logging structured for Google Cloud's operations suite (formerly Stackdriver). This format is automatically parsed by Google Cloud Logging, providing better filtering and integration with services like Error Reporting.

If you set `server.gcp_project_id` and use `--log-format=gcp`, Bifrost will also include OpenTelemetry trace context fields in each log entry (when a span is present) so logs can be correlated with Cloud Trace.

Example:
```bash
go run ./cmd/main.go -config ./example-config.yaml --log-format=gcp
```

### Validating Configuration

You can validate the configuration file without starting the server by using the `--validate` flag. This is useful for checking syntax and schema correctness in a CI/CD pipeline.

```bash
go run ./cmd/main.go -config ./example-config.yaml --validate
```
If the file is valid, the command will print "Configuration is valid." and exit successfully.

### Health Check

Bifrost automatically exposes a health check endpoint at `GET /health` that returns a 200 OK status with a JSON response:

```json
{"status":"ok"}
```

This endpoint is useful for:
- Kubernetes liveness and readiness probes
- Load balancer health checks
- Monitoring and alerting systems

Example using curl:
```bash
curl http://localhost:9000/health
```

## Using as a Go Library

Bifrost can be embedded in your own Go applications as a library, giving you programmatic control over configuration and lifecycle management.

### Installation

```bash
go get github.com/pebo/bifrost
```

### Import

```go
import (
	"github.com/pebo/bifrost/pkg/bifrost"
	"github.com/pebo/bifrost/pkg/config"
	"time"
)
```

### Basic Example

```go
package main

import (
	"context"
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
    // Load configuration from YAML file
    cfg, err := config.Load("config.yaml")
    if err != nil {
        log.Fatalf("Failed to load config: %v", err)
    }

    // Create a logger
    logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

    // Create the Bifrost proxy
    proxy, err := bifrost.New(cfg, logger)
    if err != nil {
        log.Fatalf("Failed to create proxy: %v", err)
    }

    // Set up HTTP server
    srv := &http.Server{
        Addr:    ":8080",
        Handler: proxy.Handler,
    }

    // Start server in goroutine
    go func() {
        log.Printf("Starting proxy on %s", srv.Addr)
        if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Fatalf("Server error: %v", err)
        }
    }()

    // Wait for interrupt signal
    quit := make(chan os.Signal, 1)
    signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
    <-quit

    // Graceful shutdown
    log.Println("Shutting down proxy...")
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    if err := proxy.Shutdown(ctx); err != nil {
        log.Fatalf("Proxy shutdown error: %v", err)
    }
    if err := srv.Shutdown(ctx); err != nil {
        log.Fatalf("Server shutdown error: %v", err)
    }

    log.Println("Proxy stopped")
}
```

### Programmatic Configuration

You can also build the configuration programmatically without a YAML file:

```go
cfg := &config.Config{
    Server: config.Server{
        Port:    8080,
        Timeout: 30 * time.Second,
        AllowedHeaders: []string{
            "Content-Type",
            "X-Request-ID",
        },
    },
    Routes: []config.Route{
        {
            ID:      "api-v1",
            Path:    "/api/v1/{path...}",
            Methods: []string{"GET", "POST"},
            Target: config.Target{
                URL:     "https://backend-service.example.com",
                Path:    "/v1/{path...}",
                Timeout: 5 * time.Second,
                GCPAuth: false,
            },
        },
    },
}

// It's recommended to provide a logger.
// Create a logger, for example using the standard library's slog:
logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

proxy, err := bifrost.New(cfg, logger)
if err != nil {
    log.Fatalf("Failed to create proxy: %v", err)
}
```

### Monitoring Active Requests

Bifrost provides a method to check the number of active requests, useful for graceful shutdown logic:

```go
activeCount := proxy.ActiveRequests()
log.Printf("Active requests: %d", activeCount)
```

### Documentation

- Full API documentation: [pkg.go.dev/github.com/pebo/bifrost](https://pkg.go.dev/github.com/pebo/bifrost)
- Complete working example: [examples/basic/](./examples/basic/)

## Configuration

Bifrost is configured using a single YAML file. See `pkg/config/config-schema.json` for the full specification.

### Example `example-config.yaml`

```yaml
# Bifrost Configuration: Asgardian Realms Edition
# This example configures Bifrost to route requests between the Nine Realms.
server:
  port: 9000 # A port worthy of the gods.
  timeout: 10s # Global timeout for all inter-realm communication.
  allowed_headers:
    # Standard headers blessed by Odin for passage.
    - "Content-Type"
    - "Traceparent" # Represents the thread of fate for a request.
    - "X-Cloud-Trace-Context"
    - "X-Request-ID" # A unique rune marking each request.

routes:
  - id: "valhalla-warrior-admission"
    # Route for admitting chosen warriors to the great hall of Valhalla.
    path: "/asgard/valhalla/warriors/{warriorId}"
    methods: ["POST"]
    policies:
      log_claims: ["sub", "name"] # Log the warrior's great deeds (JWT claims) for the sagas.
    allowed_headers:
      # Requires a special token from a Valkyrie to prove the warrior's worth.
      - "X-Valkyrie-Selection-Token"
    target:
      # The location of Valhalla could be written in the runes (environment variables).
      url: "https://valhalla-service.a.run.app"
      path: "/internal/hall-of-the-slain/admit/{warriorId}" # Path remapping to the internal service.
      timeout: 5s # Admission to Valhalla must be swift.
      gcp_auth: true # Secure the path to Valhalla with divine power (GCP IAM).

  - id: "yggdrasil-health-check"
    # An endpoint for checking the health of the World Tree, Yggdrasil.
    path: "/monitor/yggdrasil/health"
    methods: ["GET"]
    target:
      url: "https://yggdrasil-monitor-service.a.run.app"
      path: "/healthz"
      gcp_auth: true

  - id: "midgard-mortal-requests"
    # An endpoint for receiving requests from the mortal realm of Midgard.
    path: "/midgard/mortals/{userId}/profile"
    methods: ["GET"]
    target:
      url: "http://mortal-profile-service.internal:8080"
      path: "/profiles/{userId}"
      gcp_auth: false # Mortals do not possess divine credentials.

  - id: "heimdall-gate-control"
    # A high-priority route for managing access through the Bifrost itself, guarded by Heimdall.
    path: "/heimdall/gate/{action}"
    methods: ["PUT"]
    allowed_headers:
      - "X-Heimdall-Horn-Signal" # Requires a signal from Gjallarhorn.
    target:
      url: "https://heimdall-gate-control.a.run.app"
      path: "/gate-control/v1/{action}"
      gcp_auth: true
      timeout: 2s # Heimdall's actions must be immediate.
```

### Configuration Details

#### `server`

| Key              | Type     | Description                                                                 |
| ---------------- | -------- | --------------------------------------------------------------------------- |
| `port`           | `int`    | The port on which the proxy will listen.                                    |
| `log_level`      | `string` | Logging level: `debug`, `info`, `warn`, `error`. Defaults to `info`.        |
| `gcp_project_id` | `string` | Optional GCP project ID used to format trace IDs for log/trace correlation in Cloud Logging (requires `--log-format=gcp`). |
| `timeout`        | `string` | Global timeout for requests (e.g., "10s", "500ms"). Defaults to 30s if not specified. Can be overridden per route. |
| `max_body_size`  | `int`    | Maximum request body size in bytes. Defaults to 10MB (10485760 bytes). Protects against DoS attacks via large payloads. Set explicitly to 0 for unlimited (not recommended). |
| `allowed_headers` | `[]string` | A list of HTTP header keys that are allowed to be passed to all targets.    |

#### `routes`

A list of route objects.

| Key              | Type     | Description                                                                 |
| ---------------- | -------- | --------------------------------------------------------------------------- |
| `id`             | `string` | A unique identifier for the route.                                          |
| `path`           | `string` | The incoming request path pattern (e.g., `/users/{id}`). The HTTP method is specified in the `methods` field. |
| `methods`        | `[]string` | A list of HTTP methods for the route (e.g., `["GET", "POST"]`). |
| `policies`       | `object` | A set of policies to apply. Currently supports `log_claims: true`.           |
| `allowed_headers` | `[]string` | A list of additional headers to allow for this specific route.              |
| `target`         | `object` | The backend service to which the request will be proxied.                     |

#### `target`

| Key       | Type     | Description                                                                 |
| --------- | -------- | --------------------------------------------------------------------------- |
| `url`     | `string` | The base URL of the target microservice.                                    |
| `path`    | `string` | The path on the target service. Can use placeholders from the incoming path.  |
| `gcp_auth` | `bool`   | If `true`, an OIDC identity token for the target URL will be injected.        |
| `timeout` | `string` | Route-specific timeout that overrides the global `server.timeout`. Falls back to 30s default if neither is set. |
### Observability with OpenTelemetry

Bifrost supports optional OpenTelemetry instrumentation for distributed tracing and metrics.

When used as a library, Bifrost emits telemetry via the global OpenTelemetry APIs; if your application does not configure SDK providers, telemetry will be a no-op.

#### Configuration Example

```yaml
telemetry:
  enabled: true
  service_name: "bifrost-proxy"
  otel_collector:
    endpoint: "http://otel-collector:4318"
    timeout: 10s
    gcp_auth: false  # Set to true for authenticated Cloud Trace/Monitoring
  metrics:
    enabled: true
```

#### Exported Metrics

When metrics are enabled, Bifrost exports the following metrics:

- **`bifrost_requests_total`**: Counter tracking total HTTP requests with labels for method, route, and status code
- **`bifrost_request_duration_milliseconds`**: Histogram of request duration in milliseconds
- **`bifrost_requests_active`**: Gauge showing the number of currently active HTTP requests

**Note:** Metrics are exported every 30 seconds by default, so you may need to wait briefly after sending requests before they appear in your metrics backend.

#### Exported Traces

Bifrost creates distributed trace spans for:
- Incoming HTTP requests with method, path, status code, and response size
- Proxy requests to backend services with route metadata
- Authentication events when GCP auth is enabled

See the [examples/docker-compose](./examples/docker-compose) for a complete working setup with Jaeger and Prometheus.
## Known Limitations

*   **WebSocket Support**: WebSocket connections are not currently supported. The proxy's response writer wrapper does not implement the `http.Hijacker` interface required for WebSocket upgrades. For WebSocket traffic, consider using a dedicated WebSocket gateway or proxy like Envoy or nginx.
