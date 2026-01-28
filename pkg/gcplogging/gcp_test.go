package gcplogging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/trace"
)

func assertLogEntry(t *testing.T, buf *bytes.Buffer, expected map[string]any) {
	var logEntry map[string]any
	err := json.NewDecoder(buf).Decode(&logEntry)
	if err != nil {
		t.Fatalf("failed to decode json: %+v", err)
	}
	timeString := logEntry["time"].(string)
	_, err = time.Parse(time.RFC3339, timeString)
	if err != nil {
		t.Fatalf("failed to parse time: %+v", err)
	}
	delete(logEntry, "time")
	assert.Equal(t, expected, logEntry)
}

func TestLogFormatting(t *testing.T) {
	var buf bytes.Buffer
	tested := NewSlogGCPLogger(&buf, slog.LevelInfo, "")

	tested.Debug("debug message", "key1", "value1", "key2", 2)
	assert.Equal(t, "", buf.String())

	tested.Info("info message", "key1", "value1", "key2", 2)
	assertLogEntry(t, &buf, map[string]any{
		"severity": "INFO",
		"message":  "info message",
		"key1":     "value1",
		"key2":     float64(2),
	})
	buf.Reset()

	tested.Warn("warn message")
	assertLogEntry(t, &buf, map[string]any{
		"severity": "WARNING",
		"message":  "warn message",
	})
	buf.Reset()

	tested.Error("error message")
	assertLogEntry(t, &buf, map[string]any{
		"severity": "ERROR",
		"message":  "error message",
		"@type":    errorReportingType,
	})
	buf.Reset()

	tested.Log(context.Background(), slog.Level(123), "level123 message")
	assertLogEntry(t, &buf, map[string]any{
		"severity": "ERROR",
		"message":  "level123 message",
		"@type":    errorReportingType,
	})
}

func TestTraceIDIntegration(t *testing.T) {
	var buf bytes.Buffer
	projectID := "test-project"
	tested := NewSlogGCPLogger(&buf, slog.LevelInfo, projectID)

	// Create a mock trace context
	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)

	tested.InfoContext(ctx, "message with trace")

	var logEntry map[string]any
	err := json.NewDecoder(&buf).Decode(&logEntry)
	assert.NoError(t, err)

	// Verify trace fields are present
	assert.Equal(t, "projects/test-project/traces/4bf92f3577b34da6a3ce929d0e0e4736", logEntry["logging.googleapis.com/trace"])
	assert.Equal(t, "00f067aa0ba902b7", logEntry["logging.googleapis.com/spanId"])
	assert.Equal(t, true, logEntry["logging.googleapis.com/trace_sampled"])
	assert.Equal(t, "INFO", logEntry["severity"])
	assert.Equal(t, "message with trace", logEntry["message"])
}

func TestTraceIDWithoutProjectID(t *testing.T) {
	var buf bytes.Buffer
	tested := NewSlogGCPLogger(&buf, slog.LevelInfo, "") // Empty project ID

	// Create a mock trace context
	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)

	tested.InfoContext(ctx, "message without project")

	var logEntry map[string]any
	err := json.NewDecoder(&buf).Decode(&logEntry)
	assert.NoError(t, err)

	// Verify trace field is NOT present (no project ID), but span ID and sampled flag are
	_, hasTrace := logEntry["logging.googleapis.com/trace"]
	assert.False(t, hasTrace, "trace field should not be present without project ID")
	assert.Equal(t, "00f067aa0ba902b7", logEntry["logging.googleapis.com/spanId"])
	assert.Equal(t, true, logEntry["logging.googleapis.com/trace_sampled"])
}

func TestTraceSampledFalse(t *testing.T) {
	var buf bytes.Buffer
	tested := NewSlogGCPLogger(&buf, slog.LevelInfo, "test-project")

	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: 0,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)

	tested.InfoContext(ctx, "message not sampled")

	var logEntry map[string]any
	err := json.NewDecoder(&buf).Decode(&logEntry)
	assert.NoError(t, err)

	assert.Equal(t, false, logEntry["logging.googleapis.com/trace_sampled"])
}
