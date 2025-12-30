package gcplogging

// Package gcplogging provides a logger that logs to Google Cloud Logging
// via structured json logging.
// See: https://cloud.google.com/logging/docs/structured-logging

import (
	"context"
	"io"
	"log/slog"
	"time"
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

// gcpSlogHandler wraps another slog.Handler and adds GCP-specific fields to the log record so that it can
// be interpreted by Google Cloud Logging.
type gcpSlogHandler struct {
	handler slog.Handler
}

func newGcpSlogHandler(h slog.Handler) *gcpSlogHandler {
	return &gcpSlogHandler{handler: h}
}

func (h *gcpSlogHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelError {
		// Add Error Reporting grouping.
		r.AddAttrs(slog.String("@type", errorReportingType))
	}
	return h.handler.Handle(ctx, r)
}

func (h *gcpSlogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

func (h *gcpSlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &gcpSlogHandler{handler: h.handler.WithAttrs(attrs)}
}

func (h *gcpSlogHandler) WithGroup(name string) slog.Handler {
	return &gcpSlogHandler{handler: h.handler.WithGroup(name)}
}

func NewSlogGCPLogger(w io.Writer, level slog.Level) *slog.Logger {
	gcpJSONHandler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		ReplaceAttr: replaceAttr,
		Level:       level,
	})
	return slog.New(newGcpSlogHandler(gcpJSONHandler))
}
