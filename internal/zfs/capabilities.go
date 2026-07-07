package zfs

import (
	"context"
	"os/exec"
	"strings"
	"sync"
)

// Capabilities reports which optional ZFS features the installed OpenZFS
// release supports. Detection probes the installed tools instead of
// comparing version strings, because distros backport features.
type Capabilities struct {
	// Rewrite is true when the zfs binary knows the `rewrite` subcommand
	// (OpenZFS >= 2.3 upstream).
	Rewrite bool `json:"rewrite"`
	// Draid is true when the draid pool feature is available
	// (OpenZFS >= 2.1 upstream).
	Draid bool `json:"draid"`
}

var (
	capsOnce sync.Once
	caps     Capabilities
)

// Caps returns the host's ZFS capabilities. The probes run once, on first
// call; both are read-only and complete in milliseconds.
func Caps() Capabilities {
	capsOnce.Do(func() {
		caps = Capabilities{
			Rewrite: rewriteRecognized(probeOutput("zfs", "rewrite")),
			Draid:   draidSupported(probeOutput("zpool", "upgrade", "-v")),
		}
	})
	return caps
}

// probeOutput runs a command purely for its combined output. Probes are
// expected to "fail" (usage errors); an empty string means the binary
// itself could not be run.
func probeOutput(name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil && len(out) == 0 {
		return ""
	}
	return string(out)
}

// rewriteRecognized reports whether `zfs rewrite` output shows the
// subcommand exists. A supporting zfs invoked without arguments prints a
// usage error mentioning rewrite; an older one prints
// "unrecognized command 'rewrite'"; an empty string means zfs is missing.
func rewriteRecognized(out string) bool {
	return out != "" && !strings.Contains(out, "unrecognized command")
}

// draidSupported reports whether the `zpool upgrade -v` feature list
// contains the draid feature (first word of a line, as the feature table
// is name-first).
func draidSupported(out string) bool {
	for line := range strings.SplitSeq(out, "\n") {
		if fields := strings.Fields(line); len(fields) > 0 && fields[0] == "draid" {
			return true
		}
	}
	return false
}
