package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"

	"dumpstore/internal/ansible"
	"dumpstore/internal/smb"
	"dumpstore/internal/system"
	"dumpstore/internal/zfs"
)

// requireSMBInit writes a 409 error if smb.conf does not exist at the expected
// OS path. Returns true if the caller should abort.
func (h *Handler) requireSMBInit(w http.ResponseWriter, r *http.Request) bool {
	if !smb.IsInitialized(runtime.GOOS) {
		writeError(r.Context(), w, http.StatusConflict,
			fmt.Errorf("Samba not initialised — run POST /api/smb/init first"), nil)
		return true
	}
	return false
}

// applyConfig renders cfg to a temp file and runs smb_apply.yml to deploy it.
// The temp file is removed after the playbook exits regardless of outcome.
func (h *Handler) applyConfig(r *http.Request, cfg *smb.SMBConfig) (*ansible.PlaybookOutput, error) {
	tmp, err := os.CreateTemp("", "dumpstore-smb-*.conf")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tmp.Close()
	defer os.Remove(tmp.Name())

	goos := runtime.GOOS
	if err := cfg.RenderToFile(tmp.Name(), goos); err != nil {
		return nil, err
	}

	dirs := cfg.DirsToCreate()
	dirsJSON, _ := json.Marshal(dirs)
	servicesJSON, _ := json.Marshal(smb.ServiceNames(goos))

	return h.runOp("smb_apply.yml", map[string]string{
		"src":             tmp.Name(),
		"smb_conf":        smb.ConfPath(goos),
		"dirs":            string(dirsJSON),
		"samba_services":  string(servicesJSON),
	})
}

// getSMBStatus handles GET /api/smb/status
// Returns {"initialized":bool,"conf_path":"/etc/samba/smb.conf","os":"linux"}
func (h *Handler) getSMBStatus(w http.ResponseWriter, r *http.Request) {
	goos := runtime.GOOS
	writeJSON(r.Context(), w, map[string]any{
		"initialized": smb.IsInitialized(goos),
		"conf_path":   smb.ConfPath(goos),
		"os":          goos,
	})
}

// initSamba handles POST /api/smb/init
// Checks smbd is installed, backs up any existing config, creates the
// usershares directory, then writes dumpstore's own smb.conf from template.
// Preserves workgroup and server string from an existing config if present.
// Safe to call multiple times.
func (h *Handler) initSamba(w http.ResponseWriter, r *http.Request) {
	h.smbMu.Lock()
	defer h.smbMu.Unlock()

	goos := runtime.GOOS

	// Fail fast with a distro-specific install hint if smbd is missing.
	if hint := system.SambaInstallHint(); hint != "" {
		writeError(r.Context(), w, http.StatusUnprocessableEntity,
			fmt.Errorf("Samba not installed — run: %s", hint), nil)
		return
	}

	// Parse existing config to carry over workgroup/server string if present.
	// Falls back to defaults if the file doesn't exist yet.
	cfg, err := smb.ParseSMBConfig(goos)
	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
		return
	}

	// Step 1: check smbd is installed, back up existing conf, create usershares dir.
	out1, err := h.runOp("smb_init.yml", map[string]string{})
	auditLog(r.Context(), r, "smb.init", "", err)
	if err != nil {
		var steps []ansible.TaskStep
		if out1 != nil {
			steps = out1.Steps()
		}
		writeError(r.Context(), w, http.StatusInternalServerError, err, steps)
		return
	}

	// Step 2: write our own clean smb.conf from template.
	out2, err := h.applyConfig(r, &cfg)
	if err != nil {
		var steps []ansible.TaskStep
		steps = append(steps, out1.Steps()...)
		if out2 != nil {
			steps = append(steps, out2.Steps()...)
		}
		writeError(r.Context(), w, http.StatusInternalServerError, err, steps)
		return
	}

	var allSteps []ansible.TaskStep
	allSteps = append(allSteps, out1.Steps()...)
	allSteps = append(allSteps, out2.Steps()...)

	writeJSON(r.Context(), w, map[string]any{
		"initialized": smb.IsInitialized(goos),
		"tasks":       allSteps,
	})
}

// getSMBShares handles GET /api/smb-shares
func (h *Handler) getSMBShares(w http.ResponseWriter, r *http.Request) {
	if h.requireSMBInit(w, r) {
		return
	}
	shares, err := system.ListSMBUsershares()
	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
		return
	}
	writeJSON(r.Context(), w, shares)
}

// setSMBShare handles POST /api/smb-share/{dataset...}
func (h *Handler) setSMBShare(w http.ResponseWriter, r *http.Request) {
	if h.requireSMBInit(w, r) {
		return
	}
	dataset := r.PathValue("dataset")
	if dataset == "" {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("dataset name required"), nil)
		return
	}
	if !validZFSName(dataset) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid dataset name"), nil)
		return
	}
	var req struct {
		Sharename string `json:"sharename"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}
	if req.Sharename == "" {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("sharename is required"), nil)
		return
	}
	if !validSMBShare(req.Sharename) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid sharename"), nil)
		return
	}
	out, err := h.runOp("smb_usershare_set.yml", map[string]string{
		"dataset":   dataset,
		"sharename": req.Sharename,
	})
	auditLog(r.Context(), r, "smb_share.set", dataset, err)
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(r.Context(), w, http.StatusInternalServerError, err, steps)
		return
	}
	writeJSON(r.Context(), w, map[string]any{"dataset": dataset, "sharename": req.Sharename, "tasks": out.Steps()})
}

// deleteSMBShare handles DELETE /api/smb-share/{dataset...}?name=<sharename>
func (h *Handler) deleteSMBShare(w http.ResponseWriter, r *http.Request) {
	if h.requireSMBInit(w, r) {
		return
	}
	dataset := r.PathValue("dataset")
	if dataset == "" {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("dataset name required"), nil)
		return
	}
	sharename := r.URL.Query().Get("name")
	if sharename == "" {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("name query parameter required"), nil)
		return
	}
	if !validSMBShare(sharename) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid sharename"), nil)
		return
	}
	out, err := h.runOp("smb_usershare_unset.yml", map[string]string{
		"sharename": sharename,
	})
	auditLog(r.Context(), r, "smb_share.delete", dataset, err)
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(r.Context(), w, http.StatusInternalServerError, err, steps)
		return
	}
	writeJSON(r.Context(), w, map[string]any{"dataset": dataset, "sharename": sharename, "tasks": out.Steps()})
}

// getSambaUsers handles GET /api/smb-users
func (h *Handler) getSambaUsers(w http.ResponseWriter, r *http.Request) {
	users, err := system.ListSambaUsers()
	if errors.Is(err, system.ErrSambaNotAvailable) {
		writeJSON(r.Context(), w, map[string]any{"available": false, "users": []string{}})
		return
	}
	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
		return
	}
	if users == nil {
		users = []string{}
	}
	writeJSON(r.Context(), w, map[string]any{"available": true, "users": users})
}

// addSambaUser handles POST /api/smb-users/{name}
func (h *Handler) addSambaUser(w http.ResponseWriter, r *http.Request) {
	if h.requireSMBInit(w, r) {
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("username required"), nil)
		return
	}
	if !validUnixName(name) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid username"), nil)
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}
	if req.Password == "" {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("password is required"), nil)
		return
	}
	if !safePassword(req.Password) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("password must not contain newline characters"), nil)
		return
	}
	h.userMu.Lock()
	defer h.userMu.Unlock()
	out, err := h.runOp("smb_user_add.yml", map[string]string{
		"username":     name,
		"smb_password": req.Password,
	})
	auditLog(r.Context(), r, "smb_user.add", name, err)
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(r.Context(), w, http.StatusInternalServerError, err, steps)
		return
	}
	writeJSON(r.Context(), w, map[string]any{"username": name, "tasks": out.Steps()})
}

// removeSambaUser handles DELETE /api/smb-users/{name}
func (h *Handler) removeSambaUser(w http.ResponseWriter, r *http.Request) {
	if h.requireSMBInit(w, r) {
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("username required"), nil)
		return
	}
	if !validUnixName(name) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid username"), nil)
		return
	}
	h.userMu.Lock()
	defer h.userMu.Unlock()
	out, err := h.runOp("smb_user_remove.yml", map[string]string{
		"username": name,
	})
	auditLog(r.Context(), r, "smb_user.remove", name, err)
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(r.Context(), w, http.StatusInternalServerError, err, steps)
		return
	}
	writeJSON(r.Context(), w, map[string]any{"username": name, "tasks": out.Steps()})
}

// getSMBHomes handles GET /api/smb/homes
func (h *Handler) getSMBHomes(w http.ResponseWriter, r *http.Request) {
	cfg, err := smb.ParseSMBConfig(runtime.GOOS)
	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
		return
	}
	if cfg.Homes == nil {
		writeJSON(r.Context(), w, map[string]any{"enabled": false})
		return
	}
	writeJSON(r.Context(), w, map[string]any{
		"enabled":        true,
		"path":           cfg.Homes.Path,
		"browseable":     cfg.Homes.Browseable,
		"read_only":      cfg.Homes.ReadOnly,
		"create_mask":    cfg.Homes.CreateMask,
		"directory_mask": cfg.Homes.DirectoryMask,
	})
}

// setSMBHomes handles POST /api/smb/homes
func (h *Handler) setSMBHomes(w http.ResponseWriter, r *http.Request) {
	if h.requireSMBInit(w, r) {
		return
	}
	var req struct {
		Dataset       string `json:"dataset"`
		Path          string `json:"path"`
		Browseable    string `json:"browseable"`
		ReadOnly      string `json:"read_only"`
		CreateMask    string `json:"create_mask"`
		DirectoryMask string `json:"directory_mask"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err), nil)
		return
	}

	// Resolve dataset → mountpoint if path not supplied directly
	if req.Dataset != "" && req.Path == "" {
		if !validZFSName(req.Dataset) {
			writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid dataset name"), nil)
			return
		}
		mp, err := datasetMountpoint(req.Dataset)
		if err != nil {
			writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
			return
		}
		if mp == "" {
			writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("dataset %q has no mountpoint", req.Dataset), nil)
			return
		}
		req.Path = mp + "/%U"
	}
	if req.Path == "" {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("path is required (or provide dataset)"), nil)
		return
	}

	// Apply defaults
	if req.Browseable == "" {
		req.Browseable = "no"
	}
	if req.ReadOnly == "" {
		req.ReadOnly = "no"
	}
	if req.CreateMask == "" {
		req.CreateMask = "0644"
	}
	if req.DirectoryMask == "" {
		req.DirectoryMask = "0755"
	}

	if req.Browseable != "yes" && req.Browseable != "no" {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("browseable must be yes or no"), nil)
		return
	}
	if req.ReadOnly != "yes" && req.ReadOnly != "no" {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("read_only must be yes or no"), nil)
		return
	}
	if !reOctalMask.MatchString(req.CreateMask) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("create_mask must be a 3- or 4-digit octal value"), nil)
		return
	}
	if !reOctalMask.MatchString(req.DirectoryMask) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("directory_mask must be a 3- or 4-digit octal value"), nil)
		return
	}

	h.smbMu.Lock()
	defer h.smbMu.Unlock()

	cfg, err := smb.ParseSMBConfig(runtime.GOOS)
	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
		return
	}
	cfg.Homes = &smb.SMBHomesConfig{
		Path:          req.Path,
		Browseable:    req.Browseable,
		ReadOnly:      req.ReadOnly,
		CreateMask:    req.CreateMask,
		DirectoryMask: req.DirectoryMask,
	}

	out, err := h.applyConfig(r, &cfg)
	auditLog(r.Context(), r, "smb_homes.set", req.Dataset, err)
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(r.Context(), w, http.StatusInternalServerError, err, steps)
		return
	}
	writeJSON(r.Context(), w, map[string]any{
		"enabled":        true,
		"path":           req.Path,
		"browseable":     req.Browseable,
		"read_only":      req.ReadOnly,
		"create_mask":    req.CreateMask,
		"directory_mask": req.DirectoryMask,
		"tasks":          out.Steps(),
	})
}

// deleteSMBHomes handles DELETE /api/smb/homes
func (h *Handler) deleteSMBHomes(w http.ResponseWriter, r *http.Request) {
	if h.requireSMBInit(w, r) {
		return
	}
	h.smbMu.Lock()
	defer h.smbMu.Unlock()

	cfg, err := smb.ParseSMBConfig(runtime.GOOS)
	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
		return
	}
	cfg.Homes = nil

	out, err := h.applyConfig(r, &cfg)
	auditLog(r.Context(), r, "smb_homes.delete", "", err)
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(r.Context(), w, http.StatusInternalServerError, err, steps)
		return
	}
	writeJSON(r.Context(), w, map[string]any{"tasks": out.Steps()})
}

// getTimeMachineShares handles GET /api/smb/timemachine
func (h *Handler) getTimeMachineShares(w http.ResponseWriter, r *http.Request) {
	cfg, err := smb.ParseSMBConfig(runtime.GOOS)
	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
		return
	}
	shares := cfg.TimeMachine
	if shares == nil {
		shares = []smb.TimeMachineShare{}
	}
	writeJSON(r.Context(), w, shares)
}

// createTimeMachineShare handles POST /api/smb/timemachine
func (h *Handler) createTimeMachineShare(w http.ResponseWriter, r *http.Request) {
	if h.requireSMBInit(w, r) {
		return
	}
	var req struct {
		Sharename  string `json:"sharename"`
		Dataset    string `json:"dataset"`
		Path       string `json:"path"`
		MaxSize    string `json:"max_size"`
		ValidUsers string `json:"valid_users"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err), nil)
		return
	}
	if req.Sharename == "" {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("sharename is required"), nil)
		return
	}
	if !validSMBShare(req.Sharename) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid share name"), nil)
		return
	}

	// Resolve dataset → mountpoint
	if req.Dataset != "" && req.Path == "" {
		if !validZFSName(req.Dataset) {
			writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid dataset name"), nil)
			return
		}
		mp, err := datasetMountpoint(req.Dataset)
		if err != nil {
			writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
			return
		}
		if mp == "" {
			writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("dataset %q has no mountpoint", req.Dataset), nil)
			return
		}
		req.Path = mp
	}
	if req.Path == "" {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("path is required (or provide dataset)"), nil)
		return
	}

	h.smbMu.Lock()
	defer h.smbMu.Unlock()

	cfg, err := smb.ParseSMBConfig(runtime.GOOS)
	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
		return
	}

	// Replace if share with same name exists, otherwise append
	newShare := smb.TimeMachineShare{
		Name:       req.Sharename,
		Path:       req.Path,
		MaxSize:    req.MaxSize,
		ValidUsers: req.ValidUsers,
	}
	replaced := false
	for i, s := range cfg.TimeMachine {
		if s.Name == req.Sharename {
			cfg.TimeMachine[i] = newShare
			replaced = true
			break
		}
	}
	if !replaced {
		cfg.TimeMachine = append(cfg.TimeMachine, newShare)
	}

	out, err := h.applyConfig(r, &cfg)
	auditLog(r.Context(), r, "smb_timemachine.create", req.Dataset, err)
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(r.Context(), w, http.StatusInternalServerError, err, steps)
		return
	}
	shares := cfg.TimeMachine
	if shares == nil {
		shares = []smb.TimeMachineShare{}
	}
	writeJSON(r.Context(), w, map[string]any{"shares": shares, "tasks": out.Steps()})
}

// deleteTimeMachineShare handles DELETE /api/smb/timemachine/{sharename}
func (h *Handler) deleteTimeMachineShare(w http.ResponseWriter, r *http.Request) {
	if h.requireSMBInit(w, r) {
		return
	}
	sharename := r.PathValue("sharename")
	if sharename == "" {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("sharename is required"), nil)
		return
	}
	if !validSMBShare(sharename) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid share name"), nil)
		return
	}

	h.smbMu.Lock()
	defer h.smbMu.Unlock()

	cfg, err := smb.ParseSMBConfig(runtime.GOOS)
	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
		return
	}

	filtered := cfg.TimeMachine[:0]
	for _, s := range cfg.TimeMachine {
		if s.Name != sharename {
			filtered = append(filtered, s)
		}
	}
	cfg.TimeMachine = filtered

	out, err := h.applyConfig(r, &cfg)
	auditLog(r.Context(), r, "smb_timemachine.delete", sharename, err)
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(r.Context(), w, http.StatusInternalServerError, err, steps)
		return
	}
	writeJSON(r.Context(), w, map[string]any{"tasks": out.Steps()})
}

// datasetMountpoint looks up the mountpoint for the named dataset.
// Returns "" if the dataset is not found or has no mountpoint.
func datasetMountpoint(dataset string) (string, error) {
	datasets, err := zfs.ListDatasets()
	if err != nil {
		return "", fmt.Errorf("list datasets: %w", err)
	}
	for _, ds := range datasets {
		if ds.Name == dataset {
			if ds.Mountpoint == "-" || ds.Mountpoint == "none" {
				return "", nil
			}
			return ds.Mountpoint, nil
		}
	}
	return "", nil
}
