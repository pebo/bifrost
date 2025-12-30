package engine

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
)

// logClaims extracts specific fields from an unverified JWT for observability
func (p *Proxy) logClaims(r *http.Request, claimsToLog []string) {
	if len(claimsToLog) == 0 {
		return
	}

	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return
	}

	parts := strings.Split(strings.TrimPrefix(authHeader, "Bearer "), ".")
	if len(parts) != 3 {
		return // Not a valid JWT structure
	}

	// The payload is the second segment (index 1)
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return
	}

	// Use a standard JSON library for parsing into a map
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return
	}

	// Log only the requested fields to maintain privacy/security.
	// Note: Claims are extracted from an *unverified* JWT payload.
	for _, key := range claimsToLog {
		if val, exists := claims[key]; exists {
			p.logger.Info("jwt_claim_unverified",
				"route", r.URL.Path,
				"claim", key,
				"value", val,
			)
		}
	}
}
