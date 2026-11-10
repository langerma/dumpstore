package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"slices"
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

// requireCtldInit returns a 409 error if /etc/ctl.conf is not yet owned by
// dumpstore.
func requireCtldInit() error {
	if !iscsi.CtldInitialized() {
		return errStatus(http.StatusConflict,
			fmt.Errorf("ctld not initialised — run POST /api/v1/iscsi/init first"))
	}
	return nil
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

// ISCSIStatus is the iSCSI backend and its initialisation state.
type ISCSIStatus struct {
	Backend     string `json:"backend" doc:"linux (targetcli), freebsd (ctld), or empty when no backend is installed"`
	Initialized bool   `json:"initialized"`
	ConfPath    string `json:"conf_path" doc:"ctl.conf path (FreeBSD); empty on Linux"`
	ConfExists  bool   `json:"conf_exists"`
}

// getISCSIStatus handles GET /api/v1/iscsi/status
// Returns {backend, initialized, conf_path, conf_exists}. Only ctld (FreeBSD)
// has an init gate; targetcli (Linux) always reports initialized.
func (h *Handler) getISCSIStatus(context.Context, struct{}) (ISCSIStatus, error) {
	backend := iscsi.Backend()
	resp := ISCSIStatus{
		Backend:     backend,
		Initialized: backend == "linux",
	}
	if backend == "freebsd" {
		_, err := os.Stat(iscsi.CtlConfPath)
		resp.Initialized = iscsi.CtldInitialized()
		resp.ConfPath = iscsi.CtlConfPath
		resp.ConfExists = err == nil
	}
	return resp, nil
}

// ISCSIInitResult is the result of taking over /etc/ctl.conf.
type ISCSIInitResult struct {
	Initialized bool               `json:"initialized"`
	Imported    int                `json:"imported" doc:"Targets imported from the existing ctl.conf"`
	Skipped     int                `json:"skipped" doc:"ctl.conf entries dumpstore cannot represent (kept in the backup)"`
	Tasks       []ansible.TaskStep `json:"tasks"`
}

// initISCSI handles POST /api/v1/iscsi/init (FreeBSD/ctld only).
// Takes ownership of /etc/ctl.conf: backs up an existing file once to
// ctl.conf.pre-dumpstore, imports the targets dumpstore's model represents
// (reporting everything else as skipped steps), and renders the file from
// template. Safe to call multiple times.
func (h *Handler) initISCSI(ctx context.Context, _ struct{}) (ISCSIInitResult, error) {
	switch iscsi.Backend() {
	case "freebsd":
	case "linux":
		return ISCSIInitResult{}, errStatus(http.StatusBadRequest,
			fmt.Errorf("init applies to ctld (FreeBSD) only — targetcli needs no initialisation"))
	default:
		return ISCSIInitResult{}, errStatus(http.StatusNotImplemented,
			fmt.Errorf("ctld not installed"))
	}

	h.iscsiMu.Lock()
	defer h.iscsiMu.Unlock()

	raw, err := iscsi.ReadCtlConfRaw()
	if err != nil {
		return ISCSIInitResult{}, err
	}
	targets, skipped := iscsi.Import(raw)

	id := operations.IDFromContext(ctx)
	var steps []ansible.TaskStep
	for _, s := range skipped {
		step := ansible.TaskStep{Name: "Not imported: " + s, Status: "skipped", Msg: "kept in " + iscsi.CtlConfBackupPath}
		h.operations.Step(id, step)
		steps = append(steps, step)
	}

	out, err := h.applyCtlConf(ctx, targets, true)
	auditLog(ctx, "iscsi.init", "", err)
	if out != nil {
		steps = append(steps, out.Steps()...)
	}
	if err != nil {
		return ISCSIInitResult{}, opError(err, steps)
	}
	return ISCSIInitResult{
		Initialized: iscsi.CtldInitialized(),
		Imported:    len(targets),
		Skipped:     len(skipped),
		Tasks:       steps,
	}, nil
}

// getISCSITargets handles GET /api/v1/iscsi/targets
// Returns all active iSCSI targets from the platform backend (targetcli or ctld).
func (h *Handler) getISCSITargets(context.Context, struct{}) ([]iscsi.Target, error) {
	return iscsi.ListTargets()
}

// CreateISCSITargetRequest is the body of POST /api/v1/iscsi/targets.
type CreateISCSITargetRequest struct {
	Zvol         string   `json:"zvol" doc:"Backing zvol, e.g. tank/vol1"`
	IQN          string   `json:"iqn" doc:"Target IQN"`
	PortalIP     string   `json:"portal_ip,omitempty" doc:"Portal IP address (default 0.0.0.0)"`
	PortalPort   string   `json:"portal_port,omitempty" doc:"Portal TCP port (default 3260)"`
	AuthMode     string   `json:"auth_mode" enum:"none,chap"`
	CHAPUser     string   `json:"chap_user,omitempty" doc:"Required when auth_mode is chap"`
	CHAPPassword string   `json:"chap_password,omitempty" doc:"Required when auth_mode is chap"`
	Initiators   []string `json:"initiators,omitempty" doc:"Allowed initiator IQNs"`
}

// ISCSITargetResult is the result of creating or deleting an iSCSI target.
type ISCSITargetResult struct {
	IQN   string             `json:"iqn"`
	Zvol  string             `json:"zvol"`
	Tasks []ansible.TaskStep `json:"tasks"`
}

// createISCSITarget handles POST /api/v1/iscsi/targets
// Body: {zvol, iqn, portal_ip, portal_port, auth_mode, chap_user, chap_password, initiators}
func (h *Handler) createISCSITarget(ctx context.Context, in struct {
	Body CreateISCSITargetRequest `body:""`
}) (ISCSITargetResult, error) {
	req := in.Body
	if !validZFSName(req.Zvol) || !strings.Contains(req.Zvol, "/") {
		return ISCSITargetResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid zvol name"))
	}
	if !validIQN(req.IQN) {
		return ISCSITargetResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid IQN format"))
	}
	if req.PortalIP == "" {
		req.PortalIP = "0.0.0.0"
	}
	if !validPortalIP(req.PortalIP) {
		return ISCSITargetResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid portal IP address"))
	}
	if req.PortalPort == "" {
		req.PortalPort = "3260"
	}
	if !validPort(req.PortalPort) {
		return ISCSITargetResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid portal port"))
	}
	if req.AuthMode != "none" && req.AuthMode != "chap" {
		return ISCSITargetResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("auth_mode must be 'none' or 'chap'"))
	}
	if req.AuthMode == "chap" {
		if req.CHAPUser == "" || req.CHAPPassword == "" {
			return ISCSITargetResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("chap_user and chap_password required when auth_mode is 'chap'"))
		}
		if !safePropertyValue(req.CHAPUser) {
			return ISCSITargetResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid characters in CHAP username"))
		}
		if !validCHAPSecret(req.CHAPPassword) {
			return ISCSITargetResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("CHAP password must not contain quotes, backslashes, or newlines"))
		}
	}
	for _, ini := range req.Initiators {
		if !validIQN(ini) {
			return ISCSITargetResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid initiator IQN: %s", ini))
		}
	}

	backend := iscsi.Backend()
	if backend == "" {
		return ISCSITargetResult{}, errStatus(http.StatusNotImplemented,
			fmt.Errorf("no iSCSI backend detected (targetcli or ctld required)"))
	}

	h.refMu.Lock() // the zvol must not be destroyed or renamed while the target is added (#183)
	defer h.refMu.Unlock()

	if backend == "freebsd" {
		return h.createCtldTarget(ctx, iscsi.Target{
			IQN:        req.IQN,
			ZvolName:   req.Zvol,
			Portals:    []string{net.JoinHostPort(req.PortalIP, req.PortalPort)},
			AuthMode:   req.AuthMode,
			Initiators: req.Initiators,
			CHAPUser:   req.CHAPUser,
			CHAPSecret: req.CHAPPassword,
		})
	}

	out, err := h.runOp(ctx, "iscsi_target_create.yml", map[string]string{
		"zvol":          req.Zvol,
		"iqn":           req.IQN,
		"portal_ip":     req.PortalIP,
		"portal_port":   req.PortalPort,
		"auth_mode":     req.AuthMode,
		"chap_user":     req.CHAPUser,
		"chap_password": req.CHAPPassword,
		"initiators":    strings.Join(req.Initiators, ","),
	})
	auditLog(ctx, "iscsi.create", req.Zvol, err)
	if err != nil {
		return ISCSITargetResult{}, playbookError(err, out)
	}
	return ISCSITargetResult{Zvol: req.Zvol, IQN: req.IQN, Tasks: out.Steps()}, nil
}

// deleteISCSITarget handles DELETE /api/v1/iscsi/targets/{iqn}
func (h *Handler) deleteISCSITarget(ctx context.Context, in deleteISCSIParams) (ISCSITargetResult, error) {
	iqn := in.IQN
	if !validIQN(iqn) {
		return ISCSITargetResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid IQN"))
	}

	backend := iscsi.Backend()
	if backend == "" {
		return ISCSITargetResult{}, errStatus(http.StatusNotImplemented,
			fmt.Errorf("no iSCSI backend detected (targetcli or ctld required)"))
	}
	if backend == "freebsd" {
		return h.deleteCtldTarget(ctx, iqn)
	}

	// targetcli needs the zvol to remove its backstore; look it up by IQN.
	targets, err := iscsi.ListTargets()
	if err != nil {
		return ISCSITargetResult{}, err
	}
	i := slices.IndexFunc(targets, func(t iscsi.Target) bool { return strings.EqualFold(t.IQN, iqn) })
	if i < 0 {
		return ISCSITargetResult{}, errStatus(http.StatusNotFound, fmt.Errorf("no iSCSI target %s", iqn))
	}
	zvol := targets[i].ZvolName

	out, err := h.runOp(ctx, "iscsi_target_delete.yml", map[string]string{"iqn": iqn, "zvol": zvol})
	auditLog(ctx, "iscsi.delete", zvol, err)
	if err != nil {
		return ISCSITargetResult{}, playbookError(err, out)
	}
	return ISCSITargetResult{IQN: iqn, Zvol: zvol, Tasks: out.Steps()}, nil
}

// createCtldTarget adds t to the dumpstore-owned ctl.conf and applies it.
// Caller holds refMu.
func (h *Handler) createCtldTarget(ctx context.Context, t iscsi.Target) (ISCSITargetResult, error) {
	h.iscsiMu.Lock()
	defer h.iscsiMu.Unlock()
	if err := requireCtldInit(); err != nil {
		return ISCSITargetResult{}, err
	}
	targets, err := iscsi.LoadCtld()
	if err != nil {
		return ISCSITargetResult{}, err
	}
	for _, e := range targets {
		if strings.EqualFold(e.IQN, t.IQN) {
			return ISCSITargetResult{}, errStatus(http.StatusConflict, fmt.Errorf("iSCSI target %s already exists", t.IQN))
		}
		if e.ZvolName == t.ZvolName {
			return ISCSITargetResult{}, errStatus(http.StatusConflict,
				fmt.Errorf("zvol %s is already exported as %s", t.ZvolName, e.IQN))
		}
	}
	if t.Initiators == nil {
		t.Initiators = []string{}
	}
	// ctld only logs a warning for a missing LUN path and exports nothing.
	if _, err := os.Stat("/dev/zvol/" + t.ZvolName); err != nil {
		return ISCSITargetResult{}, errStatus(http.StatusBadRequest,
			fmt.Errorf("zvol device /dev/zvol/%s not found", t.ZvolName))
	}

	out, err := h.applyCtlConf(ctx, append(targets, t), false)
	auditLog(ctx, "iscsi.create", t.ZvolName, err)
	if err != nil {
		return ISCSITargetResult{}, playbookError(err, out)
	}
	return ISCSITargetResult{Zvol: t.ZvolName, IQN: t.IQN, Tasks: out.Steps()}, nil
}

// deleteCtldTarget removes the target iqn (exporting zvol) from the
// dumpstore-owned ctl.conf and applies it.
func (h *Handler) deleteCtldTarget(ctx context.Context, iqn string) (ISCSITargetResult, error) {
	h.iscsiMu.Lock()
	defer h.iscsiMu.Unlock()
	if err := requireCtldInit(); err != nil {
		return ISCSITargetResult{}, err
	}
	targets, err := iscsi.LoadCtld()
	if err != nil {
		return ISCSITargetResult{}, err
	}
	var zvol string
	kept := make([]iscsi.Target, 0, len(targets))
	for _, t := range targets {
		if strings.EqualFold(t.IQN, iqn) {
			zvol = t.ZvolName
			continue
		}
		kept = append(kept, t)
	}
	if len(kept) == len(targets) {
		return ISCSITargetResult{}, errStatus(http.StatusNotFound, fmt.Errorf("no iSCSI target %s", iqn))
	}

	out, err := h.applyCtlConf(ctx, kept, false)
	auditLog(ctx, "iscsi.delete", zvol, err)
	if err != nil {
		return ISCSITargetResult{}, playbookError(err, out)
	}
	return ISCSITargetResult{IQN: iqn, Zvol: zvol, Tasks: out.Steps()}, nil
}
