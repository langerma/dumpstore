package api

import (
	"errors"
	"net/http"
	"regexp"

	"golang.org/x/crypto/bcrypt"
)

// reUsername allows letters, digits, underscores, hyphens, and dots — no
// shell-special characters.
var reUsername = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9._-]{0,62}$`)

func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, errors.New("invalid request body"), nil)
		return
	}

	h.authMu.RLock()
	pwHash := h.authCfg.PasswordHash
	h.authMu.RUnlock()

	if pwHash == "" || bcrypt.CompareHashAndPassword([]byte(pwHash), []byte(req.CurrentPassword)) != nil {
		writeError(r.Context(), w, http.StatusUnauthorized, errors.New("current password is incorrect"), nil)
		return
	}
	if len(req.NewPassword) == 0 {
		writeError(r.Context(), w, http.StatusBadRequest, errors.New("new password must not be empty"), nil)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), 12)
	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, errors.New("failed to hash password"), nil)
		return
	}
	out, err := h.runOp("auth_set_password.yml", map[string]string{
		"config_path":   h.configPath,
		"password_hash": string(hash),
	})
	if err != nil {
		writeRunOpError(r.Context(), w, err, out)
		return
	}
	// Update in-memory config so subsequent logins use the new hash immediately.
	h.authMu.Lock()
	h.authCfg.PasswordHash = string(hash)
	username := h.authCfg.Username
	h.authMu.Unlock()

	auditLog(r.Context(), r, "auth.change_password", username, nil)
	writeJSON(r.Context(), w, map[string]any{"tasks": out.Steps()})
}

func (h *Handler) changeUsername(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, errors.New("invalid request body"), nil)
		return
	}
	if !reUsername.MatchString(req.Username) {
		writeError(r.Context(), w, http.StatusBadRequest, errors.New("invalid username: use letters, digits, underscores, hyphens, dots; must start with a letter"), nil)
		return
	}
	out, err := h.runOp("auth_set_username.yml", map[string]string{
		"config_path": h.configPath,
		"username":    req.Username,
	})
	if err != nil {
		writeRunOpError(r.Context(), w, err, out)
		return
	}
	auditLog(r.Context(), r, "auth.change_username", req.Username, nil)

	h.authMu.Lock()
	h.authCfg.Username = req.Username
	h.authMu.Unlock()

	// Invalidate all sessions — the username changed so everyone must re-login.
	h.authStore.DeleteAll()
	writeJSON(r.Context(), w, map[string]any{"tasks": out.Steps()})
}
