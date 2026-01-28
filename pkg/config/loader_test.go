package config

import (
	"os"
	"testing"
)

func TestExpandEnvStrict_AllowsDefaultWhenUnset(t *testing.T) {
	_ = os.Unsetenv("NOTSET")

	out, err := expandEnvStrict([]byte("url: ${NOTSET:-http://example}\n"))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if string(out) != "url: http://example\n" {
		t.Fatalf("unexpected output: %q", string(out))
	}
}

func TestExpandEnvStrict_AllowsDefaultWhenEmpty(t *testing.T) {
	t.Setenv("EMPTY", "")

	out, err := expandEnvStrict([]byte("url: ${EMPTY:-http://example}\n"))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if string(out) != "url: http://example\n" {
		t.Fatalf("unexpected output: %q", string(out))
	}
}

func TestExpandEnvStrict_FailsWhenUnsetAndNoDefault(t *testing.T) {
	_ = os.Unsetenv("MISSING")

	_, err := expandEnvStrict([]byte("url: ${MISSING}\n"))
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestExpandEnvStrict_FailsWhenEmptyAndNoDefault(t *testing.T) {
	t.Setenv("EMPTY2", "")

	_, err := expandEnvStrict([]byte("url: ${EMPTY2}\n"))
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestExpandEnvStrict_ExpandsBasicVar(t *testing.T) {
	t.Setenv("HOST", "dummy-service")

	out, err := expandEnvStrict([]byte("url: http://${HOST}:3000\n"))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if string(out) != "url: http://dummy-service:3000\n" {
		t.Fatalf("unexpected output: %q", string(out))
	}
}

func TestValidateConfig_ValidAgainstSchema(t *testing.T) {
	configYAML := []byte("version: \"1.0\"\nserver:\n  port: 8080\n  gcp_project_id: \"test-project\"\n")
	if err := validateConfig(configYAML); err != nil {
		t.Fatalf("expected schema validation to pass, got %v", err)
	}
}

func TestValidateConfig_InvalidAgainstSchema(t *testing.T) {
	// port is a string here, but schema requires integer.
	configYAML := []byte("version: \"1.0\"\nserver:\n  port: \"8080\"\n")
	if err := validateConfig(configYAML); err == nil {
		t.Fatalf("expected schema validation to fail, got nil")
	}
}
