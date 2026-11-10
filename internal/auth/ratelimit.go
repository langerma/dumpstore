package auth

import (
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	loginRateLimit  = 10 // max failed attempts per window
	loginRateWindow = 60 * time.Second
)

type ipBucket struct {
	count int
	reset time.Time
}

// RateLimiter enforces a per-IP limit of loginRateLimit failed login
// attempts per loginRateWindow. Allow reserves an attempt before the
// password is checked, so parallel requests cannot slip past the limit;
// Reset clears the IP's bucket after a successful login, so only failures
// stay counted. Expired buckets are pruned at most once per window.
type RateLimiter struct {
	mu        sync.Mutex
	buckets   map[string]*ipBucket
	lastSweep time.Time
	now       func() time.Time // time source; tests replace it
}

// NewRateLimiter creates a new RateLimiter.
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{buckets: make(map[string]*ipBucket), now: time.Now}
}

// Allow reserves a login attempt for the request IP. It returns false and
// the time until the window resets when the IP has used up its attempts.
func (rl *RateLimiter) Allow(r *http.Request) (retryAfter time.Duration, ok bool) {
	ip := extractIP(r.RemoteAddr)

	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := rl.now()
	rl.sweep(now)

	b, found := rl.buckets[ip]
	if !found || !now.Before(b.reset) {
		rl.buckets[ip] = &ipBucket{count: 1, reset: now.Add(loginRateWindow)}
		return 0, true
	}
	if b.count >= loginRateLimit {
		return b.reset.Sub(now), false
	}
	b.count++
	return 0, true
}

// Reset forgets the request IP's attempts; called after a successful login.
func (rl *RateLimiter) Reset(r *http.Request) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.buckets, extractIP(r.RemoteAddr))
}

// sweep drops expired buckets, at most once per window. rl.mu must be held.
func (rl *RateLimiter) sweep(now time.Time) {
	if now.Sub(rl.lastSweep) < loginRateWindow {
		return
	}
	rl.lastSweep = now
	for ip, b := range rl.buckets {
		if !now.Before(b.reset) {
			delete(rl.buckets, ip)
		}
	}
}

// extractIP returns just the host portion of a host:port address.
func extractIP(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}
