package autosnap

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPeriodicSnapshotsEnabled(t *testing.T) {
	dir := t.TempDir()
	write := func(name, data string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	base := write("periodic.conf", "# local settings\ndaily_output=\"root\"\nweekly_zfs_snapshot_enable=\"YES\"\n")
	off := write("periodic.conf.local", "weekly_zfs_snapshot_enable=NO\n")
	hourly := write("local.conf", "  hourly_zfs_snapshot_enable='yes'\n")
	commented := write("commented.conf", "#daily_zfs_snapshot_enable=\"YES\"\n")
	missing := filepath.Join(dir, "missing.conf")

	for _, tc := range []struct {
		name  string
		files []string
		want  bool
	}{
		{"weekly YES", []string{base, missing}, true},
		{"overridden by a later file", []string{base, off}, false},
		{"hourly, lower case, single quotes", []string{base, off, hourly}, true},
		{"comment only", []string{commented}, false},
		{"no files", []string{missing}, false},
	} {
		if got := periodicSnapshotsEnabled(tc.files); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
