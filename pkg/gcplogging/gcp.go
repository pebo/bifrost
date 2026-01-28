package gcplogging

// Package gcplogging provides a logger that logs to Google Cloud Logging
// via structured json logging.
// See: https://cloud.google.com/logging/docs/structured-logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/trace"
)

func slogLevelToGcpSeverity(level slog.Level) slog.Attr {
	switch {
	case level < slog.LevelInfo:
		return slog.String("severity", "DEBUG")
	case level < slog.LevelWarn:
		return slog.String("severity", "INFO")
	case level < slog.LevelError:
		return slog.String("severity", "WARNING")
	default:
		return slog.String("severity", "ERROR")
	}
}

func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	switch a.Key {
	case slog.TimeKey:
		tv, ok := a.Value.Any().(time.Time)
		if !ok {
			return a
		}
		return slog.String("time", tv.Format(time.RFC3339Nano))
	case slog.LevelKey:
		logLevel, ok := a.Value.Any().(slog.Level)
		if !ok {
			return a
		}
		return slogLevelToGcpSeverity(logLevel)
	case slog.MessageKey:
		return slog.String("message", a.Value.String())
	default:
		return a
	}
}

const errorReportingType = "type.googleapis.com/google.devtools.clouderrorreporting.v1beta1.ReportedErrorEvent"

// formatGCPTraceID formats an OpenTelemetry trace ID in the format expected by GCP Cloud Logging.
// Returns an empty string if the trace ID is invalid or projectID is empty.
func formatGCPTraceID(projectID string, traceID trace.TraceID) string {
	if projectID == "" || !traceID.IsValid() {
		return ""
	}
	return fmt.Sprintf("projects/%s/traces/%s", projectID, traceID.String())
}

// gcpSlogHandler wraps another slog.Handler and adds GCP-specific fields to the log record so that it can
// be interpreted by Google Cloud Logging. If projectID is set, it also adds OpenTelemetry trace context
// for correlation with GCP Cloud Trace.
type gcpSlogHandler struct {
	handler   slog.Handler
	projectID string
}

func newGcpSlogHandler(h slog.Handler, projectID string) *gcpSlogHandler {
	return &gcpSlogHandler{
		handler:   h,
		projectID: projectID,
	}
}

func (h *gcpSlogHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelError {
		// Add Error Reporting grouping.
		r.AddAttrs(slog.String("@type", errorReportingType))
	}

	// Extract trace context from OpenTelemetry and add GCP trace fields for log-trace correlation.
	spanCtx := trace.SpanContextFromContext(ctx)
	if spanCtx.IsValid() {
		// Add trace ID in GCP format if projectID is configured
		if traceID := formatGCPTraceID(h.projectID, spanCtx.TraceID()); traceID != "" {
			r.AddAttrs(slog.String("logging.googleapis.com/trace", traceID))
		}
		// Add span ID for detailed correlation
		r.AddAttrs(slog.String("logging.googleapis.com/spanId", spanCtx.SpanID().String()))
		// Add trace sampled flag
		r.AddAttrs(slog.Bool("logging.googleapis.com/trace_sampled", spanCtx.TraceFlags().IsSampled()))
	}

	return h.handler.Handle(ctx, r)
}

func (h *gcpSlogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

func (h *gcpSlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &gcpSlogHandler{
		handler:   h.handler.WithAttrs(attrs),
		projectID: h.projectID,
	}
}

func (h *gcpSlogHandler) WithGroup(name string) slog.Handler {
	return &gcpSlogHandler{
		handler:   h.handler.WithGroup(name),
		projectID: h.projectID,
	}
}

// NewSlogGCPLogger creates a new slog.Logger configured for Google Cloud Logging.
// The projectID parameter enables trace correlation in GCP Cloud Logging. Pass an empty string
// to disable trace correlation (trace context will not be added to logs).
func NewSlogGCPLogger(w io.Writer, level slog.Level, projectID string) *slog.Logger {
	gcpJSONHandler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		ReplaceAttr: replaceAttr,
		Level:       level,
	})
	return slog.New(newGcpSlogHandler(gcpJSONHandler, projectID))
}
