package telemetry

import (
	"log/slog"
	"net/http"

	"github.com/pebo/bifrost/internal/engine"
)

// authTransport wraps an HTTP transport to inject GCP OIDC tokens into requests.
type authTransport struct {
	base          http.RoundTripper
	endpoint      string
	tokenProvider engine.TokenProvider
	logger        *slog.Logger
}

// RoundTrip implements http.RoundTripper by injecting an authorization header.
func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone the request to avoid modifying the original
	req = req.Clone(req.Context())

	// Get OIDC token source for the endpoint
	tokenSource, err := t.tokenProvider.GetTokenSource(req.Context(), t.endpoint)
	if err != nil {
		t.logger.Error("failed to get OIDC token source for telemetry exporter", "endpoint", t.endpoint, "error", err)
		return nil, err
	}

	// Get the actual token
	token, err := tokenSource.Token()
	if err != nil {
		t.logger.Error("failed to get OIDC token for telemetry exporter", "endpoint", t.endpoint, "error", err)
		return nil, err
	}

	// Inject authorization header
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)

	// Forward the request
	return t.base.RoundTrip(req)
}

// newAuthenticatedHTTPClient creates an HTTP client that injects GCP OIDC tokens.
func newAuthenticatedHTTPClient(endpoint string, tokenProvider engine.TokenProvider, logger *slog.Logger) *http.Client {
	return &http.Client{
		Transport: &authTransport{
			base:          http.DefaultTransport,
			endpoint:      endpoint,
			tokenProvider: tokenProvider,
			logger:        logger,
		},
	}
}
