package replication

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// snapName returns the source snapshot label a run of task taskID should
// create: dumpstore-repl-<task ID>-<UTC YYYY-MM-DD_HH-MM-SS>, readable like
// TrueNAS's auto-YYYY-MM-DD_HH-MM and grouped per task when sorted by name.
// The task ID makes the snapshot attributable, so pruneSource deletes only
// its own task's snapshots (#221): several tasks may replicate the same
// source, and a recursive task's `zfs snapshot -r` also lands on child
// datasets that other tasks replicate. It also keeps two tasks firing in the
// same second from colliding on one name.
func snapName(now time.Time, taskID string) string {
	return ownPrefix(taskID) + now.UTC().Format("2006-01-02_15-04-05")
}

// ownPrefix is the label prefix of every snapshot task taskID creates.
func ownPrefix(taskID string) string { return SnapshotPrefix + taskID + "-" }

// createSnapshot runs `zfs snapshot [-r] dataset@label`. The atomic recursive
// form lets us snapshot a parent + children in one operation when Recursive
// is set on the task.
func createSnapshot(dataset, label string, recursive bool) error {
	args := []string{"snapshot"}
	if recursive {
		args = append(args, "-r")
	}
	args = append(args, dataset+"@"+label)
	return runCmd("zfs", args...)
}

// holdSnapshot runs `zfs hold [-r] tag dataset@label`. The hold prevents
// destruction by any other tool (auto-snapshot pruners, ad-hoc admin
// `zfs destroy`) for as long as the tag is in place.
func holdSnapshot(tag, snap string, recursive bool) error {
	args := []string{"hold"}
	if recursive {
		args = append(args, "-r")
	}
	args = append(args, tag, snap)
	return runCmd("zfs", args...)
}

// releaseSnapshot drops a hold previously placed by holdSnapshot.
// Missing-tag errors are treated as success — replication should be tolerant
// of being asked to release a hold it didn't end up placing.
func releaseSnapshot(tag, snap string, recursive bool) error {
	args := []string{"release"}
	if recursive {
		args = append(args, "-r")
	}
	args = append(args, tag, snap)
	if err := runCmd("zfs", args...); err != nil {
		// `zfs release` exits non-zero if the tag is absent. Don't fail the
		// run for a benign release — log via the returned error string.
		if strings.Contains(err.Error(), "no such tag") {
			return nil
		}
		return err
	}
	return nil
}

// listSourceSnaps returns the names of dumpstore-repl-* snapshots on dataset
// (local), oldest first. Used for incremental selection and for source-side
// pruning of orphans from prior failed runs.
func listSourceSnaps(dataset string) ([]string, error) {
	out, err := runOut("zfs", "list", "-H", "-t", "snapshot", "-o", "name", "-s", "creation", "-r", dataset)
	if err != nil {
		return nil, err
	}
	return filterReplSnaps(out, dataset), nil
}

// listTargetSnaps returns the names of dumpstore-repl-* snapshots on the
// destination, oldest first. When remote is set, the listing runs over SSH.
//
// Returns an empty list (no error) if the target dataset does not yet exist
// on the destination — the initial replication run is the one that creates it.
func listTargetSnaps(target, remote string) ([]string, error) {
	args := []string{"list", "-H", "-t", "snapshot", "-o", "name", "-s", "creation", "-r", target}
	var (
		out []byte
		err error
	)
	if remote != "" {
		out, err = runOut("ssh", append([]string{"-o", "BatchMode=yes", remote, "zfs"}, args...)...)
	} else {
		out, err = runOut("zfs", args...)
	}
	if err != nil {
		// `zfs list` returns non-zero with "dataset does not exist" for an
		// uninitialised destination. Surface that as an empty list so a full
		// send proceeds. Any other failure is a real error.
		if strings.Contains(err.Error(), "does not exist") {
			return nil, nil
		}
		return nil, err
	}
	return filterReplSnaps(out, target), nil
}

// filterReplSnaps reads `zfs list` output and returns only snapshots that
// belong to `<dataset>` (exact dataset match — recursive listings include
// children, which we deliberately ignore here; recursive replication's
// child snapshots are created by `zfs snapshot -r` but compared by parent).
func filterReplSnaps(out []byte, dataset string) []string {
	var names []string
	prefix := dataset + "@" + SnapshotPrefix
	for _, line := range bytes.Split(bytes.TrimSpace(out), []byte("\n")) {
		s := string(line)
		if s == "" {
			continue
		}
		if strings.HasPrefix(s, prefix) {
			names = append(names, s)
		}
	}
	return names
}

// findLastCommon returns the most recent dumpstore-repl-* snapshot present
// on BOTH source and target, or "" if there is no overlap. It compares by
// snapshot label (the part after '@'), then returns the qualified source name
// suitable for `zfs send -i`.
func findLastCommon(source, target, remote string) (string, error) {
	src, err := listSourceSnaps(source)
	if err != nil {
		return "", fmt.Errorf("list source snapshots: %w", err)
	}
	dst, err := listTargetSnaps(target, remote)
	if err != nil {
		return "", fmt.Errorf("list target snapshots: %w", err)
	}
	if len(src) == 0 || len(dst) == 0 {
		return "", nil
	}
	have := make(map[string]bool, len(dst))
	for _, name := range dst {
		if i := strings.IndexByte(name, '@'); i >= 0 {
			have[name[i+1:]] = true
		}
	}
	// Walk source newest-first; first match wins.
	for i := len(src) - 1; i >= 0; i-- {
		if j := strings.IndexByte(src[i], '@'); j >= 0 && have[src[i][j+1:]] {
			return src[i], nil
		}
	}
	return "", nil
}

// pruneTarget destroys the oldest dumpstore-repl-* snapshots on target so that
// at most retain remain. Returns the list of destroyed snapshot names (full
// `<target>@<label>`) and the first error encountered.
func pruneTarget(target, remote string, retain int) ([]string, error) {
	all, err := listTargetSnaps(target, remote)
	if err != nil {
		return nil, err
	}
	if retain < 0 {
		retain = 0
	}
	if len(all) <= retain {
		return nil, nil
	}
	excess := all[:len(all)-retain]
	sort.Strings(excess) // already sorted by creation, but be explicit
	destroyed := make([]string, 0, len(excess))
	for _, snap := range excess {
		if err := destroyRemoteOrLocal(snap, remote); err != nil {
			return destroyed, fmt.Errorf("destroy %s: %w", snap, err)
		}
		destroyed = append(destroyed, snap)
	}
	return destroyed, nil
}

// pruneSource destroys the oldest of task taskID's own snapshots on the local
// source (labels starting with dumpstore-repl-<taskID>-, see snapName) so that at most retain of
// them remain (#221). keep — the snapshot the run just sent, the incremental
// base for the next run — is never destroyed, and retain is at least 1.
// Snapshots of other tasks and old-format labels without a task ID are left
// alone. Recursive tasks destroy with -r, matching their `zfs snapshot -r`.
// A snapshot that can't be destroyed (held by a run still in flight, cloned)
// is skipped; the errors are joined and the rest are still pruned. Must only
// be called after a successful run, when keep is the newest own snapshot.
func pruneSource(source, taskID string, recursive bool, retain int, keep string) ([]string, error) {
	all, err := listSourceSnaps(source)
	if err != nil {
		return nil, err
	}
	prefix := source + "@" + ownPrefix(taskID)
	var own []string
	for _, s := range all {
		if strings.HasPrefix(s, prefix) {
			own = append(own, s)
		}
	}
	retain = max(retain, 1)
	if len(own) <= retain {
		return nil, nil
	}
	var (
		destroyed []string
		errs      []error
	)
	for _, snap := range own[:len(own)-retain] { // oldest first
		if snap == keep {
			continue
		}
		args := []string{"destroy"}
		if recursive {
			args = append(args, "-r")
		}
		if err := runCmd("zfs", append(args, snap)...); err != nil {
			errs = append(errs, fmt.Errorf("destroy %s: %w", snap, err))
			continue
		}
		destroyed = append(destroyed, snap)
	}
	return destroyed, errors.Join(errs...)
}

func destroyRemoteOrLocal(snap, remote string) error {
	if remote != "" {
		return runCmd("ssh", "-o", "BatchMode=yes", remote, "zfs", "destroy", snap)
	}
	return runCmd("zfs", "destroy", snap)
}

// runCmd and runOut run zfs/ssh for the package. They are variables so unit
// tests can record commands and return canned listings instead.
var (
	runCmd = execCmd
	runOut = execOut
)

// execCmd runs a command and returns an error containing stderr on failure.
func execCmd(name string, args ...string) error {
	var stderr bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// execOut runs a command and returns stdout on success.
func execOut(name string, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
