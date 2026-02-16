# Bifrost Agent Guide

## Overview
Bifrost is a lightweight, config-driven reverse proxy in Go for routing traffic between microservices, with first-class GCP support.

## What matters most
- Declarative routing from YAML config.
- Path parameter mapping (e.g., `{id}`).
- Optional GCP identity-token auth for upstream calls.
- Observability via OpenTelemetry and GCP-friendly logging.

## Key paths
- `cmd/main.go`: CLI entrypoint.
- `internal/engine/`: core proxy behavior (`proxy.go`, `auth.go`, `claims.go`).
- `pkg/config/`: config loading + schema validation.
- `pkg/gcplogging/`, `pkg/telemetry/`: logging and telemetry helpers.
- `example-config.yaml`: reference config.

## Local workflow
- Go version: `go 1.25` (see `go.mod`).
- Run: `go run ./cmd/main.go -config ./example-config.yaml`
- Validate config only: `go run ./cmd/main.go -config ./example-config.yaml --validate`
- Test: `go test ./...`
- Format: `gofmt`

## Change guidance
- Keep changes focused and minimal; match existing style.
- Update `CHANGELOG.md` when behavior, config, flags, or observability output changes in a user-visible way.
- For routing/config features, update all of:
  1. `pkg/config/loader.go`
  2. `internal/engine/proxy.go`
  3. `pkg/config/config-schema.json`
  4. `example-config.yaml`
  5. Relevant tests (`*_test.go`)
- For request-flow issues, start with `internal/engine/proxy_test.go`.
- Use `examples/basic/` or `examples/docker-compose/` for end-to-end checks.
