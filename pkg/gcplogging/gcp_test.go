package gcplogging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
	tested := NewSlogGCPLogger(&buf, slog.LevelInfo)

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
