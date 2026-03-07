// Package zfs executes zpool/zfs CLI commands directly for low-latency reads.
package zfs

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Pool represents a ZFS storage pool.
type Pool struct {
	Name        string  `json:"name"`
	Size        uint64  `json:"size"`
	Alloc       uint64  `json:"alloc"`
	Free        uint64  `json:"free"`
	Health      string  `json:"health"`
	Frag        string  `json:"frag"`
	Cap         string  `json:"cap"`
	Dedup       string  `json:"dedup"`
	UsedPercent float64 `json:"used_percent"`
}

// Dataset represents a ZFS filesystem or volume.
type Dataset struct {
	Name          string `json:"name"`
	Used          uint64 `json:"used"`
	Avail         uint64 `json:"avail"`
	Refer         uint64 `json:"refer"`
	Mountpoint    string `json:"mountpoint"`
	Type          string `json:"type"`
	CompressRatio string `json:"compress_ratio"`
	Compression   string `json:"compression"`
	Quota         uint64 `json:"quota"`
	Reservation   uint64 `json:"reservation"`
	Pool          string `json:"pool"`
	Depth         int    `json:"depth"`
	ShareNFS      string `json:"sharenfs"`
	ShareSMB      string `json:"sharesmb"`
	ACLType       string `json:"acltype"`
}

// Snapshot represents a ZFS snapshot.
type Snapshot struct {
	Name      string `json:"name"`
	Dataset   string `json:"dataset"`
	SnapLabel string `json:"snap_label"`
	Used      uint64 `json:"used"`
	Refer     uint64 `json:"refer"`
	Creation  int64  `json:"creation"`
	Clones    string `json:"clones"`
}

// VdevEntry is one node in the vdev tree returned by zpool status.
type VdevEntry struct {
	Name  string `json:"name"`
	State string `json:"state"`
	Read  uint64 `json:"read"`
	Write uint64 `json:"write"`
	Cksum uint64 `json:"cksum"`
	Depth int    `json:"depth"`
}

// PoolDetail holds the parsed output of `zpool status` for one pool.
type PoolDetail struct {
	Name   string      `json:"name"`
	State  string      `json:"state"`
	Status string      `json:"status,omitempty"` // advisory message (degraded, feature warnings, …)
	Scan   string      `json:"scan"`
	Errors string      `json:"errors"`
	Vdevs  []VdevEntry `json:"vdevs"`
}

// IOStat holds I/O statistics for one pool.
type IOStat struct {
	Pool     string  `json:"pool"`
	ReadOps  float64 `json:"read_ops"`
	WriteOps float64 `json:"write_ops"`
	ReadBW   float64 `json:"read_bw"`
	WriteBW  float64 `json:"write_bw"`
}

// ListPools runs `zpool list` and returns all pools.
func ListPools() ([]Pool, error) {
	out, err := run("zpool", "list", "-H", "-p", "-o", "name,size,alloc,free,health,frag,cap,dedup")
	if err != nil {
		return nil, err
	}
	var pools []Pool
	for _, line := range splitLines(out) {
		f := strings.Split(line, "\t")
		if len(f) < 8 {
			continue
		}
		alloc := parseUint(f[2])
		size := parseUint(f[1])
		usedPct := 0.0
		if size > 0 {
			usedPct = float64(alloc) / float64(size) * 100
		}
		pools = append(pools, Pool{
			Name:        f[0],
			Size:        size,
			Alloc:       alloc,
			Free:        parseUint(f[3]),
			Health:      f[4],
			Frag:        f[5],
			Cap:         f[6],
			Dedup:       f[7],
			UsedPercent: usedPct,
		})
	}
	return pools, nil
}

// ListDatasets runs `zfs list` and returns all datasets.
func ListDatasets() ([]Dataset, error) {
	out, err := run("zfs", "list", "-H", "-p",
		"-o", "name,used,avail,refer,mountpoint,type,compressratio,compression,quota,reservation,sharenfs,sharesmb,acltype")
	if err != nil {
		return nil, err
	}
	var datasets []Dataset
	for _, line := range splitLines(out) {
		f := strings.Split(line, "\t")
		if len(f) < 13 {
			continue
		}
		name := f[0]
		pool := strings.SplitN(name, "/", 2)[0]
		datasets = append(datasets, Dataset{
			Name:          name,
			Used:          parseUint(f[1]),
			Avail:         parseUint(f[2]),
			Refer:         parseUint(f[3]),
			Mountpoint:    f[4],
			Type:          f[5],
			CompressRatio: f[6],
			Compression:   f[7],
			Quota:         parseUint(f[8]),
			Reservation:   parseUint(f[9]),
			Pool:          pool,
			Depth:         strings.Count(name, "/"),
			ShareNFS:      f[10],
			ShareSMB:      f[11],
			ACLType:       normalizeACLType(f[12]),
		})
	}
	return datasets, nil
}

// ListSnapshots runs `zfs list -t snapshot` and returns all snapshots.
func ListSnapshots() ([]Snapshot, error) {
	out, err := run("zfs", "list", "-H", "-p", "-t", "snapshot",
		"-o", "name,used,refer,creation,clones")
	if err != nil {
		return nil, err
	}
	var snaps []Snapshot
	for _, line := range splitLines(out) {
		f := strings.Split(line, "\t")
		if len(f) < 5 {
			continue
		}
		name := f[0]
		parts := strings.SplitN(name, "@", 2)
		dataset, label := name, ""
		if len(parts) == 2 {
			dataset, label = parts[0], parts[1]
		}
		snaps = append(snaps, Snapshot{
			Name:      name,
			Dataset:   dataset,
			SnapLabel: label,
			Used:      parseUint(f[1]),
			Refer:     parseUint(f[2]),
			Creation:  parseInt64(f[3]),
			Clones:    f[4],
		})
	}
	return snaps, nil
}

// IOStats runs `zpool iostat` and returns one per-interval sample per pool.
//
// Column layout (7 fields with -H -p):
//
//	[0] pool  [1] alloc  [2] free  [3] read_ops  [4] write_ops  [5] read_bw  [6] write_bw
//
// -p gives exact byte values instead of human-readable suffixes.
// -y skips the cumulative-since-boot first report so we see current activity.
// Child device lines (mirrors, disks) have "-" for alloc/free and are skipped.
func IOStats() ([]IOStat, error) {
	out, err := run("zpool", "iostat", "-H", "-p", "-y", "1", "1")
	if err != nil {
		return nil, err
	}
	var stats []IOStat
	for _, line := range splitLines(out) {
		f := strings.Fields(line)
		if len(f) < 7 || f[1] == "-" {
			continue
		}
		stats = append(stats, IOStat{
			Pool:     f[0],
			ReadOps:  parseFloat(f[3]),
			WriteOps: parseFloat(f[4]),
			ReadBW:   parseFloat(f[5]),
			WriteBW:  parseFloat(f[6]),
		})
	}
	return stats, nil
}

// DatasetProp holds the value and source of a ZFS property.
type DatasetProp struct {
	Value  string `json:"value"`
	Source string `json:"source"` // "local", "default", "inherited from <parent>", …
}

// GetDatasetProps runs `zfs get` for the editable properties of a dataset and
// returns a map of property name → DatasetProp.
func GetDatasetProps(name string) (map[string]DatasetProp, error) {
	out, err := run("zfs", "get", "-H",
		"compression,quota,mountpoint,recordsize,atime,exec,sync,dedup,copies,xattr,readonly,acltype,sharenfs,sharesmb",
		name)
	if err != nil {
		return nil, err
	}
	result := make(map[string]DatasetProp)
	for _, line := range splitLines(out) {
		f := strings.SplitN(line, "\t", 4)
		if len(f) < 4 {
			continue
		}
		// f[0]=dataset  f[1]=property  f[2]=value  f[3]=source
		result[f[1]] = DatasetProp{Value: f[2], Source: strings.TrimSpace(f[3])}
	}
	return result, nil
}

// GetMountpointOwnership returns the owner username and group name of a
// mountpoint directory by running `stat --format=%U %G <path>`.
// This is Linux-specific and matches the target platform for the service.
func GetMountpointOwnership(mountpoint string) (owner, group string, err error) {
	out, err := run("stat", "--format=%U %G", mountpoint)
	if err != nil {
		return "", "", fmt.Errorf("stat %s: %w", mountpoint, err)
	}
	parts := strings.Fields(strings.TrimSpace(out))
	if len(parts) < 2 {
		return "", "", fmt.Errorf("unexpected stat output: %q", out)
	}
	return parts[0], parts[1], nil
}

// Version returns the OpenZFS version string reported by `zpool version`
// (e.g. "zfs-2.2.3-1"). Returns an empty string if the command is unavailable.
func Version() (string, error) {
	out, err := run("zpool", "version")
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) > 0 {
		return strings.TrimSpace(lines[0]), nil
	}
	return "", nil
}

// PoolStatuses runs `zpool status` and returns parsed details for every pool.
func PoolStatuses() ([]PoolDetail, error) {
	out, err := run("zpool", "status")
	if err != nil {
		return nil, err
	}
	return parsePoolStatuses(out), nil
}

func parsePoolStatuses(out string) []PoolDetail {
	var pools []PoolDetail
	var cur *PoolDetail
	inConfig := false

	for _, line := range strings.Split(out, "\n") {
		// ── New pool entry ───────────────────────────────────────────────
		if strings.HasPrefix(line, "  pool: ") {
			if cur != nil {
				pools = append(pools, *cur)
			}
			cur = &PoolDetail{Name: strings.TrimSpace(strings.TrimPrefix(line, "  pool:"))}
			inConfig = false
			continue
		}
		if cur == nil {
			continue
		}

		// ── Top-level keyword lines ──────────────────────────────────────
		if strings.HasPrefix(line, " state: ") {
			cur.State = strings.TrimSpace(strings.TrimPrefix(line, " state:"))
			continue
		}
		if strings.HasPrefix(line, "status: ") {
			cur.Status = strings.TrimSpace(strings.TrimPrefix(line, "status:"))
			continue
		}
		if strings.HasPrefix(line, "  scan: ") {
			cur.Scan = strings.TrimSpace(strings.TrimPrefix(line, "  scan:"))
			continue
		}
		if strings.HasPrefix(line, "errors: ") {
			cur.Errors = strings.TrimSpace(strings.TrimPrefix(line, "errors:"))
			continue
		}
		if strings.TrimSpace(line) == "config:" {
			inConfig = true
			continue
		}

		if !inConfig {
			continue
		}

		// ── Inside config: section ───────────────────────────────────────
		// Lines in this section start with a hard tab; anything else ends it.
		if !strings.HasPrefix(line, "\t") {
			if strings.TrimSpace(line) != "" {
				inConfig = false
			}
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Skip the column header
		if strings.HasPrefix(trimmed, "NAME") && strings.Contains(trimmed, "STATE") {
			continue
		}

		rest := line[1:] // strip the leading tab
		spaces := 0
		for _, c := range rest {
			if c == ' ' {
				spaces++
			} else {
				break
			}
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		entry := VdevEntry{
			Name:  fields[0],
			Depth: spaces / 2,
		}
		if len(fields) >= 2 && fields[1] != "-" {
			entry.State = fields[1]
		}
		if len(fields) >= 5 {
			entry.Read = parseUint(fields[2])
			entry.Write = parseUint(fields[3])
			entry.Cksum = parseUint(fields[4])
		}
		cur.Vdevs = append(cur.Vdevs, entry)
	}
	if cur != nil {
		pools = append(pools, *cur)
	}
	return pools
}

// run executes a command and returns its stdout as a string.
func run(name string, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String(), nil
}

func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func parseUint(s string) uint64 {
	s = strings.TrimSpace(s)
	if s == "-" || s == "" || s == "none" {
		return 0
	}
	v, _ := strconv.ParseUint(s, 10, 64)
	return v
}

func parseInt64(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "-" || s == "" {
		return 0
	}
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

func parseFloat(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "-" || s == "" {
		return 0
	}
	v, _ := strconv.ParseFloat(s, 64)
	return v
}
