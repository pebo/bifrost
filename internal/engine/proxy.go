package engine

import (
	"context"
	"fmt"

	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/pebo/bifrost/pkg/config"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// placeholderRegexp matches path template placeholders like {id} and Go ServeMux
// catch-all placeholders like {path...}.
var placeholderRegexp = regexp.MustCompile(`\{(\w+)(?:\.\.\.)?\}`)

// defaultProxyTimeout is used when neither server-level nor route-level timeout is configured.
// This prevents requests from hanging indefinitely or timing out immediately with a zero duration.
const defaultProxyTimeout = 30 * time.Second

// defaultMaxBodySize is the default maximum request body size (10MB).
// This protects against DoS attacks via large request bodies.
const defaultMaxBodySize = 10 * 1024 * 1024 // 10MB

const (
	headerXForwardedFor   = "X-Forwarded-For"
	headerXForwardedHost  = "X-Forwarded-Host"
	headerXForwardedProto = "X-Forwarded-Proto"
	headerAuthorization   = "Authorization"
)

// newTransport creates an HTTP transport optimized for Cloud Run environments.
// Uses higher connection pooling limits than Go defaults for better proxy performance.
func newTransport() *http.Transport {
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Transport{
		Proxy:       http.ProxyFromEnvironment,
		DialContext: dialer.DialContext,
		// Increase from default 2 to 20 for better connection reuse to backends
		MaxIdleConnsPerHost: 20,
		// Keep default 100 total idle connections
		MaxIdleConns: 100,
		// Close idle connections after 90s (Go default)
		IdleConnTimeout: 90 * time.Second,
		// Allow up to 60s for backend to send response headers (handles slow backends)
		ResponseHeaderTimeout: 60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
	}
}

type Proxy struct {
	cfg           *config.Config
	logger        *slog.Logger
	tokenProvider TokenProvider
}

func NewProxy(cfg *config.Config, logger *slog.Logger) *Proxy {
	return &Proxy{
		cfg:           cfg,
		logger:        logger,
		tokenProvider: NewDefaultTokenProvider(),
	}
}

func (p *Proxy) CreateHandler(route config.Route) (http.HandlerFunc, error) {
	targetBase, err := url.Parse(route.Target.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse target URL %s: %w", route.Target.URL, err)
	}
	reverseProxy := httputil.NewSingleHostReverseProxy(targetBase)
	reverseProxy.Transport = newTransport()

	reverseProxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		p.logger.Error("proxy error", "error", err, "host", r.Host, "url", r.URL)
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
	}

	// Define the Allowlist for this specific route
	allowlist := p.createHeaderAllowlist(route)

	// Setup the Director once to avoid data races
	originalDirector := reverseProxy.Director
	reverseProxy.Director = func(req *http.Request) {
		// Remove any client-supplied forwarded headers to prevent spoofing.
		// The reverse proxy will set correct values based on the incoming connection.
		req.Header.Del(headerXForwardedFor)
		req.Header.Del(headerXForwardedHost)
		req.Header.Del(headerXForwardedProto)

		originalDirector(req)

		// Remap the Path
		newPath := p.resolveTargetPath(route.Target.Path, req)
		if newPath == "" {
			newPath = req.URL.Path
		}
		req.URL.Path = path.Clean(newPath)

		// Fix Host for Cloud Run
		req.URL.Host = targetBase.Host
		req.URL.Scheme = targetBase.Scheme
		req.Host = targetBase.Host

		// Header Filtering (Security)
		p.filterHeaders(req, allowlist)
	}

	// Get tracer for span creation
	tracer := otel.Tracer("bifrost.proxy")

	handler := func(w http.ResponseWriter, r *http.Request) {
		// Apply request body size limit (use default if not configured)
		maxBodySize := p.cfg.Server.MaxBodySize
		if maxBodySize == 0 {
			maxBodySize = defaultMaxBodySize
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)

		// Start a span for the proxy operation
		ctx, span := tracer.Start(r.Context(), "proxy_request",
			trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(
				attribute.String("route.id", route.ID),
				attribute.String("route.path", route.Path),
				attribute.String("target.url", route.Target.URL),
				attribute.Bool("target.gcp_auth", route.Target.GCPAuth),
			),
		)
		defer span.End()

		r = r.WithContext(ctx)

		// Timeout selection priority: route-specific > server-level > default (30s)
		timeout := p.cfg.Server.Timeout
		if route.Target.Timeout > 0 {
			timeout = route.Target.Timeout
		}
		// Use default timeout if none is configured to prevent hanging or immediate timeout
		if timeout <= 0 {
			timeout = defaultProxyTimeout
		}

		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		r = r.WithContext(ctx)

		// Add timeout to span
		span.SetAttributes(attribute.Int64("timeout.ms", timeout.Milliseconds()))

		// Audit Logging (JWT Claims)
		p.logClaims(r, route.Policies.LogClaims)

		// GCP OIDC Injection (if needed, inject before proxying)
		if route.Target.GCPAuth {
			span.AddEvent("injecting_gcp_auth")

			if err := p.injectGCPAuth(r, route.Target.URL); err != nil {
				p.logger.Error("Failed to inject GCP auth token", "error", err)
				span.RecordError(err)
				span.SetAttributes(attribute.Bool("auth.failed", true))
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			span.SetAttributes(attribute.Bool("auth.success", true))
		}

		reverseProxy.ServeHTTP(w, r)
	}
	return handler, nil
}

// resolveTargetPath replaces {key} in the target template with values from the request
func (p *Proxy) resolveTargetPath(template string, r *http.Request) string {
	return placeholderRegexp.ReplaceAllStringFunc(template, func(match string) string {
		key := strings.Trim(match, "{}")
		key = strings.TrimSuffix(key, "...")
		return r.PathValue(key)
	})
}

// filterHeaders strips any header not present in the allowlist
func (p *Proxy) filterHeaders(req *http.Request, allowlist map[string]bool) {
	for h := range req.Header {
		if !allowlist[http.CanonicalHeaderKey(h)] {
			req.Header.Del(h)
		}
	}
}

// createHeaderAllowlist combines global and route-specific allowed headers into a set for fast lookup.
// Header names are normalized to canonical form for case-insensitive matching.
func (p *Proxy) createHeaderAllowlist(route config.Route) map[string]bool {
	m := make(map[string]bool)

	// Always allow reverse proxy generated forwarded headers.
	// We explicitly strip any client-supplied values before the proxy sets them.
	m[headerXForwardedFor] = true
	m[headerXForwardedHost] = true
	m[headerXForwardedProto] = true

	// Add globals
	for _, h := range p.cfg.Server.AllowedHeaders {
		m[http.CanonicalHeaderKey(h)] = true
	}
	// Add route-specific
	for _, h := range route.AllowedHeaders {
		m[http.CanonicalHeaderKey(h)] = true
	}

	// Ensure Authorization survives filtering for GCP-authenticated routes.
	// This is needed for the injected OIDC token.
	if route.Target.GCPAuth {
		m[headerAuthorization] = true
	}
	return m
}
