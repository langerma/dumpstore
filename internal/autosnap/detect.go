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
// OR any of the package's cron entry points existing, counts as active.
// FreeBSD: any of {hourly,daily,weekly,monthly}_zfs_snapshot_enable
// (zfs-periodic's periodic(8) switches) effectively YES in
// PeriodicConfFiles.
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
	// Debian/Ubuntu packaging uses cron rather than systemd. Takeover renames
	// these to <file>.dumpstore-disabled, so existence means active.
	for _, path := range CronFiles {
		if _, err := os.Stat(path); err == nil {
			s.OSDaemonActive = true
			return s
		}
	}
	return s
}

// CronFiles are the cron entry points of the cron-based zfs-auto-snapshot
// packaging: frequent in cron.d, the other buckets as run-parts scripts.
var CronFiles = []string{
	"/etc/cron.d/zfs-auto-snapshot",
	"/etc/cron.hourly/zfs-auto-snapshot",
	"/etc/cron.daily/zfs-auto-snapshot",
	"/etc/cron.weekly/zfs-auto-snapshot",
	"/etc/cron.monthly/zfs-auto-snapshot",
}

// PeriodicConfFiles are FreeBSD's periodic_conf_files, in the order
// periodic(8) sources them: a later file overrides an earlier one.
var PeriodicConfFiles = []string{
	"/etc/periodic.conf",
	"/etc/periodic.conf.local",
	"/usr/local/etc/periodic.conf",
}

// periodicSnapshotVars are the switches of the zfs-periodic package's
// hourly/daily/weekly/monthly periodic(8) snapshot scripts.
var periodicSnapshotVars = []string{
	"hourly_zfs_snapshot_enable",
	"daily_zfs_snapshot_enable",
	"weekly_zfs_snapshot_enable",
	"monthly_zfs_snapshot_enable",
}

func detectFreeBSD() Status {
	return Status{OSDaemon: "zfstools", OSDaemonActive: periodicSnapshotsEnabled(PeriodicConfFiles)}
}

// periodicSnapshotsEnabled reports whether any periodic snapshot switch is
// effectively YES after sourcing files in order. Missing files are skipped.
func periodicSnapshotsEnabled(files []string) bool {
	vals := map[string]string{}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		parsePeriodicConf(string(data), vals)
	}
	for _, v := range periodicSnapshotVars {
		if strings.EqualFold(vals[v], "YES") {
			return true
		}
	}
	return false
}

// parsePeriodicConf records the `name=value` assignments of one sh-style
// conf file into vals, later lines overriding earlier ones. Crude but
// sufficient: comment lines are skipped and one level of quotes stripped.
func parsePeriodicConf(data string, vals map[string]string) {
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		vals[strings.TrimSpace(name)] = strings.Trim(strings.TrimSpace(val), "\"'")
	}
}
