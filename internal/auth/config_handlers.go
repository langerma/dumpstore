package auth

import (
	"encoding/json"
	"net/http"
)

// handleAuthConfig returns non-sensitive config: username and session TTL.
func handleAuthConfig(cfg *Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(AuthConfigResponse{
			Username:   cfg.Username,
			SessionTTL: cfg.SessionTTL.String(),
		})
	}
}
