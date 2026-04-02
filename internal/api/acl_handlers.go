package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"dumpstore/internal/ansible"
	"dumpstore/internal/zfs"
)

// aclSafeRe matches strings that are safe to pass as ACE specs to setfacl / nfs4_setfacl.
// Allows letters, digits, and the small set of punctuation found in valid ACE specs:
//   - POSIX: "user:alice:rwx", "default:group:storage:r-x"
//   - NFSv4: "A::OWNER@:rwaDxtTnNcCoy", "A:fd:alice@localdomain:rwx"
//
// Per-field '@' rules (applied to each colon-separated field):
//   - At most one '@' per field.
//   - '@' must not be the first character of a field.
//   - A trailing '@' is only valid when the entire prefix is uppercase letters
//     (NFSv4 well-known principals: OWNER@, GROUP@, EVERYONE@).
var aclSafeRe = func() func(string) bool {
	return func(s string) bool {
		if len(s) == 0 {
			return false
		}
		// First pass: character-class check.
		for _, c := range s {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
				c == ':' || c == '@' || c == '-' || c == '_' || c == '.' || c == '=' || c == '/') {
				return false
			}
		}
		// Second pass: validate '@' usage within each colon-separated field.
		for _, field := range strings.Split(s, ":") {
			atIdx := strings.Index(field, "@")
			if atIdx == -1 {
				continue
			}
			if strings.Count(field, "@") > 1 {
				return false // e.g. "alice@@domain"
			}
			if atIdx == 0 {
				return false // leading '@'
			}
			if atIdx == len(field)-1 {
				// Trailing '@': only valid for all-uppercase prefixes (OWNER@, GROUP@, EVERYONE@).
				for _, c := range field[:atIdx] {
					if c < 'A' || c > 'Z' {
						return false
					}
				}
			}
		}
		return true
	}
}()

// getACLStatus handles GET /api/acl-status
// Returns a map of dataset name → bool indicating whether the dataset has
// non-trivial ACL entries on its mountpoint.
func (h *Handler) getACLStatus(w http.ResponseWriter, r *http.Request) {
	datasets, err := zfs.ListDatasets()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err, nil)
		return
	}
	status := make(map[string]bool, len(datasets))
	for _, d := range datasets {
		if d.Type != "filesystem" || d.Mountpoint == "none" || d.Mountpoint == "-" || d.Mountpoint == "" {
			continue
		}
		// DatasetHasACL checks actual getfacl output; fall back to acltype
		// when getfacl / nfs4_getfacl are not available.
		hasACL, aclErr := zfs.DatasetHasACL(d.Mountpoint)
		if aclErr != nil {
			slog.Warn("DatasetHasACL failed, falling back to acltype", "dataset", d.Name, "err", aclErr)
			hasACL = d.ACLType != "" && d.ACLType != "off"
		} else if !hasACL {
			hasACL = d.ACLType != "" && d.ACLType != "off"
		}
		status[d.Name] = hasACL
	}
	writeJSON(w, status)
}

// getDatasetACL handles GET /api/acl/{dataset...}
func (h *Handler) getDatasetACL(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("dataset")
	if name == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("dataset name required"), nil)
		return
	}
	if !validZFSName(name) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid dataset name"), nil)
		return
	}
	acl, err := zfs.GetDatasetACL(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err, nil)
		return
	}
	writeJSON(w, acl)
}

// setACLEntry handles POST /api/acl/{dataset...}
// Body: {"ace":"user:alice:rwx","recursive":false}  (POSIX)
//
//	{"ace":"A::alice@localdomain:rwaDxtTnNcCoy"}       (NFSv4)
func (h *Handler) setACLEntry(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("dataset")
	if name == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("dataset name required"), nil)
		return
	}
	if !validZFSName(name) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid dataset name"), nil)
		return
	}
	if ok, err := datasetExists(name); err != nil {
		writeError(w, http.StatusInternalServerError, err, nil)
		return
	} else if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("dataset %q not found", name), nil)
		return
	}

	var req struct {
		ACE       string `json:"ace"`
		Recursive bool   `json:"recursive"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}
	if req.ACE == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("ace is required"), nil)
		return
	}
	if !aclSafeRe(req.ACE) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("ace contains invalid characters"), nil)
		return
	}

	acl, err := zfs.GetDatasetACL(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err, nil)
		return
	}
	if acl.ACLType == "off" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("acltype is off for dataset %s; enable it first", name), nil)
		return
	}
	if acl.Mountpoint == "none" || acl.Mountpoint == "-" || acl.Mountpoint == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("dataset %s has no mountpoint", name), nil)
		return
	}

	recursive := "false"
	if req.Recursive {
		recursive = "true"
	}

	var playbook string
	vars := map[string]string{
		"dataset":    name,
		"mountpoint": acl.Mountpoint,
		"ace":        req.ACE,
		"recursive":  recursive,
	}
	switch acl.ACLType {
	case "posix":
		playbook = "acl_set_posix.yml"
	case "nfsv4":
		playbook = "acl_set_nfs4.yml"
	default:
		writeError(w, http.StatusBadRequest, fmt.Errorf("unsupported acltype: %s", acl.ACLType), nil)
		return
	}

	out, err := h.runOp(playbook, vars)
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(w, http.StatusInternalServerError, err, steps)
		return
	}
	writeJSON(w, map[string]any{"dataset": name, "ace": req.ACE, "tasks": out.Steps()})
}

// removeACLEntry handles DELETE /api/acl/{dataset...}?entry=<spec>
// For POSIX: entry is the removal spec (e.g. "user:alice", "default:group:storage")
// For NFSv4: entry is the full ACE string to remove
func (h *Handler) removeACLEntry(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("dataset")
	if name == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("dataset name required"), nil)
		return
	}
	if !validZFSName(name) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid dataset name"), nil)
		return
	}

	entry := r.URL.Query().Get("entry")
	if entry == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("entry query parameter required"), nil)
		return
	}
	if !aclSafeRe(entry) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("entry contains invalid characters"), nil)
		return
	}
	if ok, err := datasetExists(name); err != nil {
		writeError(w, http.StatusInternalServerError, err, nil)
		return
	} else if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("dataset %q not found", name), nil)
		return
	}

	recursive := r.URL.Query().Get("recursive") == "true"

	acl, err := zfs.GetDatasetACL(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err, nil)
		return
	}
	if acl.Mountpoint == "none" || acl.Mountpoint == "-" || acl.Mountpoint == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("dataset %s has no mountpoint", name), nil)
		return
	}

	recursiveStr := "false"
	if recursive {
		recursiveStr = "true"
	}

	var playbook string
	vars := map[string]string{
		"mountpoint": acl.Mountpoint,
		"ace":        entry,
		"recursive":  recursiveStr,
	}
	switch acl.ACLType {
	case "posix":
		playbook = "acl_remove_posix.yml"
	case "nfsv4":
		playbook = "acl_remove_nfs4.yml"
	default:
		writeError(w, http.StatusBadRequest, fmt.Errorf("unsupported acltype: %s", acl.ACLType), nil)
		return
	}

	out, err := h.runOp(playbook, vars)
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(w, http.StatusInternalServerError, err, steps)
		return
	}
	writeJSON(w, map[string]any{"dataset": name, "entry": entry, "tasks": out.Steps()})
}
