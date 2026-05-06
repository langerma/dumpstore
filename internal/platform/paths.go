// Package platform provides OS-specific path helpers for dumpstore.
package platform

// ConfigDir returns the root directory for dumpstore configuration files.
// On FreeBSD, third-party software config belongs under /usr/local/etc.
// On Linux, it belongs under /etc.
func ConfigDir(goos string) string {
	if goos == "freebsd" {
		return "/usr/local/etc/dumpstore"
	}
	return "/etc/dumpstore"
}

// StateDir returns the root directory for dumpstore mutable runtime state
// (job records, scheduled-job persistence, cached state).
// On FreeBSD, third-party variable data belongs under /var/db.
// On Linux, it belongs under /var/lib.
func StateDir(goos string) string {
	if goos == "freebsd" {
		return "/var/db/dumpstore"
	}
	return "/var/lib/dumpstore"
}
