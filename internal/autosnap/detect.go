package autosnap

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Status describes who currently owns auto-snapshot execution on the host.
// Used both by the startup decision (register the scheduler tasks?) and by
// the UI banner.
type Status struct {
	OSDaemon          string `json:"os_daemon"`           // "zfs-auto-snapshot" | "zfstools" | ""
	OSDaemonActive    bool   `json:"os_daemon_active"`    // daemon is enabled and would run
	DumpstoreManaged  bool   `json:"dumpstore_managed"`   // dumpstore's scheduler tasks are registered
}

// DetectStatus inspects the host to determine whether the OS auto-snapshot
// daemon is currently active. It does NOT decide what's registered with the
// scheduler — that's the runner's job; DumpstoreManaged is populated by the
// handler from Runner.IsRegistered().
//
// Linux: any of the five `zfs-auto-snapshot-*.timer` units being enabled,
// OR `/etc/cron.d/zfs-auto-snapshot` existing, counts as active.
// FreeBSD: `daily_zfs_snapshot_enable="YES"` in periodic.conf{,.local}.
func DetectStatus() Status {
	switch runtime.GOOS {
	case "linux":
		return detectLinux()
	case "freebsd":
		return detectFreeBSD()
	default:
		return Status{}
	}
}

func detectLinux() Status {
	s := Status{OSDaemon: "zfs-auto-snapshot"}

	// systemd timers shipped with the zfs-auto-snapshot package.
	for _, unit := range []string{
		"zfs-auto-snapshot-frequent.timer",
		"zfs-auto-snapshot-hourly.timer",
		"zfs-auto-snapshot-daily.timer",
		"zfs-auto-snapshot-weekly.timer",
		"zfs-auto-snapshot-monthly.timer",
	} {
		// `is-enabled` returns 0 for enabled (incl. static/alias), non-zero
		// otherwise. We only care about explicitly enabled timers.
		cmd := exec.Command("systemctl", "is-enabled", unit)
		out, _ := cmd.Output()
		if strings.TrimSpace(string(out)) == "enabled" {
			s.OSDaemonActive = true
			return s
		}
	}
	// Older packaging uses cron rather than systemd.
	if _, err := os.Stat("/etc/cron.d/zfs-auto-snapshot"); err == nil {
		s.OSDaemonActive = true
	}
	return s
}

func detectFreeBSD() Status {
	s := Status{OSDaemon: "zfstools"}
	for _, path := range []string{"/etc/periodic.conf", "/etc/periodic.conf.local"} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		// Match `daily_zfs_snapshot_enable="YES"` (with or without quotes,
		// any indentation). Comment lines (#) are skipped.
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if !strings.HasPrefix(line, "daily_zfs_snapshot_enable") {
				continue
			}
			// Crude but sufficient: look for YES in the value.
			eq := strings.Index(line, "=")
			if eq < 0 {
				continue
			}
			val := strings.Trim(strings.TrimSpace(line[eq+1:]), "\"'")
			if strings.EqualFold(val, "YES") {
				s.OSDaemonActive = true
				return s
			}
		}
	}
	return s
}
