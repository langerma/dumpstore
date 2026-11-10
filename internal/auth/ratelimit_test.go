package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fakeClock returns a limiter whose clock the test advances.
func fakeClock() (*RateLimiter, *time.Time) {
	rl := NewRateLimiter()
	now := time.Unix(1_700_000_000, 0)
	rl.now = func() time.Time { return now }
	return rl, &now
}

func reqFrom(ip string) *http.Request {
	r := httptest.NewRequest("POST", "/auth/login", nil)
	r.RemoteAddr = ip + ":40000"
	return r
}

func TestRateLimiterFailuresBlock(t *testing.T) {
	rl, _ := fakeClock()
	r := reqFrom("192.0.2.1")
	for i := 1; i <= loginRateLimit; i++ {
		if _, ok := rl.Allow(r); !ok {
			t.Fatalf("attempt %d blocked, want allowed", i)
		}
	}
	retry, ok := rl.Allow(r)
	if ok || retry != loginRateWindow {
		t.Fatalf("attempt %d: ok=%v retry=%s, want blocked with retry %s", loginRateLimit+1, ok, retry, loginRateWindow)
	}
	if _, ok := rl.Allow(reqFrom("192.0.2.2")); !ok {
		t.Fatal("another IP is blocked")
	}
}

func TestRateLimiterSuccessDoesNotCount(t *testing.T) {
	rl, _ := fakeClock()
	r := reqFrom("192.0.2.1")
	for i := 1; i <= 3*loginRateLimit; i++ {
		if _, ok := rl.Allow(r); !ok {
			t.Fatalf("successful login %d blocked", i)
		}
		rl.Reset(r)
	}
	// Failures before a success are forgotten too.
	for range loginRateLimit - 1 {
		rl.Allow(r)
	}
	rl.Reset(r)
	for i := 1; i <= loginRateLimit; i++ {
		if _, ok := rl.Allow(r); !ok {
			t.Fatalf("failure %d after a success blocked", i)
		}
	}
}

func TestRateLimiterExpiryAndPruning(t *testing.T) {
	rl, now := fakeClock()
	r := reqFrom("192.0.2.1")
	for range loginRateLimit + 1 {
		rl.Allow(r)
	}
	rl.Allow(reqFrom("192.0.2.2"))

	*now = now.Add(loginRateWindow / 2)
	if retry, ok := rl.Allow(r); ok || retry != loginRateWindow/2 {
		t.Fatalf("mid-window: ok=%v retry=%s, want blocked with %s left", ok, retry, loginRateWindow/2)
	}

	*now = now.Add(loginRateWindow)
	if _, ok := rl.Allow(r); !ok {
		t.Fatal("still blocked after the window expired")
	}
	// The sweep on that call dropped the other IP's expired bucket.
	if _, found := rl.buckets["192.0.2.2"]; found || len(rl.buckets) != 1 {
		t.Fatalf("buckets after sweep: %v, want only 192.0.2.1", rl.buckets)
	}
}

func TestLoginRateLimitedResponse(t *testing.T) {
	cfg := &Config{Username: "admin"}
	rl, _ := fakeClock()
	h := handleLogin(cfg, NewSessionStore(time.Hour), rl)
	post := func(accept string) *httptest.ResponseRecorder {
		body := url.Values{"username": {"admin"}, "password": {"wrong"}}.Encode()
		r := httptest.NewRequest("POST", "/auth/login", strings.NewReader(body))
		r.RemoteAddr = "192.0.2.1:40000"
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if accept != "" {
			r.Header.Set("Accept", accept)
		}
		w := httptest.NewRecorder()
		h(w, r)
		return w
	}
	for i := 1; i <= loginRateLimit; i++ {
		if w := post(""); w.Code != http.StatusFound || !strings.Contains(w.Header().Get("Location"), "Invalid") {
			t.Fatalf("failure %d: %d %s, want redirect with Invalid…", i, w.Code, w.Header().Get("Location"))
		}
	}
	w := post("application/json")
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "60" ||
		!strings.Contains(w.Body.String(), `"error"`) {
		t.Fatalf("JSON client: %d Retry-After=%q body=%s, want 429, 60, error body", w.Code, w.Header().Get("Retry-After"), w.Body)
	}
	w = post("text/html,application/xhtml+xml")
	if w.Code != http.StatusFound || !strings.Contains(w.Header().Get("Location"), "Too+many+attempts") {
		t.Fatalf("browser: %d %s, want redirect with Too many attempts", w.Code, w.Header().Get("Location"))
	}
}
