package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"dumpstore/internal/ansible"
	"dumpstore/internal/system"
	"dumpstore/internal/zfs"
)

// getSMBShares handles GET /api/smb-shares
// Returns all Samba usershares from `net usershare info *`.
func (h *Handler) getSMBShares(w http.ResponseWriter, r *http.Request) {
	shares, err := system.ListSMBUsershares()
	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
		return
	}
	writeJSON(r.Context(), w, shares)
}

// setSMBShare handles POST /api/smb-share/{dataset...}
// Body: {"sharename":"myshare"}
// Creates a usershare via `net usershare add` using the dataset's mountpoint.
func (h *Handler) setSMBShare(w http.ResponseWriter, r *http.Request) {
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
// Removes a usershare via `net usershare delete`.
func (h *Handler) deleteSMBShare(w http.ResponseWriter, r *http.Request) {
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
// Returns {"available":true,"users":["alice","bob"]} or {"available":false,"users":[]}
// when pdbedit is not installed.
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
// Body: {"password":"plaintext"}
func (h *Handler) addSambaUser(w http.ResponseWriter, r *http.Request) {
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

// configureSambaPAM handles POST /api/smb-config/pam
// Applies usershare + PAM passthrough settings to /etc/samba/smb.conf and restarts smbd/nmbd.
func (h *Handler) configureSambaPAM(w http.ResponseWriter, r *http.Request) {
	out, err := h.runOp("smb_setup.yml", map[string]string{})
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

// getSMBHomes handles GET /api/smb/homes
// Returns the current [homes] section config from smb.conf.
func (h *Handler) getSMBHomes(w http.ResponseWriter, r *http.Request) {
	writeJSON(r.Context(), w, system.ParseSMBHomes())
}

// setSMBHomes handles POST /api/smb/homes
// Body: {"path":"/tank/homes/%U","browseable":"no","read_only":"no","create_mask":"0644","directory_mask":"0755"}
// The "path" field is required. If "dataset" is provided instead, its mountpoint is resolved and /%U is appended.
func (h *Handler) setSMBHomes(w http.ResponseWriter, r *http.Request) {
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

	// Resolve dataset to path if provided
	if req.Dataset != "" && req.Path == "" {
		if !validZFSName(req.Dataset) {
			writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid dataset name"), nil)
			return
		}
		datasets, err := zfs.ListDatasets()
		if err != nil {
			writeError(r.Context(), w, http.StatusInternalServerError, fmt.Errorf("failed to list datasets: %w", err), nil)
			return
		}
		var mp string
		for _, ds := range datasets {
			if ds.Name == req.Dataset {
				mp = ds.Mountpoint
				break
			}
		}
		if mp == "" || mp == "-" || mp == "none" {
			writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("dataset %q has no mountpoint", req.Dataset), nil)
			return
		}
		req.Path = mp + "/%U"
	}

	if req.Path == "" {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("path is required (or provide dataset)"), nil)
		return
	}

	// Defaults
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

	// Validate
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

	out, err := h.runOp("smb_homes_set.yml", map[string]string{
		"path":           req.Path,
		"browseable":     req.Browseable,
		"read_only":      req.ReadOnly,
		"create_mask":    req.CreateMask,
		"directory_mask": req.DirectoryMask,
	})
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(r.Context(), w, http.StatusInternalServerError, err, steps)
		return
	}
	writeJSON(r.Context(), w, map[string]any{"config": system.ParseSMBHomes(), "tasks": out.Steps()})
}

// deleteSMBHomes handles DELETE /api/smb/homes
// Removes the [homes] section from smb.conf.
func (h *Handler) deleteSMBHomes(w http.ResponseWriter, r *http.Request) {
	out, err := h.runOp("smb_homes_unset.yml", map[string]string{})
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
// Returns all Samba shares configured as Time Machine targets.
func (h *Handler) getTimeMachineShares(w http.ResponseWriter, r *http.Request) {
	writeJSON(r.Context(), w, system.ParseTimeMachineShares())
}

// createTimeMachineShare handles POST /api/smb/timemachine
// Body: {"sharename":"TimeMachine","dataset":"tank/tm","path":"/tank/tm","max_size":"500G","valid_users":"@backup"}
func (h *Handler) createTimeMachineShare(w http.ResponseWriter, r *http.Request) {
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

	// Resolve dataset to path if provided
	if req.Dataset != "" && req.Path == "" {
		if !validZFSName(req.Dataset) {
			writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid dataset name"), nil)
			return
		}
		datasets, err := zfs.ListDatasets()
		if err != nil {
			writeError(r.Context(), w, http.StatusInternalServerError, fmt.Errorf("failed to list datasets: %w", err), nil)
			return
		}
		for _, ds := range datasets {
			if ds.Name == req.Dataset {
				req.Path = ds.Mountpoint
				break
			}
		}
		if req.Path == "" || req.Path == "-" || req.Path == "none" {
			writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("dataset %q has no mountpoint", req.Dataset), nil)
			return
		}
	}

	if req.Path == "" {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("path is required (or provide dataset)"), nil)
		return
	}

	out, err := h.runOp("smb_timemachine_set.yml", map[string]string{
		"sharename":   req.Sharename,
		"path":        req.Path,
		"max_size":    req.MaxSize,
		"valid_users": req.ValidUsers,
	})
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(r.Context(), w, http.StatusInternalServerError, err, steps)
		return
	}
	writeJSON(r.Context(), w, map[string]any{"shares": system.ParseTimeMachineShares(), "tasks": out.Steps()})
}

// deleteTimeMachineShare handles DELETE /api/smb/timemachine/{sharename}
func (h *Handler) deleteTimeMachineShare(w http.ResponseWriter, r *http.Request) {
	sharename := r.PathValue("sharename")
	if sharename == "" {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("sharename is required"), nil)
		return
	}
	if !validSMBShare(sharename) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid share name"), nil)
		return
	}

	out, err := h.runOp("smb_timemachine_unset.yml", map[string]string{
		"sharename": sharename,
	})
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
