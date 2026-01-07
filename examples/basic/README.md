# Basic Bifrost Example

This example demonstrates a simple Bifrost proxy setup with minimal configuration.

## What This Example Shows

- Loading configuration from a YAML file
- Creating and starting a Bifrost proxy
- Handling graceful shutdown on SIGINT/SIGTERM
- Basic routing with path parameters

## Running the Example

1. **Start some backend services** (or use existing ones):

```bash
# Example: Simple HTTP server on port 3000
python3 -m http.server 3000 &

# Or use any existing service
```

2. **Update the configuration**:

Edit `config.yaml` and replace the backend URLs with your actual services.

3. **Run the proxy**:

```bash
go run main.go
```

4. **Test the proxy**:

```bash
# Request will be proxied to http://localhost:3000/api/test
curl http://localhost:8080/api/test

# Request will be proxied to http://localhost:3001/v1/users/123
curl http://localhost:8080/users/123

# Health check endpoint (built-in)
curl http://localhost:8080/health
```

5. **Stop the proxy** with Ctrl+C (SIGINT):

The proxy will gracefully shutdown, waiting for in-flight requests to complete.

## Configuration Highlights

- **Port**: The proxy listens on port 8080
- **Timeouts**: Global 30s timeout, with route-specific overrides
- **Path Mapping**: Routes can remap paths (e.g., `/users/{id}` → `/v1/users/{id}`)
- **Header Filtering**: Only allowed headers are passed to backends
- **Body Size Limit**: Requests larger than 10MB are rejected

## Next Steps

- See `../gcp-auth/` for GCP authentication examples
- See `../programmatic/` for building configuration in code
- See `../telemetry/` for OpenTelemetry integration
