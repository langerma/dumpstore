package auth

import (
	"encoding/json"
	"net/http"

	"golang.org/x/crypto/bcrypt"
)

const sessionCookieName = "dumpstore_session"

// RegisterRoutes registers the login/logout and auth config endpoints on mux.
func RegisterRoutes(mux *http.ServeMux, cfg *Config, store *SessionStore, rl *RateLimiter) {
	mux.HandleFunc("GET /login", handleLoginPage(cfg, store))
	mux.HandleFunc("POST /auth/login", handleLogin(cfg, store, rl))
	mux.HandleFunc("POST /auth/logout", handleLogout(store))
	mux.HandleFunc("GET /api/whoami", handleWhoami(cfg, store))
	mux.HandleFunc("GET /api/auth/config", handleAuthConfig(cfg))
}

// handleLoginPage serves a self-contained HTML login form.
// Authenticated users are redirected straight to the app.
func handleLoginPage(cfg *Config, store *SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(sessionCookieName); err == nil && store.Valid(c.Value) {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		errMsg := r.URL.Query().Get("error")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(loginPage(cfg.Username, errMsg)))
	}
}

// handleLogin validates credentials, sets the session cookie, and redirects.
func handleLogin(cfg *Config, store *SessionStore, rl *RateLimiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !rl.Allow(r) {
			http.Redirect(w, r, "/login?error=Too+many+attempts.+Try+again+later.", http.StatusFound)
			return
		}

		username := r.FormValue("username")
		password := r.FormValue("password")

		valid := username == cfg.Username &&
			cfg.PasswordHash != "" &&
			bcrypt.CompareHashAndPassword([]byte(cfg.PasswordHash), []byte(password)) == nil

		if !valid {
			http.Redirect(w, r, "/login?error=Invalid+username+or+password.", http.StatusFound)
			return
		}

		token := store.Create()
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
			MaxAge:   int(cfg.SessionTTL.Seconds()),
			Secure:   r.TLS != nil,
		})
		http.Redirect(w, r, "/", http.StatusFound)
	}
}

// handleLogout invalidates the session and clears the cookie.
func handleLogout(store *SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(sessionCookieName); err == nil {
			store.Delete(c.Value)
		}
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
			MaxAge:   -1,
		})
		http.Redirect(w, r, "/login", http.StatusFound)
	}
}

// handleWhoami returns the authenticated username (used by the frontend to
// populate the header badge).
func handleWhoami(cfg *Config, store *SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Determine identity: trusted proxy header or session cookie.
		user := ""
		if ru := r.Header.Get("X-Remote-User"); ru != "" {
			user = ru
		} else if c, err := r.Cookie(sessionCookieName); err == nil && store.Valid(c.Value) {
			user = cfg.Username
		}
		if user == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"user": user})
	}
}

// loginPage returns a self-contained HTML login page.
func loginPage(username, errMsg string) string {
	errHTML := ""
	if errMsg != "" {
		errHTML = `<p class="login-error">` + htmlEsc(errMsg) + `</p>`
	}
	return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>dumpstore — login</title>
<style>
*,*::before,*::after{box-sizing:border-box;margin:0;padding:0}
:root{
  --bg:#0f1117;--surface:#1a1d27;--surface2:#22263a;--border:#2e3347;
  --accent:#4f9cf9;--text:#e2e8f0;--text-muted:#8892a4;--red:#f87171;
  --radius:8px;--font:'SF Mono','Fira Code','Cascadia Code',monospace;
}
body{background:var(--bg);color:var(--text);font-family:var(--font);
  display:flex;align-items:center;justify-content:center;min-height:100vh;}
.card{background:var(--surface);border:1px solid var(--border);
  border-radius:var(--radius);padding:2rem;width:100%;max-width:380px;}
.login-logo{height:32px;width:auto;display:block;margin:0 auto 1.75rem;}
label{display:block;font-size:12px;color:var(--text-muted);margin-bottom:1rem;}
label span{display:block;margin-bottom:4px;}
input{display:block;width:100%;background:var(--surface2);border:1px solid var(--border);
  border-radius:6px;color:var(--text);padding:7px 10px;font-family:var(--font);
  font-size:13px;outline:none;}
input:focus{border-color:var(--accent);}
button{display:block;width:100%;background:var(--accent);border:none;color:#000;
  padding:8px 14px;border-radius:var(--radius);cursor:pointer;font-family:var(--font);
  font-size:13px;font-weight:600;margin-top:1.25rem;transition:opacity .15s;}
button:hover{opacity:.85;}
.login-error{color:var(--red);font-size:12px;margin-bottom:1rem;
  background:rgba(248,113,113,.1);border:1px solid rgba(248,113,113,.3);
  border-radius:6px;padding:8px 10px;}
</style>
</head>
<body>
<div class="card">
  <img src="/images/dumpstore-blue-dark-lockup.svg" alt="dumpstore" class="login-logo">
  ` + errHTML + `
  <form method="POST" action="/auth/login">
    <label><span>Username</span>
      <input type="text" name="username" value="` + htmlEsc(username) + `" autocomplete="username" required>
    </label>
    <label><span>Password</span>
      <input type="password" name="password" autocomplete="current-password" required autofocus>
    </label>
    <button type="submit">Sign in</button>
  </form>
</div>
</body>
</html>`
}

// htmlEsc escapes the five HTML special characters for safe inline injection.
func htmlEsc(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '&':
			out = append(out, '&', 'a', 'm', 'p', ';')
		case '<':
			out = append(out, '&', 'l', 't', ';')
		case '>':
			out = append(out, '&', 'g', 't', ';')
		case '"':
			out = append(out, '&', 'q', 'u', 'o', 't', ';')
		case '\'':
			out = append(out, '&', '#', '3', '9', ';')
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}
