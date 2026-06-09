// Package blockdev enumerates physical block devices for use as ZFS vdev
// candidates (disk replacement, pool creation, pool expansion).
package blockdev

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Device is one physical block device.
type Device struct {
	Name      string `json:"name"`       // short name, e.g. "sdb" / "ada1"
	Path      string `json:"path"`       // device node, e.g. "/dev/sdb"
	SizeBytes uint64 `json:"size_bytes"` // capacity in bytes
	Model     string `json:"model"`      // hardware model string, if known
	InUseBy   string `json:"in_use_by"`  // pool name when the device backs a vdev, "" otherwise
}

// List returns the physical block devices visible on this host.
func List(goos string) ([]Device, error) {
	if goos == "freebsd" {
		out, err := run("geom", "disk", "list")
		if err != nil {
			return nil, err
		}
		return parseGeomDiskList(out), nil
	}
	return listLinux()
}

// skipLinuxDev matches virtual/pseudo block devices that can never back a vdev:
// loopbacks, ramdisks, zvols, device-mapper nodes, optical drives, floppies,
// network block devices and compressed-RAM devices.
var skipLinuxDev = regexp.MustCompile(`^(loop|ram|zd|dm-|sr|fd|nbd|zram)`)

// listLinux enumerates whole-disk devices from /sys/block.
func listLinux() ([]Device, error) {
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return nil, fmt.Errorf("reading /sys/block: %w", err)
	}
	devs := make([]Device, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if skipLinuxDev.MatchString(name) {
			continue
		}
		dev := Device{Name: name, Path: "/dev/" + name}
		// size is reported in 512-byte sectors regardless of the device's
		// logical block size
		if b, err := os.ReadFile(filepath.Join("/sys/block", name, "size")); err == nil {
			if sectors, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64); err == nil {
				dev.SizeBytes = sectors * 512
			}
		}
		if b, err := os.ReadFile(filepath.Join("/sys/block", name, "device", "model")); err == nil {
			dev.Model = strings.TrimSpace(string(b))
		}
		devs = append(devs, dev)
	}
	return devs, nil
}

// parseGeomDiskList parses `geom disk list` output into devices.
func parseGeomDiskList(out string) []Device {
	devs := make([]Device, 0)
	var cur *Device
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "Geom name: "):
			if cur != nil {
				devs = append(devs, *cur)
			}
			name := strings.TrimPrefix(trimmed, "Geom name: ")
			cur = &Device{Name: name, Path: "/dev/" + name}
		case cur == nil:
			continue
		case strings.HasPrefix(trimmed, "Mediasize: "):
			// "Mediasize: 21474836480 (20G)"
			f := strings.Fields(strings.TrimPrefix(trimmed, "Mediasize: "))
			if len(f) > 0 {
				if v, err := strconv.ParseUint(f[0], 10, 64); err == nil {
					cur.SizeBytes = v
				}
			}
		case strings.HasPrefix(trimmed, "descr: "):
			cur.Model = strings.TrimPrefix(trimmed, "descr: ")
		}
	}
	if cur != nil {
		devs = append(devs, *cur)
	}
	return devs
}

// MarkInUse fills InUseBy on each device by matching pool vdev names against
// the device names. vdevByPool maps a vdev name (as printed by `zpool status`)
// to the pool that contains it. Matching is best-effort: vdev names that are
// symlinks under /dev/disk/* (by-id, by-path, …) are resolved to their target
// device, and partition suffixes are stripped (sdb2 → sdb, nvme0n1p1 → nvme0n1,
// ada0p3 → ada0).
func MarkInUse(devs []Device, vdevByPool map[string]string) {
	resolved := make(map[string]string, len(vdevByPool))
	for vdev, pool := range vdevByPool {
		resolved[resolveVdevName(vdev)] = pool
	}
	for i := range devs {
		for vdev, pool := range resolved {
			if vdevMatchesDevice(vdev, devs[i].Name) {
				devs[i].InUseBy = pool
				break
			}
		}
	}
}

// resolveVdevName turns a vdev name from `zpool status` into a short device
// name where possible. zpool prints short names (sdb1, ata-FOO-part1, gpt/data)
// relative to the import search path; absolute paths appear when a pool was
// created with explicit paths.
func resolveVdevName(name string) string {
	if strings.HasPrefix(name, "/") {
		if target, err := filepath.EvalSymlinks(name); err == nil {
			return filepath.Base(target)
		}
		return filepath.Base(name)
	}
	for _, dir := range []string{"/dev/disk/by-id", "/dev/disk/by-path", "/dev/disk/by-partlabel", "/dev/gpt", "/dev"} {
		p := filepath.Join(dir, name)
		if target, err := filepath.EvalSymlinks(p); err == nil && target != p {
			return filepath.Base(target)
		}
	}
	return filepath.Base(name)
}

// rePartSuffix matches a trailing partition suffix: "2" after a letter (sdb2),
// or "p2" after a digit (nvme0n1p2, ada0p3).
var rePartSuffix = regexp.MustCompile(`^p?[0-9]+$`)

// vdevMatchesDevice reports whether a resolved vdev name refers to the whole
// disk dev or one of its partitions.
func vdevMatchesDevice(vdev, dev string) bool {
	if vdev == dev {
		return true
	}
	if rest, ok := strings.CutPrefix(vdev, dev); ok {
		return rePartSuffix.MatchString(rest)
	}
	return false
}

// run executes a command with a timeout and returns its stdout.
func run(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String(), nil
}
