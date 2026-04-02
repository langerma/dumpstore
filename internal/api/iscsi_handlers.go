package api

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"dumpstore/internal/ansible"
	"dumpstore/internal/iscsi"
)

// validPortalIP returns true if s is a parseable IPv4 or IPv6 address.
func validPortalIP(s string) bool { return net.ParseIP(s) != nil }

// validPort returns true if s is a decimal port number in 1–65535.
func validPort(s string) bool {
	n, err := strconv.Atoi(s)
	return err == nil && n >= 1 && n <= 65535
}

// validIQN returns true if s is a valid iSCSI Qualified Name.
func validIQN(s string) bool { return reIQN.MatchString(s) }

// getISCSITargets handles GET /api/iscsi-targets
// Returns all active iSCSI targets from the platform backend (targetcli or ctld).
func (h *Handler) getISCSITargets(w http.ResponseWriter, r *http.Request) {
	targets, err := iscsi.ListTargets()
	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
		return
	}
	writeJSON(r.Context(), w, targets)
}

// createISCSITarget handles POST /api/iscsi-targets
// Body: {zvol, iqn, portal_ip, portal_port, auth_mode, chap_user, chap_password, initiators}
func (h *Handler) createISCSITarget(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Zvol         string   `json:"zvol"`
		IQN          string   `json:"iqn"`
		PortalIP     string   `json:"portal_ip"`
		PortalPort   string   `json:"portal_port"`
		AuthMode     string   `json:"auth_mode"`
		CHAPUser     string   `json:"chap_user"`
		CHAPPassword string   `json:"chap_password"`
		Initiators   []string `json:"initiators"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}

	if !validZFSName(req.Zvol) || !strings.Contains(req.Zvol, "/") {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid zvol name"), nil)
		return
	}
	if !validIQN(req.IQN) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid IQN format"), nil)
		return
	}
	if req.PortalIP == "" {
		req.PortalIP = "0.0.0.0"
	}
	if !validPortalIP(req.PortalIP) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid portal IP address"), nil)
		return
	}
	if req.PortalPort == "" {
		req.PortalPort = "3260"
	}
	if !validPort(req.PortalPort) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid portal port"), nil)
		return
	}
	if req.AuthMode != "none" && req.AuthMode != "chap" {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("auth_mode must be 'none' or 'chap'"), nil)
		return
	}
	if req.AuthMode == "chap" {
		if req.CHAPUser == "" || req.CHAPPassword == "" {
			writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("chap_user and chap_password required when auth_mode is 'chap'"), nil)
			return
		}
		if !safePropertyValue(req.CHAPUser) {
			writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid characters in CHAP username"), nil)
			return
		}
		if !safePassword(req.CHAPPassword) {
			writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("CHAP password must not contain newline characters"), nil)
			return
		}
	}
	for _, ini := range req.Initiators {
		if !validIQN(ini) {
			writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid initiator IQN: %s", ini), nil)
			return
		}
	}

	backend := iscsi.Backend()
	if backend == "" {
		writeError(r.Context(), w, http.StatusNotImplemented,
			fmt.Errorf("no iSCSI backend detected (targetcli or ctld required)"), nil)
		return
	}
	playbook := "iscsi_target_create.yml"
	if backend == "freebsd" {
		playbook = "iscsi_target_create_freebsd.yml"
	}

	out, err := h.runOp(playbook, map[string]string{
		"zvol":          req.Zvol,
		"iqn":           req.IQN,
		"portal_ip":     req.PortalIP,
		"portal_port":   req.PortalPort,
		"auth_mode":     req.AuthMode,
		"chap_user":     req.CHAPUser,
		"chap_password": req.CHAPPassword,
		"initiators":    strings.Join(req.Initiators, ","),
	})
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(r.Context(), w, http.StatusInternalServerError, err, steps)
		return
	}
	writeJSON(r.Context(), w, map[string]any{"zvol": req.Zvol, "iqn": req.IQN, "tasks": out.Steps()})
}

// deleteISCSITarget handles DELETE /api/iscsi-targets?iqn=...&zvol=...
func (h *Handler) deleteISCSITarget(w http.ResponseWriter, r *http.Request) {
	iqn := r.URL.Query().Get("iqn")
	zvol := r.URL.Query().Get("zvol")

	if !validIQN(iqn) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid IQN"), nil)
		return
	}
	if !validZFSName(zvol) || !strings.Contains(zvol, "/") {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid zvol name"), nil)
		return
	}

	backend := iscsi.Backend()
	if backend == "" {
		writeError(r.Context(), w, http.StatusNotImplemented,
			fmt.Errorf("no iSCSI backend detected (targetcli or ctld required)"), nil)
		return
	}
	playbook := "iscsi_target_delete.yml"
	if backend == "freebsd" {
		playbook = "iscsi_target_delete_freebsd.yml"
	}

	out, err := h.runOp(playbook, map[string]string{"iqn": iqn, "zvol": zvol})
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(r.Context(), w, http.StatusInternalServerError, err, steps)
		return
	}
	writeJSON(r.Context(), w, map[string]any{"iqn": iqn, "zvol": zvol, "tasks": out.Steps()})
}
