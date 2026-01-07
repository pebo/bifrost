# Bifrost Docker Compose Example

<img src="../../bifrost-logo.png" alt="Bifrost logo" width="128" />

This example demonstrates how to use Bifrost as a reverse proxy in a Docker Compose setup.

## Overview

This example consists of five services:

1.  `bifrost`: The reverse proxy with OpenTelemetry tracing and metrics enabled.
2.  `dummy-service`: A simple Node.js Express service that Bifrost proxies requests to.
3.  `otel-collector`: OpenTelemetry Collector that receives traces and metrics from Bifrost.
4.  `jaeger`: Jaeger UI for viewing distributed traces.
5.  `prometheus`: Prometheus for viewing metrics.

## How to Run

1.  **Navigate to the example directory:**

    ```bash
    cd examples/docker-compose
    ```

2.  **Start the services:**

    ```bash
    docker compose -f docker-compose.yml up --build
    ```

    This will build the Docker images for both `bifrost` and `dummy-service` and start the containers.

## Testing the Proxy

Once the services are running, you can test the proxy by sending requests to Bifrost on port `8080`.

### GET Request

Send a GET request to `/hello/{name}`. Bifrost will forward it to the `dummy-service`.

```bash
curl http://localhost:8080/hello/world
```

You should see the following response from the `dummy-service`:

```
Hello, world! This is the dummy service.
```

### POST Request

Send a POST request to `/echo` with a JSON body. Bifrost will forward it to the `dummy-service`, which will echo the body back.

```bash
curl -X POST http://localhost:8080/echo -H "Content-Type: application/json" -d '{"message": "testing"}'
```

You should receive the following response:

```json
{"message":"testing"}
```

## Viewing Traces

After sending requests to Bifrost, you can view the distributed traces in Jaeger:

1. Open your browser and navigate to [http://localhost:16686](http://localhost:16686)
2. Select **bifrost-proxy** from the Service dropdown
3. Click **Find Traces** to see all traces
4. Click on any trace to view the detailed span information showing:
   - HTTP request spans from the bifrost handler
   - Proxy request spans with route metadata
   - GCP auth events (if enabled)
   - Request timing and attributes

## Viewing Metrics

Bifrost exports the following metrics to Prometheus:

- `bifrost_requests_total`: Counter of total HTTP requests by method, route, and status code
- `bifrost_request_duration_milliseconds`: Histogram of request duration in milliseconds
- `bifrost_requests_active`: Gauge of currently active HTTP requests

To view metrics:

1. Open your browser and navigate to [http://localhost:9090](http://localhost:9090)
2. In the Prometheus query interface, try these example queries:
   - `bifrost_requests_total` - Total request count
   - `rate(bifrost_requests_total[1m])` - Request rate per second
   - `histogram_quantile(0.95, rate(bifrost_request_duration_milliseconds_bucket[5m]))` - 95th percentile latency
   - `bifrost_requests_active` - Current active requests

**Note:** Metrics are exported every 30 seconds by default, so you may need to wait up to half a minute after sending requests before they appear in Prometheus.

## Files

*   `docker-compose.yml`: Defines all services including the observability stack (run with `docker compose`).
*   `bifrost.Dockerfile`: Dockerfile to build the bifrost service.
*   `bifrost.yaml`: Configuration for Bifrost with telemetry and metrics enabled.
*   `otel-collector-config.yaml`: OpenTelemetry Collector configuration with trace and metric pipelines.
*   `prometheus.yml`: Prometheus configuration for scraping metrics from the OTEL Collector.
*   `dummy-service/`: Contains the source code and Dockerfile for the simple Node.js service.
