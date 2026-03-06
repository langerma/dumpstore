// Package system collects host and process information without external dependencies.
package system

import (
	"bytes"
	"os"
	"os/exec"
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
	var users []User
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
	var groups []Group
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || line[0] == '#' {
			continue
		}
		f := strings.SplitN(line, ":", 4)
		if len(f) < 4 {
			continue
		}
		gid, _ := strconv.Atoi(f[2])
		var members []string
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
		{Name: "Package manager", Version: detectPkgManager()},
	}
}
