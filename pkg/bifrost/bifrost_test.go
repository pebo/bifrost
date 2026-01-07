package bifrost

import (
	"context"
	"log/slog"

	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/pebo/bifrost/pkg/config"

	"github.com/stretchr/testify/assert"
)

func TestNew(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	t.Run("should create new bifrost instance with valid config", func(t *testing.T) {
		cfg := &config.Config{
			Server: config.Server{
				Port: 8080,
			},
			Routes: []config.Route{
				{
					Path: "/service1",
					Target: config.Target{
						URL: "http://localhost:8081",
					},
				},
				{
					Path:    "/service2",
					Methods: []string{"GET", "POST"},
					Target: config.Target{
						URL: "http://localhost:8082",
					},
				},
			},
		}

		b, err := New(cfg, logger)
		assert.NoError(t, err)
		assert.NotNil(t, b)
		assert.NotNil(t, b.Handler)
	})

	t.Run("should return error for invalid route config", func(t *testing.T) {
		cfg := &config.Config{
			Routes: []config.Route{
				{
					Path: "/invalid",
					Target: config.Target{
						URL: "http://invalid-url:-1", // Invalid port
					},
				},
			},
		}

		b, err := New(cfg, logger)
		assert.Error(t, err)
		assert.Nil(t, b)
	})

	t.Run("should fail on invalid port number", func(t *testing.T) {
		cfg := &config.Config{
			Server: config.Server{
				Port: 0,
			},
		}
		b, err := New(cfg, logger)
		assert.Error(t, err)
		assert.Nil(t, b)
		assert.Contains(t, err.Error(), "port must be between")
	})

	t.Run("should fail on port number too high", func(t *testing.T) {
		cfg := &config.Config{
			Server: config.Server{
				Port: 70000,
			},
		}
		b, err := New(cfg, logger)
		assert.Error(t, err)
		assert.Nil(t, b)
		assert.Contains(t, err.Error(), "port must be between")
	})
}

func TestBifrost_Handler(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	targetService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Hello from target"))
	}))
	defer targetService.Close()

	cfg := &config.Config{
		Server: config.Server{
			Port:    8080,
			Timeout: 5 * time.Second,
		},
		Routes: []config.Route{
			{
				Path: "/test",
				Target: config.Target{
					URL: targetService.URL,
				},
			},
			{
				Path:    "/test-get",
				Methods: []string{"GET"},
				Target: config.Target{
					URL: targetService.URL,
				},
			},
		},
	}

	b, err := New(cfg, logger)
	assert.NoError(t, err)

	t.Run("should route to target for any method", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		rr := httptest.NewRecorder()
		b.Handler.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, "Hello from target", rr.Body.String())
	})

	t.Run("should route to target for specified method", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test-get", nil)
		rr := httptest.NewRecorder()
		b.Handler.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, "Hello from target", rr.Body.String())
	})

	t.Run("should not route for unspecified method", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/test-get", nil)
		rr := httptest.NewRecorder()
		b.Handler.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	})

	t.Run("should return 404 for unknown route", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/unknown", nil)
		rr := httptest.NewRecorder()
		b.Handler.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})
}

func TestBifrost_Config(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := &config.Config{
		Server: config.Server{Port: 9999},
	}

	b, err := New(cfg, logger)
	assert.NoError(t, err)

	retrievedCfg := b.Config()
	assert.Equal(t, cfg.Server.Port, retrievedCfg.Server.Port)
}

func TestHealthCheckEndpoint(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := &config.Config{
		Server: config.Server{Port: 8080},
	}

	b, err := New(cfg, logger)
	assert.NoError(t, err)

	t.Run("should return 200 OK on GET /health", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/health", nil)
		rr := httptest.NewRecorder()

		b.Handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
		assert.JSONEq(t, `{"status":"ok"}`, rr.Body.String())
	})

	t.Run("should return 405 on POST /health", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/health", nil)
		rr := httptest.NewRecorder()

		b.Handler.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	})
}

func TestGracefulShutdown(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	t.Run("should wait for active requests to complete", func(t *testing.T) {
		requestStarted := make(chan struct{})

		// Create a backend that takes some time to respond
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(requestStarted) // Signal that the request has started
			time.Sleep(500 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))
		defer backend.Close()

		cfg := &config.Config{
			Server: config.Server{Port: 8080},
			Routes: []config.Route{
				{
					Path: "/slow",
					Target: config.Target{
						URL: backend.URL,
					},
				},
			},
		}

		b, err := New(cfg, logger)
		assert.NoError(t, err)

		// Start a request in a goroutine
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/slow", nil)
			w := httptest.NewRecorder()
			b.Handler.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code)
		}()

		// Wait for the request to start
		<-requestStarted
		assert.Equal(t, int64(1), b.ActiveRequests())

		// Start shutdown with enough time
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		err = b.Shutdown(ctx)
		assert.NoError(t, err)
		assert.Equal(t, int64(0), b.ActiveRequests())

		wg.Wait()
	})

	t.Run("should timeout if requests take too long", func(t *testing.T) {
		requestStarted := make(chan struct{})
		// Create a backend that takes longer than our timeout
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(requestStarted)
			time.Sleep(2 * time.Second)
			w.WriteHeader(http.StatusOK)
		}))
		defer backend.Close()

		cfg := &config.Config{
			Server: config.Server{Port: 8080},
			Routes: []config.Route{
				{
					Path: "/veryslow",
					Target: config.Target{
						URL: backend.URL,
					},
				},
			},
		}

		b, err := New(cfg, logger)
		assert.NoError(t, err)

		// Start a slow request
		go func() {
			req := httptest.NewRequest("GET", "/veryslow", nil)
			w := httptest.NewRecorder()
			b.Handler.ServeHTTP(w, req)
		}()

		// Wait for the request to start
		<-requestStarted
		assert.Equal(t, int64(1), b.ActiveRequests())

		// Shutdown with a short timeout
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()

		err = b.Shutdown(ctx)
		assert.Error(t, err)
		assert.Equal(t, context.DeadlineExceeded, err)
		assert.Equal(t, int64(1), b.ActiveRequests()) // Request still active
	})

	t.Run("should complete immediately with no active requests", func(t *testing.T) {
		cfg := &config.Config{
			Server: config.Server{Port: 8080},
		}

		b, err := New(cfg, logger)
		assert.NoError(t, err)
		assert.Equal(t, int64(0), b.ActiveRequests())

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()

		err = b.Shutdown(ctx)
		assert.NoError(t, err)
	})

	t.Run("should track multiple concurrent requests", func(t *testing.T) {
		backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(200 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))
		defer backend.Close()

		cfg := &config.Config{
			Server: config.Server{Port: 8080},
			Routes: []config.Route{
				{
					Path: "/concurrent",
					Target: config.Target{
						URL: backend.URL,
					},
				},
			},
		}

		b, err := New(cfg, logger)
		assert.NoError(t, err)

		// Start multiple concurrent requests
		var wg sync.WaitGroup
		numRequests := 5
		for i := 0; i < numRequests; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				req := httptest.NewRequest("GET", "/concurrent", nil)
				w := httptest.NewRecorder()
				b.Handler.ServeHTTP(w, req)
			}()
		}

		// Wait for all requests to start
		time.Sleep(50 * time.Millisecond)
		assert.Equal(t, int64(numRequests), b.ActiveRequests())

		// Wait for completion
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		err = b.Shutdown(ctx)
		assert.NoError(t, err)
		assert.Equal(t, int64(0), b.ActiveRequests())

		wg.Wait()
	})
}

func TestValidateRoute(t *testing.T) {
	tests := []struct {
		name    string
		route   config.Route
		index   int
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid route with target URL",
			route: config.Route{
				Path: "/users",
				Target: config.Target{
					URL: "http://localhost:8080",
				},
			},
			index:   0,
			wantErr: false,
		},
		{
			name: "valid route with methods",
			route: config.Route{
				Path:    "/posts",
				Methods: []string{"GET", "POST"},
				Target: config.Target{
					URL: "http://api.example.com",
				},
			},
			index:   0,
			wantErr: false,
		},
		{
			name: "empty path",
			route: config.Route{
				Path: "",
				Target: config.Target{
					URL: "http://localhost:8080",
				},
			},
			index:   0,
			wantErr: true,
			errMsg:  "path cannot be empty",
		},
		{
			name: "path without leading slash",
			route: config.Route{
				Path: "users",
				Target: config.Target{
					URL: "http://localhost:8080",
				},
			},
			index:   1,
			wantErr: true,
			errMsg:  "path must start with '/'",
		},
		{
			name: "empty target URL",
			route: config.Route{
				Path: "/api",
				Target: config.Target{
					URL: "",
				},
			},
			index:   2,
			wantErr: true,
			errMsg:  "target URL cannot be empty",
		},
		{
			name: "invalid HTTP method",
			route: config.Route{
				Path:    "/resource",
				Methods: []string{"GET", "INVALID"},
				Target: config.Target{
					URL: "http://localhost:8080",
				},
			},
			index:   3,
			wantErr: true,
			errMsg:  "invalid HTTP method",
		},
		{
			name: "valid HTTP methods uppercase",
			route: config.Route{
				Path:    "/resource",
				Methods: []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"},
				Target: config.Target{
					URL: "http://localhost:8080",
				},
			},
			index:   4,
			wantErr: false,
		},
		{
			name: "valid HTTP methods lowercase",
			route: config.Route{
				Path:    "/resource",
				Methods: []string{"get", "post", "delete"},
				Target: config.Target{
					URL: "http://localhost:8080",
				},
			},
			index:   5,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRoute(tt.route, tt.index)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestNew_RouteValidation(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	t.Run("should fail on empty path", func(t *testing.T) {
		cfg := &config.Config{
			Server: config.Server{Port: 8080},
			Routes: []config.Route{
				{
					Path: "",
					Target: config.Target{
						URL: "http://localhost:8080",
					},
				},
			},
		}
		b, err := New(cfg, logger)
		assert.Error(t, err)
		assert.Nil(t, b)
		assert.Contains(t, err.Error(), "path cannot be empty")
	})

	t.Run("should fail on empty target URL", func(t *testing.T) {
		cfg := &config.Config{
			Server: config.Server{Port: 8080},
			Routes: []config.Route{
				{
					Path: "/api",
					Target: config.Target{
						URL: "",
					},
				},
			},
		}
		b, err := New(cfg, logger)
		assert.Error(t, err)
		assert.Nil(t, b)
		assert.Contains(t, err.Error(), "target URL cannot be empty")
	})

	t.Run("should fail on invalid HTTP method", func(t *testing.T) {
		cfg := &config.Config{
			Server: config.Server{Port: 8080},
			Routes: []config.Route{
				{
					Path:    "/api",
					Methods: []string{"UNKNOWN"},
					Target: config.Target{
						URL: "http://localhost:8080",
					},
				},
			},
		}
		b, err := New(cfg, logger)
		assert.Error(t, err)
		assert.Nil(t, b)
		assert.Contains(t, err.Error(), "invalid HTTP method")
	})

	t.Run("should fail on duplicate route pattern", func(t *testing.T) {
		cfg := &config.Config{
			Server: config.Server{Port: 8080},
			Routes: []config.Route{
				{
					Path: "/api",
					Target: config.Target{
						URL: "http://localhost:8080",
					},
				},
				{
					Path: "/api",
					Target: config.Target{
						URL: "http://localhost:8081",
					},
				},
			},
		}
		b, err := New(cfg, logger)
		assert.Error(t, err)
		assert.Nil(t, b)
		assert.Contains(t, err.Error(), "already registered")
	})

	t.Run("should fail on duplicate route with same method", func(t *testing.T) {
		cfg := &config.Config{
			Server: config.Server{Port: 8080},
			Routes: []config.Route{
				{
					Path:    "/users",
					Methods: []string{"GET"},
					Target: config.Target{
						URL: "http://localhost:8080",
					},
				},
				{
					Path:    "/users",
					Methods: []string{"GET"},
					Target: config.Target{
						URL: "http://localhost:8081",
					},
				},
			},
		}
		b, err := New(cfg, logger)
		assert.Error(t, err)
		assert.Nil(t, b)
		assert.Contains(t, err.Error(), "already registered")
	})

	t.Run("should fail on nil config", func(t *testing.T) {
		b, err := New(nil, logger)
		assert.Error(t, err)
		assert.Nil(t, b)
		assert.Contains(t, err.Error(), "config cannot be nil")
	})

	t.Run("should fail on nil logger", func(t *testing.T) {
		cfg := &config.Config{}
		b, err := New(cfg, nil)
		assert.Error(t, err)
		assert.Nil(t, b)
		assert.Contains(t, err.Error(), "logger cannot be nil")
	})
}

type mockFlusher struct {
	http.ResponseWriter
	flushed bool
}

func (m *mockFlusher) Flush() {
	m.flushed = true
}

func TestResponseWriter_Flush(t *testing.T) {
	t.Run("should call Flush on underlying ResponseWriter if it implements http.Flusher", func(t *testing.T) {
		mock := &mockFlusher{ResponseWriter: httptest.NewRecorder()}
		rw := &responseWriter{ResponseWriter: mock}

		rw.Flush()

		assert.True(t, mock.flushed)
	})

	t.Run("should not panic if underlying ResponseWriter does not implement http.Flusher", func(t *testing.T) {
		rw := &responseWriter{ResponseWriter: httptest.NewRecorder()}

		assert.NotPanics(t, func() {
			rw.Flush()
		})
	})
}
