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
