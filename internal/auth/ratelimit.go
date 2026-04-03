package auth

import (
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	loginRateLimit  = 10             // max attempts per window
	loginRateWindow = 60 * time.Second
)

type ipBucket struct {
	count int
	reset time.Time
}

// RateLimiter enforces a per-IP limit of loginRateLimit attempts per
// loginRateWindow for the login endpoint.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*ipBucket
}

// NewRateLimiter creates a new RateLimiter.
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{buckets: make(map[string]*ipBucket)}
}

// Allow reports whether the request IP is within the rate limit.
// Returns false (deny) when the limit has been exceeded.
func (rl *RateLimiter) Allow(r *http.Request) bool {
	ip := extractIP(r.RemoteAddr)
	now := time.Now()

	rl.mu.Lock()
	defer rl.mu.Unlock()

	b, ok := rl.buckets[ip]
	if !ok || now.After(b.reset) {
		rl.buckets[ip] = &ipBucket{count: 1, reset: now.Add(loginRateWindow)}
		return true
	}
	b.count++
	return b.count <= loginRateLimit
}

// extractIP returns just the host portion of a host:port address.
func extractIP(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}
