package auth

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
)

// Middleware guards all routes, allowing through only:
//   - paths listed in Config.UnprotectedPaths (prefix match)
//   - requests bearing a valid session cookie
//   - requests with X-Remote-User header from a trusted proxy CIDR
type Middleware struct {
	cfg          *Config
	store        *SessionStore
	trustedCIDRs []*net.IPNet
}

// NewMiddleware parses TrustedProxies CIDRs from cfg and returns a Middleware.
// Invalid CIDR strings are silently skipped (they were validated at startup).
func NewMiddleware(cfg *Config, store *SessionStore) *Middleware {
	m := &Middleware{cfg: cfg, store: store}
	for _, cidr := range cfg.TrustedProxies {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err == nil {
			m.trustedCIDRs = append(m.trustedCIDRs, ipNet)
		}
	}
	return m
}

// Wrap returns an http.Handler that enforces authentication before delegating
// to next.
func (m *Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Unprotected paths — let through immediately.
		for _, p := range m.cfg.UnprotectedPaths {
			if r.URL.Path == p || strings.HasPrefix(r.URL.Path, p+"/") {
				next.ServeHTTP(w, r)
				return
			}
		}

		// 2. The login page, auth endpoints, and static images are always accessible.
		if r.URL.Path == "/login" ||
			strings.HasPrefix(r.URL.Path, "/auth/") ||
			strings.HasPrefix(r.URL.Path, "/images/") {
			next.ServeHTTP(w, r)
			return
		}

		// 3. Trusted proxy delegation via X-Remote-User.
		if user := r.Header.Get("X-Remote-User"); user != "" && m.isTrustedProxy(r) {
			next.ServeHTTP(w, r)
			return
		}

		// 4. Session cookie check.
		if c, err := r.Cookie("dumpstore_session"); err == nil && m.store.Valid(c.Value) {
			next.ServeHTTP(w, r)
			return
		}

		// 5. Not authenticated — API clients get 401 JSON; browsers get a redirect.
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.Contains(r.Header.Get("Accept"), "application/json") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}
		http.Redirect(w, r, "/login", http.StatusFound)
	})
}

// isTrustedProxy reports whether the request's remote IP is in a configured
// trusted proxy CIDR.
func (m *Middleware) isTrustedProxy(r *http.Request) bool {
	if len(m.trustedCIDRs) == 0 {
		return false
	}
	ipStr := extractIP(r.RemoteAddr)
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	for _, cidr := range m.trustedCIDRs {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}
