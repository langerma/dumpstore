package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestIdentity: X-Remote-User is honoured only from trusted_proxies (#195).
// whoami and the auth middleware share Identity, so both are checked.
func TestIdentity(t *testing.T) {
	cfg := &Config{Username: "admin", TrustedProxies: []string{"10.0.0.0/8"}}
	store := NewSessionStore(time.Hour)
	m := NewMiddleware(cfg, store)
	session := store.Create()

	req := func(remote, header string, withSession bool) *http.Request {
		r := httptest.NewRequest("GET", "/api/v1/auth/whoami", nil)
		r.RemoteAddr = remote
		if header != "" {
			r.Header.Set("X-Remote-User", header)
		}
		if withSession {
			r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
		}
		return r
	}

	for _, c := range []struct {
		name        string
		remote      string
		header      string
		withSession bool
		wantUser    string
		wantOK      bool
	}{
		{"trusted proxy header", "10.1.2.3:5000", "alice", false, "alice", true},
		{"spoofed header from untrusted, no session", "192.0.2.9:5000", "mallory", false, "", false},
		{"spoofed header from untrusted, valid session", "192.0.2.9:5000", "mallory", true, "admin", true},
		{"session only", "192.0.2.9:5000", "", true, "admin", true},
		{"nothing", "192.0.2.9:5000", "", false, "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			user, ok := m.Identity(req(c.remote, c.header, c.withSession))
			if user != c.wantUser || ok != c.wantOK {
				t.Fatalf("Identity = %q, %v; want %q, %v", user, ok, c.wantUser, c.wantOK)
			}

			// whoami reports the same identity.
			rec := httptest.NewRecorder()
			handleWhoami(m).ServeHTTP(rec, req(c.remote, c.header, c.withSession))
			if c.wantOK {
				if rec.Code != 200 || rec.Body.String() != `{"user":"`+c.wantUser+`"}`+"\n" {
					t.Errorf("whoami: %d %q", rec.Code, rec.Body.String())
				}
			} else if rec.Code != http.StatusUnauthorized {
				t.Errorf("whoami: %d, want 401", rec.Code)
			}

			// The middleware lets exactly the authenticated requests through.
			passed := false
			m.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { passed = true })).
				ServeHTTP(httptest.NewRecorder(), func() *http.Request {
					r := req(c.remote, c.header, c.withSession)
					r.URL.Path = "/api/v1/pools"
					return r
				}())
			if passed != c.wantOK {
				t.Errorf("middleware passed = %v, want %v", passed, c.wantOK)
			}
		})
	}
}
