package engine

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"golang.org/x/oauth2"
	"google.golang.org/api/idtoken"
)

// TokenProvider defines the interface for obtaining OIDC token sources
type TokenProvider interface {
	GetTokenSource(ctx context.Context, audience string) (oauth2.TokenSource, error)
}

// defaultTokenProvider manages the fetching of OIDC tokens
type defaultTokenProvider struct {
	mu      sync.RWMutex
	sources map[string]oauth2.TokenSource
}

func NewDefaultTokenProvider() TokenProvider {
	return &defaultTokenProvider{
		sources: make(map[string]oauth2.TokenSource),
	}
}

// injectGCPAuth adds a Google-signed OIDC ID token to the request
func (p *Proxy) injectGCPAuth(req *http.Request, targetURL string) error {
	// The 'audience' for the token must be the base URL of the target service
	ts, err := p.tokenProvider.GetTokenSource(req.Context(), targetURL)
	if err != nil {
		return fmt.Errorf("failed to get token source for audience %s: %w", targetURL, err)
	}

	token, err := ts.Token()
	if err != nil {
		return fmt.Errorf("failed to generate OIDC token for audience %s: %w", targetURL, err)
	}

	// Inject the token into the Authorization header
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.AccessToken))
	return nil
}

func (tp *defaultTokenProvider) GetTokenSource(ctx context.Context, audience string) (oauth2.TokenSource, error) {
	// Attempt to retrieve with a read lock for high concurrency
	tp.mu.RLock()
	ts, ok := tp.sources[audience]
	tp.mu.RUnlock()
	if ok {
		return ts, nil
	}

	// If not found, acquire a write lock to create it
	tp.mu.Lock()
	defer tp.mu.Unlock()

	// Double-check in case another goroutine created it while we waited for the lock
	if ts, ok := tp.sources[audience]; ok {
		return ts, nil
	}

	// Create and store the new token source
	ts, err := idtoken.NewTokenSource(ctx, audience)
	if err != nil {
		return nil, err
	}

	tp.sources[audience] = ts
	return ts, nil
}
