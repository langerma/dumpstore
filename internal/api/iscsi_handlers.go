package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	"dumpstore/internal/ansible"
	"dumpstore/internal/iscsi"
	"dumpstore/internal/operations"
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

// validCHAPSecret returns true if s can be written as a ctl.conf quoted
// string, which has no escape sequences: no quote, backslash, or newline.
func validCHAPSecret(s string) bool { return !strings.ContainsAny(s, "\"\\\n\r") }

// requireCtldInit writes a 409 error if /etc/ctl.conf is not yet owned by
// dumpstore. Returns true if the caller should abort.
func (h *Handler) requireCtldInit(w http.ResponseWriter, r *http.Request) bool {
	if !iscsi.CtldInitialized() {
		writeError(r.Context(), w, http.StatusConflict,
			fmt.Errorf("ctld not initialised — run POST /api/iscsi/init first"), nil)
		return true
	}
	return false
}

// applyCtlConf renders targets and runs iscsi_ctld_apply.yml to validate,
// deploy, reload, and verify it: every target must show up in ctld's port
// list, every target the current file defines but targets drops must vanish
// from it, and ctld must hold a listening socket for every portal. If that fails, the playbook restores the current file, so
// a bad config never survives to the next ctld restart. init adds the
// one-time backup. Caller holds iscsiMu.
func (h *Handler) applyCtlConf(ctx context.Context, targets []iscsi.Target, init bool) (*ansible.PlaybookOutput, error) {
	conf, err := iscsi.Render(targets)
	if err != nil {
		return nil, err
	}
	src, err := writeTemp("dumpstore-ctl-*.conf", conf)
	if err != nil {
		return nil, err
	}
	defer os.Remove(src)

	vars := map[string]string{
		"src":      src,
		"ctl_conf": iscsi.CtlConfPath,
		"init":     strconv.FormatBool(init),
	}
	var prev []string
	switch cur, err := os.ReadFile(iscsi.CtlConfPath); {
	case err == nil:
		rollback, err := writeTemp("dumpstore-ctl-prev-*.conf", cur)
		if err != nil {
			return nil, err
		}
		defer os.Remove(rollback)
		vars["rollback_src"] = rollback
		prev = iscsi.TargetNames(string(cur))
	case !os.IsNotExist(err):
		return nil, fmt.Errorf("reading ctl.conf: %w", err)
	}

	expect := make([]string, len(targets))
	kept := map[string]bool{}
	for i, t := range targets {
		expect[i] = t.IQN
		kept[strings.ToLower(t.IQN)] = true
	}
	absent := []string{}
	for _, iqn := range prev {
		if !kept[strings.ToLower(iqn)] {
			absent = append(absent, iqn)
		}
	}
	expectJSON, _ := json.Marshal(expect)
	absentJSON, _ := json.Marshal(absent)
	listenersJSON, _ := json.Marshal(iscsi.Listeners(targets))
	vars["expect_iqns"] = string(expectJSON)
	vars["absent_iqns"] = string(absentJSON)
	vars["expect_listeners"] = string(listenersJSON)
	return h.runOp(ctx, "iscsi_ctld_apply.yml", vars)
}

// writeTemp writes data to a new 0600 temp file and returns its path.
func writeTemp(pattern string, data []byte) (string, error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("write temp file: %w", err)
	}
	return f.Name(), nil
}

// getISCSIStatus handles GET /api/iscsi/status
// Returns {backend, initialized, conf_path, conf_exists}. Only ctld (FreeBSD)
// has an init gate; targetcli (Linux) always reports initialized.
func (h *Handler) getISCSIStatus(w http.ResponseWriter, r *http.Request) {
	backend := iscsi.Backend()
	resp := map[string]any{
		"backend":     backend,
		"initialized": backend == "linux",
		"conf_path":   "",
		"conf_exists": false,
	}
	if backend == "freebsd" {
		_, err := os.Stat(iscsi.CtlConfPath)
		resp["initialized"] = iscsi.CtldInitialized()
		resp["conf_path"] = iscsi.CtlConfPath
		resp["conf_exists"] = err == nil
	}
	writeJSON(r.Context(), w, resp)
}

// initISCSI handles POST /api/iscsi/init (FreeBSD/ctld only).
// Takes ownership of /etc/ctl.conf: backs up an existing file once to
// ctl.conf.pre-dumpstore, imports the targets dumpstore's model represents
// (reporting everything else as skipped steps), and renders the file from
// template. Safe to call multiple times.
func (h *Handler) initISCSI(w http.ResponseWriter, r *http.Request) {
	switch iscsi.Backend() {
	case "freebsd":
	case "linux":
		writeError(r.Context(), w, http.StatusBadRequest,
			fmt.Errorf("init applies to ctld (FreeBSD) only — targetcli needs no initialisation"), nil)
		return
	default:
		writeError(r.Context(), w, http.StatusNotImplemented,
			fmt.Errorf("ctld not installed"), nil)
		return
	}

	h.iscsiMu.Lock()
	defer h.iscsiMu.Unlock()

	raw, err := iscsi.ReadCtlConfRaw()
	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
		return
	}
	targets, skipped := iscsi.Import(raw)

	id := operations.IDFromContext(r.Context())
	var steps []ansible.TaskStep
	for _, s := range skipped {
		step := ansible.TaskStep{Name: "Not imported: " + s, Status: "skipped", Msg: "kept in " + iscsi.CtlConfBackupPath}
		h.operations.Step(id, step)
		steps = append(steps, step)
	}

	out, err := h.applyCtlConf(r.Context(), targets, true)
	auditLog(r.Context(), r, "iscsi.init", "", err)
	if out != nil {
		steps = append(steps, out.Steps()...)
	}
	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, err, steps)
		return
	}
	writeJSON(r.Context(), w, map[string]any{
		"initialized": iscsi.CtldInitialized(),
		"imported":    len(targets),
		"skipped":     len(skipped),
		"tasks":       steps,
	})
}

// getISCSITargets handles GET /api/iscsi-targets
// Returns all active iSCSI targets from the platform backend (targetcli or ctld).
func (h *Handler) getISCSITargets(context.Context, struct{}) ([]iscsi.Target, error) {
	return iscsi.ListTargets()
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
		if !validCHAPSecret(req.CHAPPassword) {
			writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("CHAP password must not contain quotes, backslashes, or newlines"), nil)
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

	h.refMu.Lock() // the zvol must not be destroyed or renamed while the target is added (#183)
	defer h.refMu.Unlock()

	if backend == "freebsd" {
		h.createCtldTarget(w, r, iscsi.Target{
			IQN:        req.IQN,
			ZvolName:   req.Zvol,
			Portals:    []string{net.JoinHostPort(req.PortalIP, req.PortalPort)},
			AuthMode:   req.AuthMode,
			Initiators: req.Initiators,
			CHAPUser:   req.CHAPUser,
			CHAPSecret: req.CHAPPassword,
		})
		return
	}

	out, err := h.runOp(r.Context(), "iscsi_target_create.yml", map[string]string{
		"zvol":          req.Zvol,
		"iqn":           req.IQN,
		"portal_ip":     req.PortalIP,
		"portal_port":   req.PortalPort,
		"auth_mode":     req.AuthMode,
		"chap_user":     req.CHAPUser,
		"chap_password": req.CHAPPassword,
		"initiators":    strings.Join(req.Initiators, ","),
	})
	auditLog(r.Context(), r, "iscsi.create", req.Zvol, err)
	if err != nil {
		writeRunOpError(r.Context(), w, err, out)
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
	if backend == "freebsd" {
		h.deleteCtldTarget(w, r, iqn, zvol)
		return
	}

	out, err := h.runOp(r.Context(), "iscsi_target_delete.yml", map[string]string{"iqn": iqn, "zvol": zvol})
	auditLog(r.Context(), r, "iscsi.delete", zvol, err)
	if err != nil {
		writeRunOpError(r.Context(), w, err, out)
		return
	}
	writeJSON(r.Context(), w, map[string]any{"iqn": iqn, "zvol": zvol, "tasks": out.Steps()})
}

// createCtldTarget adds t to the dumpstore-owned ctl.conf and applies it.
// Caller holds refMu.
func (h *Handler) createCtldTarget(w http.ResponseWriter, r *http.Request, t iscsi.Target) {
	h.iscsiMu.Lock()
	defer h.iscsiMu.Unlock()
	if h.requireCtldInit(w, r) {
		return
	}
	targets, err := iscsi.LoadCtld()
	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
		return
	}
	for _, e := range targets {
		if strings.EqualFold(e.IQN, t.IQN) {
			writeError(r.Context(), w, http.StatusConflict, fmt.Errorf("iSCSI target %s already exists", t.IQN), nil)
			return
		}
		if e.ZvolName == t.ZvolName {
			writeError(r.Context(), w, http.StatusConflict,
				fmt.Errorf("zvol %s is already exported as %s", t.ZvolName, e.IQN), nil)
			return
		}
	}
	if t.Initiators == nil {
		t.Initiators = []string{}
	}
	// ctld only logs a warning for a missing LUN path and exports nothing.
	if _, err := os.Stat("/dev/zvol/" + t.ZvolName); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest,
			fmt.Errorf("zvol device /dev/zvol/%s not found", t.ZvolName), nil)
		return
	}

	out, err := h.applyCtlConf(r.Context(), append(targets, t), false)
	auditLog(r.Context(), r, "iscsi.create", t.ZvolName, err)
	if err != nil {
		writeRunOpError(r.Context(), w, err, out)
		return
	}
	writeJSON(r.Context(), w, map[string]any{"zvol": t.ZvolName, "iqn": t.IQN, "tasks": out.Steps()})
}

// deleteCtldTarget removes the target iqn (exporting zvol) from the
// dumpstore-owned ctl.conf and applies it.
func (h *Handler) deleteCtldTarget(w http.ResponseWriter, r *http.Request, iqn, zvol string) {
	h.iscsiMu.Lock()
	defer h.iscsiMu.Unlock()
	if h.requireCtldInit(w, r) {
		return
	}
	targets, err := iscsi.LoadCtld()
	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
		return
	}
	kept := make([]iscsi.Target, 0, len(targets))
	for _, t := range targets {
		if !strings.EqualFold(t.IQN, iqn) || t.ZvolName != zvol {
			kept = append(kept, t)
		}
	}
	if len(kept) == len(targets) {
		writeError(r.Context(), w, http.StatusNotFound,
			fmt.Errorf("no iSCSI target %s for zvol %s", iqn, zvol), nil)
		return
	}

	out, err := h.applyCtlConf(r.Context(), kept, false)
	auditLog(r.Context(), r, "iscsi.delete", zvol, err)
	if err != nil {
		writeRunOpError(r.Context(), w, err, out)
		return
	}
	writeJSON(r.Context(), w, map[string]any{"iqn": iqn, "zvol": zvol, "tasks": out.Steps()})
}
