// Package system collects host and process information without external dependencies.
package system

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var startTime = time.Now()

// User represents a local user from /etc/passwd.
type User struct {
	Username string `json:"username"`
	UID      int    `json:"uid"`
	GID      int    `json:"gid"`
	Home     string `json:"home"`
	Shell    string `json:"shell"`
}

// Group represents a local group from /etc/group.
type Group struct {
	Name    string   `json:"name"`
	GID     int      `json:"gid"`
	Members []string `json:"members"`
}

// ListUsers parses /etc/passwd and returns all local users.
func ListUsers() ([]User, error) {
	data, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return nil, err
	}
	users := make([]User, 0)
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || line[0] == '#' {
			continue
		}
		f := strings.SplitN(line, ":", 7)
		if len(f) < 7 {
			continue
		}
		uid, _ := strconv.Atoi(f[2])
		gid, _ := strconv.Atoi(f[3])
		users = append(users, User{
			Username: f[0],
			UID:      uid,
			GID:      gid,
			Home:     f[5],
			Shell:    strings.TrimSpace(f[6]),
		})
	}
	return users, nil
}

// ListGroups parses /etc/group and returns all local groups.
func ListGroups() ([]Group, error) {
	data, err := os.ReadFile("/etc/group")
	if err != nil {
		return nil, err
	}
	groups := make([]Group, 0)
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || line[0] == '#' {
			continue
		}
		f := strings.SplitN(line, ":", 4)
		if len(f) < 4 {
			continue
		}
		gid, _ := strconv.Atoi(f[2])
		members := make([]string, 0)
		if f[3] != "" {
			for _, m := range strings.Split(strings.TrimSpace(f[3]), ",") {
				if m != "" {
					members = append(members, m)
				}
			}
		}
		groups = append(groups, Group{Name: f[0], GID: gid, Members: members})
	}
	return groups, nil
}

// SMBShare represents a single entry from `net usershare info`.
type SMBShare struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// ListSMBUsershares runs `net usershare info *` and returns all registered
// usershares. Returns an empty slice when `net` is not in PATH.
func ListSMBUsershares() ([]SMBShare, error) {
	if _, err := exec.LookPath("net"); err != nil {
		return []SMBShare{}, nil
	}
	var out bytes.Buffer
	cmd := exec.Command("net", "usershare", "info", "*")
	cmd.Stdout = &out
	cmd.Run() // non-zero when no shares exist — that is fine

	var shares []SMBShare
	var cur *SMBShare
	for _, line := range strings.Split(out.String(), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			if cur != nil {
				shares = append(shares, *cur)
			}
			cur = &SMBShare{Name: line[1 : len(line)-1]}
		} else if cur != nil && strings.HasPrefix(line, "path=") {
			cur.Path = strings.TrimPrefix(line, "path=")
		}
	}
	if cur != nil {
		shares = append(shares, *cur)
	}
	return shares, nil
}

// ErrSambaNotAvailable is returned by ListSambaUsers when pdbedit is not in PATH.
var ErrSambaNotAvailable = fmt.Errorf("pdbedit not found in PATH — is Samba installed?")

// ListSambaUsers runs pdbedit -L and returns the usernames registered in the
// Samba tdbsam database. Returns ErrSambaNotAvailable when pdbedit is absent.
func ListSambaUsers() ([]string, error) {
	if _, err := exec.LookPath("pdbedit"); err != nil {
		return nil, ErrSambaNotAvailable
	}
	var out bytes.Buffer
	cmd := exec.Command("pdbedit", "-L")
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	var users []string
	for _, line := range strings.Split(out.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// pdbedit -L format: "username:uid:Full Name"
		parts := strings.SplitN(line, ":", 2)
		if len(parts) >= 1 && parts[0] != "" {
			users = append(users, parts[0])
		}
	}
	return users, nil
}

// SMBHomesConfig describes the current state of the [homes] section in smb.conf.
type SMBHomesConfig struct {
	Enabled       bool   `json:"enabled"`
	Path          string `json:"path"`
	Browseable    string `json:"browseable"`
	ReadOnly      string `json:"read_only"`
	CreateMask    string `json:"create_mask"`
	DirectoryMask string `json:"directory_mask"`
}

// smbConfPath returns the platform-specific path to smb.conf.
func smbConfPath() string {
	if runtime.GOOS == "freebsd" {
		return "/usr/local/etc/smb4.conf"
	}
	return "/etc/samba/smb.conf"
}

// ParseSMBHomes reads smb.conf and returns the current [homes] configuration.
// Returns Enabled=false if smb.conf is missing or has no [homes] section.
func ParseSMBHomes() SMBHomesConfig {
	cfg := SMBHomesConfig{
		Browseable:    "no",
		ReadOnly:      "no",
		CreateMask:    "0644",
		DirectoryMask: "0755",
	}
	data, err := os.ReadFile(smbConfPath())
	if err != nil {
		return cfg
	}
	lines := strings.Split(string(data), "\n")
	inHomes := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.EqualFold(trimmed, "[homes]") {
			inHomes = true
			cfg.Enabled = true
			continue
		}
		if inHomes && strings.HasPrefix(trimmed, "[") {
			break // next section
		}
		if !inHomes {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		switch strings.ToLower(key) {
		case "path":
			cfg.Path = val
		case "browseable", "browsable":
			cfg.Browseable = val
		case "read only":
			cfg.ReadOnly = val
		case "create mask", "create mode":
			cfg.CreateMask = val
		case "directory mask", "directory mode":
			cfg.DirectoryMask = val
		}
	}
	return cfg
}

// TimeMachineShare describes a single Time Machine backup share in smb.conf.
type TimeMachineShare struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	MaxSize    string `json:"max_size"`
	ValidUsers string `json:"valid_users"`
}

// ParseTimeMachineShares reads smb.conf and returns all shares configured
// as Time Machine targets (sections containing "fruit:time machine = yes").
func ParseTimeMachineShares() []TimeMachineShare {
	data, err := os.ReadFile(smbConfPath())
	if err != nil {
		return []TimeMachineShare{}
	}

	var result []TimeMachineShare
	var cur *TimeMachineShare
	isTM := false

	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if cur != nil && isTM {
				result = append(result, *cur)
			}
			name := trimmed[1 : len(trimmed)-1]
			if strings.EqualFold(name, "global") || strings.EqualFold(name, "homes") || strings.EqualFold(name, "printers") {
				cur = nil
				isTM = false
				continue
			}
			cur = &TimeMachineShare{Name: name}
			isTM = false
			continue
		}
		if cur == nil {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		switch strings.ToLower(key) {
		case "path":
			cur.Path = val
		case "fruit:time machine":
			if strings.EqualFold(val, "yes") {
				isTM = true
			}
		case "fruit:time machine max size":
			cur.MaxSize = val
		case "valid users":
			cur.ValidUsers = val
		}
	}
	if cur != nil && isTM {
		result = append(result, *cur)
	}
	return result
}

// ListAuthorizedKeys reads ~/.ssh/authorized_keys for the given home directory.
// Returns one entry per non-blank, non-comment line.
// Returns an empty slice when the file does not exist.
func ListAuthorizedKeys(home string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys"))
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var keys []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		keys = append(keys, line)
	}
	return keys, nil
}

// ListShells reads /etc/shells and returns all valid login shells.
// Falls back to a minimal list if the file is not present.
func ListShells() []string {
	data, err := os.ReadFile("/etc/shells")
	if err != nil {
		return []string{"/bin/bash", "/bin/sh", "/sbin/nologin", "/usr/sbin/nologin"}
	}
	var shells []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		shells = append(shells, line)
	}
	return shells
}

// UIDMin returns the minimum UID for regular users from /etc/login.defs (default 1000).
func UIDMin() int {
	data, err := os.ReadFile("/etc/login.defs")
	if err != nil {
		return 1000
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "UID_MIN") && !strings.HasPrefix(line, "#") {
			f := strings.Fields(line)
			if len(f) == 2 {
				if v, err := strconv.Atoi(f[1]); err == nil {
					return v
				}
			}
		}
	}
	return 1000
}

// SoftwareTool holds the name and detected version of an external tool.
// Version is empty when the tool is not found (rendered as N/A in the UI).
type SoftwareTool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// NetworkInterface holds information about a single network interface.
type NetworkInterface struct {
	Name      string   `json:"name"`
	State     string   `json:"state"`      // "up" | "down"
	MAC       string   `json:"mac"`
	MTU       int      `json:"mtu"`
	Addrs     []string `json:"addrs"`      // IPv4 and IPv6 CIDRs
	SpeedMbps int      `json:"speed_mbps"` // -1 if unknown
	RxBytes   int64    `json:"rx_bytes"`
	TxBytes   int64    `json:"tx_bytes"`
	Virtual   bool     `json:"virtual"` // loopback or known virtual prefix
}

// GetNetworkInterfaces returns a snapshot of all network interfaces.
func GetNetworkInterfaces() ([]NetworkInterface, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	// On FreeBSD, gather speeds for all interfaces in one exec.
	var bsdSpeeds map[string]int
	if runtime.GOOS == "freebsd" {
		bsdSpeeds = parseBSDIfaceSpeeds()
	}
	result := make([]NetworkInterface, 0, len(ifaces))
	for _, iface := range ifaces {
		ni := NetworkInterface{
			Name:      iface.Name,
			MAC:       iface.HardwareAddr.String(),
			MTU:       iface.MTU,
			SpeedMbps: -1,
		}
		if iface.Flags&net.FlagUp != 0 {
			ni.State = "up"
		} else {
			ni.State = "down"
		}
		ni.Virtual = iface.Flags&net.FlagLoopback != 0 || isVirtualIface(iface.Name)
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			ni.Addrs = append(ni.Addrs, a.String())
		}
		switch runtime.GOOS {
		case "linux":
			ni.SpeedMbps = linuxIfaceSpeed(iface.Name)
			ni.RxBytes = linuxIfaceStat(iface.Name, "rx_bytes")
			ni.TxBytes = linuxIfaceStat(iface.Name, "tx_bytes")
		case "freebsd":
			if s, ok := bsdSpeeds[iface.Name]; ok {
				ni.SpeedMbps = s
			}
		}
		result = append(result, ni)
	}
	return result, nil
}

var virtualIfacePrefixes = []string{"docker", "virbr", "veth", "br-", "tun", "tap"}

func isVirtualIface(name string) bool {
	for _, pfx := range virtualIfacePrefixes {
		if strings.HasPrefix(name, pfx) {
			return true
		}
	}
	return false
}

func linuxIfaceSpeed(name string) int {
	data, err := os.ReadFile("/sys/class/net/" + name + "/speed")
	if err != nil {
		return -1
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || v < 0 {
		return -1
	}
	return v
}

func linuxIfaceStat(name, stat string) int64 {
	data, err := os.ReadFile("/sys/class/net/" + name + "/statistics/" + stat)
	if err != nil {
		return 0
	}
	v, _ := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	return v
}

// parseBSDIfaceSpeeds runs a single "ifconfig -a" and returns a map of
// interface name → link speed in Mbps. Returns nil on error.
func parseBSDIfaceSpeeds() map[string]int {
	out, err := exec.Command("ifconfig", "-a").Output()
	if err != nil {
		return nil
	}
	speeds := make(map[string]int)
	var cur string
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) > 0 && line[0] != ' ' && line[0] != '\t' {
			if idx := strings.IndexByte(line, ':'); idx > 0 {
				cur = line[:idx]
			}
			continue
		}
		if cur == "" {
			continue
		}
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "media:") {
			continue
		}
		for _, p := range strings.Fields(line) {
			if idx := strings.Index(p, "base"); idx > 0 {
				if v, err := strconv.Atoi(p[:idx]); err == nil {
					speeds[cur] = v
				}
			}
		}
	}
	return speeds
}

// Info holds a snapshot of host and process statistics.
type Info struct {
	// Host
	Hostname   string  `json:"hostname"`
	OS         string  `json:"os"`
	Arch       string  `json:"arch"`
	Kernel     string  `json:"kernel"`
	CPUCount   int     `json:"cpu_count"`
	UptimeSecs float64 `json:"uptime_secs"`
	Load1      float64 `json:"load1"`
	Load5      float64 `json:"load5"`
	Load15     float64 `json:"load15"`

	// Process (dumpstore itself)
	PID            int     `json:"pid"`
	ProcUptimeSecs float64 `json:"proc_uptime_secs"`
	HeapAllocMB    float64 `json:"heap_alloc_mb"`
	SysMB          float64 `json:"sys_mb"`
	Goroutines     int     `json:"goroutines"`
	NumGC          uint32  `json:"num_gc"`

	// External software
	Software []SoftwareTool `json:"software"`
}

// Get collects and returns current system and process information.
func Get() Info {
	hostname, _ := os.Hostname()

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	info := Info{
		Hostname:       hostname,
		OS:             runtime.GOOS,
		Arch:           runtime.GOARCH,
		Kernel:         kernelRelease(),
		CPUCount:       runtime.NumCPU(),
		UptimeSecs:     systemUptime(),
		PID:            os.Getpid(),
		ProcUptimeSecs: time.Since(startTime).Seconds(),
		HeapAllocMB:    float64(ms.HeapAlloc) / 1024 / 1024,
		SysMB:          float64(ms.Sys) / 1024 / 1024,
		Goroutines:     runtime.NumGoroutine(),
		NumGC:          ms.NumGC,
	}
	info.Load1, info.Load5, info.Load15 = loadAverages()
	info.Software = softwareVersions()
	return info
}

func kernelRelease() string {
	out, err := runCmd("uname", "-r")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func systemUptime() float64 {
	switch runtime.GOOS {
	case "linux":
		// /proc/uptime: "12345.67 23456.78\n"
		data, err := os.ReadFile("/proc/uptime")
		if err == nil {
			if parts := strings.Fields(string(data)); len(parts) >= 1 {
				v, _ := strconv.ParseFloat(parts[0], 64)
				return v
			}
		}
	default:
		// FreeBSD/others: sysctl -n kern.boottime → "{ sec = 1234567890, usec = 0 } ..."
		out, err := runCmd("sysctl", "-n", "kern.boottime")
		if err == nil {
			if i := strings.Index(out, "sec = "); i >= 0 {
				s := out[i+6:]
				if j := strings.IndexAny(s, ", }"); j >= 0 {
					s = s[:j]
				}
				if sec, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
					return time.Since(time.Unix(sec, 0)).Seconds()
				}
			}
		}
	}
	return 0
}

func loadAverages() (load1, load5, load15 float64) {
	switch runtime.GOOS {
	case "linux":
		// /proc/loadavg: "0.52 0.58 0.59 1/312 12345\n"
		data, err := os.ReadFile("/proc/loadavg")
		if err == nil {
			if parts := strings.Fields(string(data)); len(parts) >= 3 {
				load1, _ = strconv.ParseFloat(parts[0], 64)
				load5, _ = strconv.ParseFloat(parts[1], 64)
				load15, _ = strconv.ParseFloat(parts[2], 64)
			}
		}
	default:
		// FreeBSD: "{ 0.52 0.58 0.59 }"
		out, err := runCmd("sysctl", "-n", "vm.loadavg")
		if err == nil {
			s := strings.Trim(strings.TrimSpace(out), "{}")
			if parts := strings.Fields(s); len(parts) >= 3 {
				load1, _ = strconv.ParseFloat(parts[0], 64)
				load5, _ = strconv.ParseFloat(parts[1], 64)
				load15, _ = strconv.ParseFloat(parts[2], 64)
			}
		}
	}
	return
}

func runCmd(name string, args ...string) (string, error) {
	var buf bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout = &buf
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// probeVersion runs cmd with args and returns the first non-empty line of
// combined stdout+stderr. Returns "" if the binary is not found.
func probeVersion(cmd string, args ...string) string {
	if _, err := exec.LookPath(cmd); err != nil {
		return ""
	}
	var out bytes.Buffer
	c := exec.Command(cmd, args...)
	c.Stdout = &out
	c.Stderr = &out
	c.Run() // ignore exit code — many tools exit non-zero for --version
	for _, line := range strings.Split(out.String(), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return "installed"
}

// probePresence returns "installed" if cmd is found in PATH, "" otherwise.
func probePresence(cmd string) string {
	if _, err := exec.LookPath(cmd); err != nil {
		return ""
	}
	return "installed"
}

// detectPkgManager returns the name+version of the first known package manager found.
func detectPkgManager() string {
	managers := []struct{ cmd, varg string }{
		{"apt", "--version"},
		{"dnf", "--version"},
		{"yum", "--version"},
		{"pacman", "--version"},
		{"zypper", "--version"},
		{"apk", "--version"},
	}
	for _, m := range managers {
		if v := probeVersion(m.cmd, m.varg); v != "" {
			return v
		}
	}
	return ""
}

// probeISCSIBackend returns "targetcli <version>" on Linux or "ctld (installed)"
// on FreeBSD, whichever is found first. Returns "" if neither is present.
func probeISCSIBackend() string {
	if v := probeVersion("targetcli", "version"); v != "" {
		return "targetcli — " + v
	}
	if v := probePresence("ctld"); v != "" {
		return "ctld — " + v
	}
	return ""
}

// probeNFSServer returns "installed" when the platform NFS server tool is present.
// Linux uses exportfs (from nfs-kernel-server/nfs-utils).
// FreeBSD ships nfsd in the base system, so we probe mountd instead.
func probeNFSServer() string {
	if runtime.GOOS == "freebsd" {
		return probePresence("mountd")
	}
	return probePresence("exportfs")
}

func softwareVersions() []SoftwareTool {
	return []SoftwareTool{
		{Name: "ZFS", Version: probeVersion("zfs", "version")},
		{Name: "Ansible", Version: probeVersion("ansible-playbook", "--version")},
		{Name: "Python", Version: probeVersion("python3", "--version")},
		{Name: "smartctl", Version: probeVersion("smartctl", "--version")},
		{Name: "NFS server", Version: probeNFSServer()},
		{Name: "nfs4-acl-tools", Version: probePresence("nfs4_setfacl")},
		{Name: "setfacl (ACL)", Version: probePresence("setfacl")},
		{Name: "zfs-auto-snapshot", Version: probePresence("zfs-auto-snapshot")},
		{Name: "Samba (smbd)", Version: probeVersion("smbd", "--version")},
		{Name: "iSCSI backend", Version: probeISCSIBackend()},
		{Name: "Package manager", Version: detectPkgManager()},
	}
}
