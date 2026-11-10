package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
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
func (h *Handler) getACLStatus(ctx context.Context, _ struct{}) (map[string]bool, error) {
	datasets, err := zfs.ListDatasets()
	if err != nil {
		return nil, err
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
			slog.WarnContext(ctx, "DatasetHasACL failed, falling back to acltype", "dataset", d.Name, "err", aclErr)
			hasACL = d.ACLType != "" && d.ACLType != "off"
		} else if !hasACL {
			hasACL = d.ACLType != "" && d.ACLType != "off"
		}
		status[d.Name] = hasACL
	}
	return status, nil
}

// getDatasetACL handles GET /api/acl/{dataset...}
func (h *Handler) getDatasetACL(_ context.Context, p datasetParam) (*zfs.DatasetACL, error) {
	if p.Dataset == "" {
		return nil, errStatus(http.StatusBadRequest, fmt.Errorf("dataset name required"))
	}
	if !validZFSName(p.Dataset) {
		return nil, errStatus(http.StatusBadRequest, fmt.Errorf("invalid dataset name"))
	}
	return zfs.GetDatasetACL(p.Dataset)
}

// setACLEntry handles POST /api/acl/{dataset...}
// Body: {"ace":"user:alice:rwx","recursive":false}  (POSIX)
//
//	{"ace":"A::alice@localdomain:rwaDxtTnNcCoy"}       (NFSv4)
func (h *Handler) setACLEntry(ctx context.Context, in struct {
	datasetParam
	Body SetACLEntryRequest `body:""`
}) (ACLEntryResult, error) {
	name := in.Dataset
	if name == "" {
		return ACLEntryResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("dataset name required"))
	}
	if !validZFSName(name) {
		return ACLEntryResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid dataset name"))
	}
	if ok, err := datasetExists(name); err != nil {
		return ACLEntryResult{}, err
	} else if !ok {
		return ACLEntryResult{}, errStatus(http.StatusNotFound, fmt.Errorf("dataset %q not found", name))
	}

	req := in.Body
	if req.ACE == "" {
		return ACLEntryResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("ace is required"))
	}
	if !aclSafeRe(req.ACE) {
		return ACLEntryResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("ace contains invalid characters"))
	}

	acl, err := zfs.GetDatasetACL(name)
	if err != nil {
		return ACLEntryResult{}, err
	}
	if acl.ACLType == "off" {
		return ACLEntryResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("acltype is off for dataset %s; enable it first", name))
	}
	if acl.Mountpoint == "none" || acl.Mountpoint == "-" || acl.Mountpoint == "" {
		return ACLEntryResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("dataset %s has no mountpoint", name))
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
		if runtime.GOOS == "freebsd" {
			e, ok := zfs.ParseNFSv4ACE(req.ACE)
			if !ok {
				return ACLEntryResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("ace must be type:flags:principal:perms"))
			}
			spec, err := zfs.FreeBSDACESpec(e)
			if err != nil {
				return ACLEntryResult{}, errStatus(http.StatusBadRequest, err)
			}
			vars["ace"] = spec
		}
	default:
		return ACLEntryResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("unsupported acltype: %s", acl.ACLType))
	}

	out, err := h.runOp(ctx, playbook, vars)
	auditLog(ctx, "acl.set", name, err)
	if err != nil {
		return ACLEntryResult{}, playbookError(err, out)
	}
	return ACLEntryResult{Dataset: name, ACE: req.ACE, Tasks: out.Steps()}, nil
}

// SetACLEntryRequest is the POST /api/acl/{dataset...} body.
type SetACLEntryRequest struct {
	ACE       string `json:"ace" doc:"POSIX (user:alice:rwx) or NFSv4 (A::alice@localdomain:rwaDxtTnNcCoy) entry"`
	Recursive bool   `json:"recursive,omitempty" doc:"Apply to all files below the mountpoint"`
}

// ACLEntryResult is the POST /api/acl/{dataset...} response.
type ACLEntryResult struct {
	Dataset string             `json:"dataset"`
	ACE     string             `json:"ace"`
	Tasks   []ansible.TaskStep `json:"tasks"`
}

// ACLRemoveResult is the DELETE /api/acl/{dataset...} response.
type ACLRemoveResult struct {
	Dataset string             `json:"dataset"`
	Entry   string             `json:"entry"`
	Tasks   []ansible.TaskStep `json:"tasks"`
}

// findACLEntry returns the first entry in the dataset's current ACL matched by
// the given removal spec. This prevents setfacl -x / nfs4_setfacl -x from
// silently succeeding on a nonexistent entry.
//
// For POSIX the spec is "tag:qualifier" or "default:tag:qualifier".
// For NFSv4 the spec is "type:flags:principal:perms"; we match on type+principal (parts[0] and [2]).
func findACLEntry(acl *zfs.DatasetACL, entry string) (zfs.ACLEntry, bool) {
	switch acl.ACLType {
	case "posix":
		isDefault := false
		s := entry
		if strings.HasPrefix(s, "default:") {
			isDefault = true
			s = strings.TrimPrefix(s, "default:")
		}
		parts := strings.SplitN(s, ":", 2)
		tag := parts[0]
		qualifier := ""
		if len(parts) == 2 {
			qualifier = parts[1]
		}
		for _, e := range acl.Entries {
			if e.Tag == tag && e.Qualifier == qualifier && e.Default == isDefault {
				return e, true
			}
		}
	case "nfsv4":
		parts := strings.SplitN(entry, ":", 4)
		if len(parts) < 3 {
			return zfs.ACLEntry{}, false
		}
		typ, principal := parts[0], parts[2]
		for _, e := range acl.Entries {
			if e.Tag == typ && e.Qualifier == principal {
				return e, true
			}
		}
	}
	return zfs.ACLEntry{}, false
}

// removeACLEntry handles DELETE /api/acl/{dataset...}?entry=<spec>
// For POSIX: entry is the removal spec (e.g. "user:alice", "default:group:storage")
// For NFSv4: entry is the full ACE string to remove
func (h *Handler) removeACLEntry(ctx context.Context, in removeACLParams) (ACLRemoveResult, error) {
	name := in.Dataset
	if name == "" {
		return ACLRemoveResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("dataset name required"))
	}
	if !validZFSName(name) {
		return ACLRemoveResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid dataset name"))
	}

	entry := in.Entry
	if entry == "" {
		return ACLRemoveResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("entry query parameter required"))
	}
	if !aclSafeRe(entry) {
		return ACLRemoveResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("entry contains invalid characters"))
	}
	if ok, err := datasetExists(name); err != nil {
		return ACLRemoveResult{}, err
	} else if !ok {
		return ACLRemoveResult{}, errStatus(http.StatusNotFound, fmt.Errorf("dataset %q not found", name))
	}

	recursive := in.Recursive == "true"

	acl, err := zfs.GetDatasetACL(name)
	if err != nil {
		return ACLRemoveResult{}, err
	}
	if acl.Mountpoint == "none" || acl.Mountpoint == "-" || acl.Mountpoint == "" {
		return ACLRemoveResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("dataset %s has no mountpoint", name))
	}
	match, ok := findACLEntry(acl, entry)
	if !ok {
		return ACLRemoveResult{}, errStatus(http.StatusNotFound, fmt.Errorf("ACL entry %q not found on dataset %s", entry, name))
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
		if runtime.GOOS == "freebsd" {
			// setfacl -x needs the entry exactly as stored: remove the matched one.
			spec, err := zfs.FreeBSDACESpec(match)
			if err != nil {
				return ACLRemoveResult{}, err
			}
			vars["ace"] = spec
		}
	default:
		return ACLRemoveResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("unsupported acltype: %s", acl.ACLType))
	}

	out, err := h.runOp(ctx, playbook, vars)
	auditLog(ctx, "acl.remove", name, err)
	if err != nil {
		return ACLRemoveResult{}, playbookError(err, out)
	}
	return ACLRemoveResult{Dataset: name, Entry: entry, Tasks: out.Steps()}, nil
}
