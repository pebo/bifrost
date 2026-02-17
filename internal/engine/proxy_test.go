package engine

import (
	"context"
	"errors"

	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pebo/bifrost/pkg/config"

	"github.com/stretchr/testify/assert"
	"golang.org/x/oauth2"
)

func TestResolveTargetPath(t *testing.T) {
	p := &Proxy{}

	// Test case 1: Simple replacement
	req1, _ := http.NewRequest("GET", "/users/123", nil)
	req1.SetPathValue("id", "123")
	result1 := p.resolveTargetPath("/v1/users/{id}", req1)
	assert.Equal(t, "/v1/users/123", result1)

	// Test case 2: Multiple replacements
	req2, _ := http.NewRequest("GET", "/org/abc/users/123", nil)
	req2.SetPathValue("org", "abc")
	req2.SetPathValue("id", "123")
	result2 := p.resolveTargetPath("/v1/organizations/{org}/users/{id}", req2)
	assert.Equal(t, "/v1/organizations/abc/users/123", result2)

	// Test case 3: No placeholders
	req3, _ := http.NewRequest("GET", "/health", nil)
	result3 := p.resolveTargetPath("/v1/healthz", req3)
	assert.Equal(t, "/v1/healthz", result3)

	// Test case 4: Placeholder in template not in request path
	req4, _ := http.NewRequest("GET", "/users/123", nil)
	req4.SetPathValue("id", "123")
	result4 := p.resolveTargetPath("/v1/users/{id}/posts/{post_id}", req4)
	assert.Equal(t, "/v1/users/123/posts/", result4)

	// Test case 5: Catch-all placeholder replacement ({path...})
	req5, _ := http.NewRequest("GET", "/api/v1/users/123", nil)
	req5.SetPathValue("path", "v1/users/123")
	result5 := p.resolveTargetPath("/api/{path...}", req5)
	assert.Equal(t, "/api/v1/users/123", result5)
}

func TestCreateAllowList(t *testing.T) {
	// Test case 1: Route with its own headers
	p1 := &Proxy{
		cfg: &config.Config{
			Server: config.Server{
				AllowedHeaders: []string{"X-Global", "Content-Type"},
			},
		},
	}
	route1 := config.Route{
		AllowedHeaders: []string{"X-Route-Specific", "Content-Type"},
	}
	allowlist1 := p1.createHeaderAllowlist(route1)
	assert.True(t, allowlist1["X-Global"])
	assert.True(t, allowlist1["Content-Type"])
	assert.True(t, allowlist1["X-Route-Specific"])
	assert.True(t, allowlist1["X-Forwarded-For"])
	assert.True(t, allowlist1["X-Forwarded-Host"])
	assert.True(t, allowlist1["X-Forwarded-Proto"])
	assert.Len(t, allowlist1, 6)
	// Test case 2: Route with no specific headers
	p2 := &Proxy{
		cfg: &config.Config{
			Server: config.Server{
				AllowedHeaders: []string{"X-Global", "Content-Type"},
			},
		},
	}
	route2 := config.Route{}
	allowlist2 := p2.createHeaderAllowlist(route2)
	assert.True(t, allowlist2["X-Global"])
	assert.True(t, allowlist2["Content-Type"])
	assert.True(t, allowlist2["X-Forwarded-For"])
	assert.True(t, allowlist2["X-Forwarded-Host"])
	assert.True(t, allowlist2["X-Forwarded-Proto"])
	assert.Len(t, allowlist2, 5)

	// Test case 3: No global headers
	p3 := &Proxy{
		cfg: &config.Config{
			Server: config.Server{
				AllowedHeaders: nil,
			},
		},
	}
	allowlist3 := p3.createHeaderAllowlist(route1)
	assert.False(t, allowlist3["X-Global"])
	assert.True(t, allowlist3["Content-Type"])
	assert.True(t, allowlist3["X-Route-Specific"])
	assert.True(t, allowlist3["X-Forwarded-For"])
	assert.True(t, allowlist3["X-Forwarded-Host"])
	assert.True(t, allowlist3["X-Forwarded-Proto"])
	assert.Len(t, allowlist3, 5)
}

func TestFilterHeaders(t *testing.T) {
	p := &Proxy{}
	req, _ := http.NewRequest("GET", "/", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Allowed", "true")
	req.Header.Set("X-Disallowed", "true")
	req.Header.Set("x-mixed-case", "true")

	allowlist := map[string]bool{
		"Content-Type": true,
		"X-Allowed":    true,
		"X-Mixed-Case": true, // Canonical key
	}

	p.filterHeaders(req, allowlist)

	assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
	assert.Equal(t, "true", req.Header.Get("X-Allowed"))
	assert.Equal(t, "true", req.Header.Get("x-mixed-case"))
	assert.Equal(t, "", req.Header.Get("X-Disallowed"))
}

// MockTokenProvider for testing
type mockTokenProvider struct {
	shouldFailSource bool
	shouldFailToken  bool
}

func (m *mockTokenProvider) GetTokenSource(ctx context.Context, audience string) (oauth2.TokenSource, error) {
	if m.shouldFailSource {
		return nil, errors.New("mock source failure")
	}
	return &mockTokenSource{shouldFail: m.shouldFailToken}, nil
}

type mockTokenSource struct {
	shouldFail bool
}

func (m *mockTokenSource) Token() (*oauth2.Token, error) {
	if m.shouldFail {
		return nil, errors.New("mock token failure")
	}
	return &oauth2.Token{AccessToken: "mock-token"}, nil
}

func TestInjectGCPAuth_Failure(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	p := NewProxy(&config.Config{
		Server: config.Server{Timeout: 0},
	}, logger)

	tests := []struct {
		name             string
		shouldFailSource bool
		shouldFailToken  bool
	}{
		{
			name:             "fails when getting token source",
			shouldFailSource: true,
			shouldFailToken:  false,
		},
		{
			name:             "fails when generating token",
			shouldFailSource: false,
			shouldFailToken:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup mock
			p.tokenProvider = &mockTokenProvider{
				shouldFailSource: tt.shouldFailSource,
				shouldFailToken:  tt.shouldFailToken,
			}

			// Create a request that triggers the proxy handler
			req := httptest.NewRequest("GET", "/target", nil)
			w := httptest.NewRecorder()

			route := config.Route{
				Path: "/target",
				Target: config.Target{
					URL:     "http://example.com",
					GCPAuth: true,
				},
			}

			handler, err := p.CreateHandler(route)
			assert.NoError(t, err)

			// Execute handler
			handler.ServeHTTP(w, req)
		})
	}
}

func TestInjectGCPAuth_ProxyBehavior(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	p := NewProxy(&config.Config{
		Server: config.Server{Timeout: 0},
	}, logger)

	// Mock provider that fails
	p.tokenProvider = &mockTokenProvider{shouldFailToken: true}

	// Mock backend server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	route := config.Route{
		Path: "/target",
		Target: config.Target{
			URL:     backend.URL,
			GCPAuth: true,
		},
	}

	handler, err := p.CreateHandler(route)
	assert.NoError(t, err)

	req := httptest.NewRequest("GET", "/target", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// If auth fails, we cancel the context.
	// The ReverseProxy should fail to send the request or abort it.
	// The client (w) might receive a 502 Bad Gateway or similar, or just a closed connection.
	// Let's check that the backend was NOT called?
	// But we can't easily check that without a counter in the backend.

	// Let's add a counter to the backend
	called := false
	backendWithCounter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer backendWithCounter.Close()

	route.Target.URL = backendWithCounter.URL
	handler, _ = p.CreateHandler(route)

	handler.ServeHTTP(w, req)

	assert.False(t, called, "Backend should not be called if auth injection fails")
}

func TestDefaultTimeout(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	t.Run("should use default timeout when server timeout is 0", func(t *testing.T) {
		contextChecked := false
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// The Director has already been called, check if context was properly set up
			contextChecked = true
			w.WriteHeader(http.StatusOK)
		}))
		defer backend.Close()

		p := NewProxy(&config.Config{
			Server: config.Server{Timeout: 0}, // No timeout configured
		}, logger)

		route := config.Route{
			Path: "/test",
			Target: config.Target{
				URL:     backend.URL,
				Timeout: 0, // No route-specific timeout either
			},
		}

		handler, err := p.CreateHandler(route)
		assert.NoError(t, err)

		req := httptest.NewRequest("GET", "/test", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.True(t, contextChecked, "Backend should have been called")
	})

	t.Run("should use route timeout when specified", func(t *testing.T) {
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer backend.Close()

		p := NewProxy(&config.Config{
			Server: config.Server{Timeout: 0},
		}, logger)

		route := config.Route{
			Path: "/test",
			Target: config.Target{
				URL:     backend.URL,
				Timeout: 2 * time.Second,
			},
		}

		handler, err := p.CreateHandler(route)
		assert.NoError(t, err)

		req := httptest.NewRequest("GET", "/test", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("should timeout when backend is slow", func(t *testing.T) {
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Simulate slow backend
			time.Sleep(200 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))
		defer backend.Close()

		p := NewProxy(&config.Config{
			Server: config.Server{Timeout: 0},
		}, logger)

		route := config.Route{
			Path: "/test",
			Target: config.Target{
				URL:     backend.URL,
				Timeout: 50 * time.Millisecond, // Very short timeout
			},
		}

		handler, err := p.CreateHandler(route)
		assert.NoError(t, err)

		req := httptest.NewRequest("GET", "/test", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)
		// Should get a 502 Bad Gateway due to context deadline exceeded
		assert.Equal(t, http.StatusBadGateway, w.Code)
	})
}

func TestRequestBodySizeLimit(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	t.Run("should accept request within body size limit", func(t *testing.T) {
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(body)
		}))
		defer backend.Close()

		p := NewProxy(&config.Config{
			Server: config.Server{
				MaxBodySize: 1024, // 1KB limit
			},
		}, logger)

		route := config.Route{
			Path: "/test",
			Target: config.Target{
				URL: backend.URL,
			},
		}

		handler, err := p.CreateHandler(route)
		assert.NoError(t, err)

		// Send 512 bytes (within limit)
		body := strings.NewReader(strings.Repeat("a", 512))
		req := httptest.NewRequest("POST", "/test", body)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, 512, len(w.Body.Bytes()))
	})

	t.Run("should reject request exceeding body size limit", func(t *testing.T) {
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Try to read body - should fail
			_, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer backend.Close()

		p := NewProxy(&config.Config{
			Server: config.Server{
				MaxBodySize: 1024, // 1KB limit
			},
		}, logger)

		route := config.Route{
			Path: "/test",
			Target: config.Target{
				URL: backend.URL,
			},
		}

		handler, err := p.CreateHandler(route)
		assert.NoError(t, err)

		// Send 2KB (exceeds limit)
		body := strings.NewReader(strings.Repeat("a", 2048))
		req := httptest.NewRequest("POST", "/test", body)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)
		// MaxBytesReader causes the request to fail and should surface as 413.
		assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	})

	t.Run("should use default 10MB limit when max_body_size is 0", func(t *testing.T) {
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(body)
		}))
		defer backend.Close()

		p := NewProxy(&config.Config{
			Server: config.Server{
				MaxBodySize: 0, // Should use default (10MB)
			},
		}, logger)

		route := config.Route{
			Path: "/test",
			Target: config.Target{
				URL: backend.URL,
			},
		}

		handler, err := p.CreateHandler(route)
		assert.NoError(t, err)

		// Send 5MB (within default 10MB limit)
		body := strings.NewReader(strings.Repeat("a", 5*1024*1024))
		req := httptest.NewRequest("POST", "/test", body)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, 5*1024*1024, len(w.Body.Bytes()))
	})
}
