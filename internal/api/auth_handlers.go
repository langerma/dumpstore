package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"

	"dumpstore/internal/auth"
)

// reUsername allows letters, digits, underscores, hyphens, and dots — no
// shell-special characters.
var reUsername = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9._-]{0,62}$`)

func (h *Handler) changePassword(ctx context.Context, in struct {
	Body ChangePasswordRequest `body:""`
}) (TaskResult, error) {
	req := in.Body
	h.authMu.RLock()
	pwHash := h.authCfg.PasswordHash
	h.authMu.RUnlock()

	if pwHash == "" || auth.VerifyPassword(pwHash, []byte(req.CurrentPassword)) != nil {
		return TaskResult{}, errStatus(http.StatusUnauthorized, errors.New("current password is incorrect"))
	}
	if len(req.NewPassword) == 0 {
		return TaskResult{}, errStatus(http.StatusBadRequest, errors.New("new password must not be empty"))
	}
	hash, err := auth.HashPassword([]byte(req.NewPassword))
	if err != nil {
		return TaskResult{}, errors.New("failed to hash password")
	}
	out, err := h.runOp(ctx, "auth_set_password.yml", map[string]string{
		"config_path":   h.configPath,
		"password_hash": hash,
	})
	if err != nil {
		return TaskResult{}, playbookError(err, out)
	}
	// Update in-memory config so subsequent logins use the new hash immediately.
	h.authMu.Lock()
	h.authCfg.PasswordHash = hash
	username := h.authCfg.Username
	h.authMu.Unlock()

	auditLog(ctx, "auth.change_password", username, nil)
	return TaskResult{Tasks: out.Steps()}, nil
}

// ChangePasswordRequest is the POST /api/auth/change-password body.
type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (h *Handler) changeUsername(ctx context.Context, in struct {
	Body ChangeUsernameRequest `body:""`
}) (TaskResult, error) {
	req := in.Body
	if !reUsername.MatchString(req.Username) {
		return TaskResult{}, errStatus(http.StatusBadRequest, errors.New("invalid username: use letters, digits, underscores, hyphens, dots; must start with a letter"))
	}
	out, err := h.runOp(ctx, "auth_set_username.yml", map[string]string{
		"config_path": h.configPath,
		"username":    req.Username,
	})
	if err != nil {
		return TaskResult{}, playbookError(err, out)
	}
	auditLog(ctx, "auth.change_username", req.Username, nil)

	h.authMu.Lock()
	h.authCfg.Username = req.Username
	h.authMu.Unlock()

	// Invalidate all sessions — the username changed so everyone must re-login.
	h.authStore.DeleteAll()
	return TaskResult{Tasks: out.Steps()}, nil
}

// ChangeUsernameRequest is the POST /api/auth/change-username body.
type ChangeUsernameRequest struct {
	Username string `json:"username"`
}
