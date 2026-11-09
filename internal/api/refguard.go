package api

// Reference guard (#183): refuse to destroy, rename, or re-mount a dataset
// while dumpstore-managed configuration still points at it. Shares store
// paths and iSCSI/replication store dataset names; removing either out from
// under them leaves config that fails at runtime — or, for usershares,
// silently re-activates with its old ACL once a new dataset appears at the
// same path.

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"runtime"
	"sort"
	"strings"

	"dumpstore/internal/iscsi"
	"dumpstore/internal/replication"
	"dumpstore/internal/smb"
	"dumpstore/internal/zfs"
)

// refOp is the dataset operation being checked.
type refOp int

const (
	refDestroy refOp = iota
	refRename
	refMountpoint
)

func (op refOp) verb() string {
	switch op {
	case refRename:
		return "rename"
	case refMountpoint:
		return "change the mountpoint of"
	default:
		return "destroy"
	}
}

// refSnapshot is everything the conflict finder looks at, gathered at once.
type refSnapshot struct {
	Datasets    []zfs.Dataset
	Usershares  []smb.Usershare
	HomesPath   string // "" = no [homes] section
	TimeMachine []smb.TimeMachineShare
	Targets     []iscsi.Target
	Tasks       []replication.Task
}

// Conflict kinds, in report order.
const (
	kindUsershare   = "smb_usershare"
	kindHomes       = "smb_homes"
	kindTimeMachine = "smb_timemachine"
	kindISCSI       = "iscsi_target"
	kindReplSource  = "replication_source"
	kindReplTarget  = "replication_target"
)

var kindOrder = map[string]int{
	kindUsershare: 0, kindHomes: 1, kindTimeMachine: 2,
	kindISCSI: 3, kindReplSource: 4, kindReplTarget: 5,
}

// refConflict is one piece of configuration that blocks the operation.
type refConflict struct {
	Kind    string `json:"kind"`
	Dataset string `json:"dataset"`        // the affected dataset the reference resolves to
	Name    string `json:"name"`           // share name, target IQN, or task name
	ID      string `json:"id,omitempty"`   // replication task ID
	Path    string `json:"path,omitempty"` // shared path (SMB kinds)
	// Describe is a human-readable one-liner for the UI; set by writeConflicts.
	Describe string `json:"describe"`
}

func (c refConflict) describe() string {
	switch c.Kind {
	case kindUsershare:
		return fmt.Sprintf("SMB share %q (%s)", c.Name, c.Path)
	case kindHomes:
		return fmt.Sprintf("SMB [homes] share (%s)", c.Path)
	case kindTimeMachine:
		return fmt.Sprintf("Time Machine share %q (%s)", c.Name, c.Path)
	case kindISCSI:
		return fmt.Sprintf("iSCSI target %s (zvol %s)", c.Name, c.Dataset)
	case kindReplSource:
		return fmt.Sprintf("replication task %q (source %s)", c.Name, c.Dataset)
	case kindReplTarget:
		return fmt.Sprintf("replication task %q (local target %s)", c.Name, c.Dataset)
	}
	return c.Kind + " " + c.Name
}

// ownsPath reports whether d is a filesystem mounted at an absolute path.
func ownsPath(d zfs.Dataset) bool {
	return d.Type == "filesystem" && strings.HasPrefix(d.Mountpoint, "/")
}

// inSubtree reports whether name is root or one of its descendants.
func inSubtree(name, root string) bool {
	return name == root || strings.HasPrefix(name, root+"/")
}

// underPath reports whether p is mp or below it, on component boundaries.
func underPath(p, mp string) bool {
	return p == mp || mp == "/" || strings.HasPrefix(p, mp+"/")
}

// inheritsFrom reports whether d's mountpoint is the value it would inherit
// from ancestor a — i.e. d moves when a's mountpoint moves.
func inheritsFrom(d, a zfs.Dataset) bool {
	if d.Name == a.Name {
		return true
	}
	return d.Mountpoint == path.Join(a.Mountpoint, strings.TrimPrefix(d.Name, a.Name+"/"))
}

// affectedSets computes the dataset names that disappear or change (names)
// and the path-owning datasets whose paths disappear or move (paths).
func affectedSets(s refSnapshot, op refOp, name string, recursive bool, newMountpoint string) (names, paths map[string]bool) {
	names, paths = map[string]bool{}, map[string]bool{}
	byName := make(map[string]zfs.Dataset, len(s.Datasets))
	for _, d := range s.Datasets {
		byName[d.Name] = d
	}
	target, ok := byName[name]

	switch op {
	case refDestroy:
		// Non-recursive destroy of a dataset with children fails in ZFS
		// anyway; reporting the children's references would mask that error.
		for _, d := range s.Datasets {
			if d.Name == name || (recursive && inSubtree(d.Name, name)) {
				names[d.Name] = true
				if ownsPath(d) {
					paths[d.Name] = true
				}
			}
		}

	case refRename:
		for _, d := range s.Datasets {
			if inSubtree(d.Name, name) {
				names[d.Name] = true
			}
		}
		// Datasets whose mountpoint is inherited from the parent move with
		// the new name; a local mountpoint stays put. An unknown parent
		// means we can't tell — treat the whole subtree as moving.
		parentName := name[:max(strings.LastIndex(name, "/"), 0)]
		parent, havePar := byName[parentName]
		for _, d := range s.Datasets {
			if !inSubtree(d.Name, name) || !ownsPath(d) {
				continue
			}
			if !havePar || (strings.HasPrefix(parent.Mountpoint, "/") && inheritsFrom(d, parent)) {
				paths[d.Name] = true
			}
		}

	case refMountpoint:
		if ok && path.Clean(newMountpoint) == path.Clean(target.Mountpoint) && newMountpoint != "" {
			return names, paths
		}
		if !ok || !ownsPath(target) {
			// No current path: nothing can reference it, except an unknown
			// dataset, whose subtree we treat as moving.
			if !ok {
				for _, d := range s.Datasets {
					if inSubtree(d.Name, name) && ownsPath(d) {
						paths[d.Name] = true
					}
				}
			}
			return names, paths
		}
		for _, d := range s.Datasets {
			if inSubtree(d.Name, name) && ownsPath(d) && inheritsFrom(d, target) {
				paths[d.Name] = true
			}
		}
	}
	return names, paths
}

// pathOwners returns the path-owning datasets with the longest mountpoint
// that contains p. Several datasets can share a mountpoint (e.g. "/"); all
// are returned, sorted.
func pathOwners(datasets []zfs.Dataset, p string) []string {
	var owners []string
	best := -1
	for _, d := range datasets {
		if !ownsPath(d) {
			continue
		}
		mp := path.Clean(d.Mountpoint)
		if !underPath(p, mp) {
			continue
		}
		switch {
		case len(mp) > best:
			best, owners = len(mp), []string{d.Name}
		case len(mp) == best:
			owners = append(owners, d.Name)
		}
	}
	sort.Strings(owners)
	return owners
}

// homesBase cuts a [homes] path at its first substitution component:
// "/tank/homes/%U" → "/tank/homes".
func homesBase(p string) string {
	var kept []string
	for _, c := range strings.Split(p, "/") {
		if strings.Contains(c, "%") {
			break
		}
		kept = append(kept, c)
	}
	return strings.Join(kept, "/")
}

// findConflicts returns the configuration that references datasets affected
// by op on name. It does no I/O; the result is sorted by kind, dataset, name.
func findConflicts(s refSnapshot, op refOp, name string, recursive bool, newMountpoint string) []refConflict {
	names, paths := affectedSets(s, op, name, recursive, newMountpoint)
	var out []refConflict

	// affectedOwner resolves a shared path to the first affected dataset
	// that owns it, or "".
	affectedOwner := func(p string) string {
		if !strings.HasPrefix(p, "/") {
			return ""
		}
		for _, o := range pathOwners(s.Datasets, path.Clean(p)) {
			if paths[o] {
				return o
			}
		}
		return ""
	}

	for _, u := range s.Usershares {
		if o := affectedOwner(u.Path); o != "" {
			out = append(out, refConflict{Kind: kindUsershare, Dataset: o, Name: u.Name, Path: u.Path})
		}
	}
	if s.HomesPath != "" {
		if o := affectedOwner(homesBase(s.HomesPath)); o != "" {
			out = append(out, refConflict{Kind: kindHomes, Dataset: o, Name: "homes", Path: s.HomesPath})
		}
	}
	for _, tm := range s.TimeMachine {
		if o := affectedOwner(tm.Path); o != "" {
			out = append(out, refConflict{Kind: kindTimeMachine, Dataset: o, Name: tm.Name, Path: tm.Path})
		}
	}
	for _, t := range s.Targets {
		if names[t.ZvolName] {
			out = append(out, refConflict{Kind: kindISCSI, Dataset: t.ZvolName, Name: t.IQN})
		}
	}
	for _, t := range s.Tasks {
		// A recursive task on an ancestor just stops sending the removed
		// child, so only exact sources block.
		if names[t.Source] {
			out = append(out, refConflict{Kind: kindReplSource, Dataset: t.Source, Name: t.Name, ID: t.ID})
		}
		if t.Remote == "" && names[t.Target] {
			out = append(out, refConflict{Kind: kindReplTarget, Dataset: t.Target, Name: t.Name, ID: t.ID})
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Kind != b.Kind {
			return kindOrder[a.Kind] < kindOrder[b.Kind]
		}
		if a.Dataset != b.Dataset {
			return a.Dataset < b.Dataset
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ID < b.ID
	})
	return out
}

// collectRefs gathers the reference snapshot. It fails closed: any read error
// aborts the check (and the operation). Absent config — no smb.conf, no
// usershare dir, no iSCSI backend — simply means no references.
func (h *Handler) collectRefs() (refSnapshot, error) {
	var s refSnapshot
	var err error
	if s.Datasets, err = zfs.ListDatasets(); err != nil {
		return s, fmt.Errorf("list datasets: %w", err)
	}
	if s.Usershares, err = smb.ListUsershares(smb.UsershareDir(runtime.GOOS)); err != nil {
		return s, err
	}
	cfg, err := smb.ParseSMBConfig(runtime.GOOS)
	if err != nil {
		return s, err
	}
	if cfg.Homes != nil {
		s.HomesPath = cfg.Homes.Path
	}
	s.TimeMachine = cfg.TimeMachine
	if s.Targets, err = iscsi.ListTargets(); err != nil {
		return s, err
	}
	s.Tasks = h.repl.Store().List()
	return s, nil
}

// checkDatasetRefs runs the reference guard for op on name. It writes the
// response and returns false when the caller must abort: 500 if references
// can't be read, 404 if the dataset doesn't exist, 409 on conflicts. The
// caller must hold h.refMu until its operation has finished.
func (h *Handler) checkDatasetRefs(w http.ResponseWriter, r *http.Request, op refOp, name string, recursive bool, newMountpoint string) bool {
	s, err := h.collectRefs()
	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, fmt.Errorf("reference check failed: %w", err), nil)
		return false
	}
	found := false
	for _, d := range s.Datasets {
		if d.Name == name {
			found = true
			break
		}
	}
	if !found {
		writeError(r.Context(), w, http.StatusNotFound, fmt.Errorf("dataset %q not found", name), nil)
		return false
	}
	if c := findConflicts(s, op, name, recursive, newMountpoint); len(c) > 0 {
		writeConflicts(r.Context(), w, op, name, c)
		return false
	}
	return true
}

// writeConflicts writes a 409 whose error text lists every conflict (the
// op-log renders it as-is) plus the structured list for API clients.
func writeConflicts(ctx context.Context, w http.ResponseWriter, op refOp, name string, conflicts []refConflict) {
	var b strings.Builder
	fmt.Fprintf(&b, "cannot %s %s: still in use by dumpstore-managed configuration:\n", op.verb(), name)
	for i := range conflicts {
		conflicts[i].Describe = conflicts[i].describe()
		b.WriteString("  - " + conflicts[i].Describe + "\n")
	}
	b.WriteString("Remove these references first, then retry.")
	writeAPIError(ctx, w, http.StatusConflict, apiError{Error: b.String(), Conflicts: conflicts})
}
