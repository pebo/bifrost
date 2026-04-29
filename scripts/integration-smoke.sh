#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
EXAMPLE_DIR="$ROOT_DIR/examples/docker-compose"

cleanup() {
  local exit_code=$?
  if [[ $exit_code -ne 0 ]]; then
    echo "Integration smoke test failed. Dumping compose logs..."
    docker compose -f "$EXAMPLE_DIR/docker-compose.yml" logs --no-color || true
  fi

  echo "Stopping docker-compose stack..."
  docker compose -f "$EXAMPLE_DIR/docker-compose.yml" down -v --remove-orphans || true

  exit $exit_code
}
trap cleanup EXIT

echo "Starting docker-compose stack..."
docker compose -f "$EXAMPLE_DIR/docker-compose.yml" up -d --build

echo "Waiting for Bifrost health endpoint..."
for attempt in {1..60}; do
  if curl -fsS "http://localhost:8080/health" >/dev/null; then
    break
  fi
  sleep 2
  if [[ "$attempt" -eq 60 ]]; then
    echo "Timed out waiting for Bifrost to become healthy"
    exit 1
  fi
done

echo "Running GET proxy assertion..."
hello_response="$(curl -fsS "http://localhost:8080/hello/world")"
expected_hello="Hello, world! This is the dummy service."
if [[ "$hello_response" != "$expected_hello" ]]; then
  echo "Unexpected GET response"
  echo "Expected: $expected_hello"
  echo "Actual:   $hello_response"
  exit 1
fi

echo "Running POST proxy assertion..."
echo_response="$(curl -fsS -X POST "http://localhost:8080/echo" -H "Content-Type: application/json" -d '{"message":"testing"}')"
if [[ "$echo_response" != '{"message":"testing"}' ]]; then
  echo "Unexpected POST response"
  echo "Expected: {\"message\":\"testing\"}"
  echo "Actual:   $echo_response"
  exit 1
fi

echo "Integration smoke test passed"
