// Package smb owns the full Samba configuration lifecycle for dumpstore.
// dumpstore is the sole writer of smb.conf — it renders the complete file
// from a template on every write operation. Manual edits outside dumpstore
// will be overwritten on the next apply.
package smb

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"strings"
	"text/template"

	"dumpstore/internal/platform"
)

//go:embed smb.conf.tmpl
var confTemplate string

// SMBConfig is the complete in-memory representation of the dumpstore-managed
// smb.conf. It is parsed from the on-disk file and rendered back to it on
// every write.
type SMBConfig struct {
	Global      SMBGlobal
	Homes       *SMBHomesConfig  // nil = [homes] section absent
	TimeMachine []TimeMachineShare
}

// SMBGlobal holds [global] section values managed by dumpstore.
type SMBGlobal struct {
	Workgroup    string
	ServerString string
}

// SMBHomesConfig holds the [homes] section configuration.
type SMBHomesConfig struct {
	Path          string `json:"path"`
	Browseable    string `json:"browseable"`
	ReadOnly      string `json:"read_only"`
	CreateMask    string `json:"create_mask"`
	DirectoryMask string `json:"directory_mask"`
}

// TimeMachineShare describes a single Time Machine backup share.
type TimeMachineShare struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	MaxSize    string `json:"max_size"`
	ValidUsers string `json:"valid_users"`
}

// ConfPath returns the OS-specific path to smb.conf.
func ConfPath(goos string) string {
	if goos == "freebsd" {
		return "/usr/local/etc/smb4.conf"
	}
	return "/etc/samba/smb.conf"
}

// UsershareDir returns the dumpstore-managed usershares directory.
// dumpstore owns this directory exclusively; it does not share the OS default
// usershares path with manually-created shares.
func UsershareDir(goos string) string {
	return platform.ConfigDir(goos) + "/smb/usershares"
}

// ServiceNames returns the OS-specific Samba service names.
func ServiceNames(goos string) []string {
	if goos == "freebsd" {
		return []string{"samba_server"}
	}
	return []string{"smbd", "nmbd"}
}

// IsInitialized reports whether smb.conf exists at the expected OS path.
func IsInitialized(goos string) bool {
	_, err := os.Stat(ConfPath(goos))
	return err == nil
}

// ParseSMBConfig reads smb.conf and returns the current configuration.
// Returns a zero-value SMBConfig (no homes, no TM shares) if the file is
// missing.
func ParseSMBConfig(goos string) (SMBConfig, error) {
	data, err := os.ReadFile(ConfPath(goos))
	if err != nil {
		if os.IsNotExist(err) {
			return SMBConfig{Global: SMBGlobal{Workgroup: "WORKGROUP", ServerString: "Samba Server"}}, nil
		}
		return SMBConfig{}, fmt.Errorf("read smb.conf: %w", err)
	}
	return parseFromBytes(data)
}

// RenderToFile renders the full smb.conf from the embedded template and writes it to dst.
func (c *SMBConfig) RenderToFile(dst, goos string) error {
	tmpl, err := template.New("smb.conf").Funcs(template.FuncMap{
		"usershareDir": func() string { return UsershareDir(goos) },
	}).Parse(confTemplate)
	if err != nil {
		return fmt.Errorf("parse smb.conf template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, c); err != nil {
		return fmt.Errorf("render smb.conf: %w", err)
	}

	if err := os.WriteFile(dst, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write smb.conf: %w", err)
	}
	return nil
}

// parseFromBytes parses an smb.conf from raw bytes (used in tests).
func parseFromBytes(data []byte) (SMBConfig, error) {
	cfg := SMBConfig{
		Global: SMBGlobal{
			Workgroup:    "WORKGROUP",
			ServerString: "Samba Server",
		},
	}

	type section int
	const (
		secNone   section = iota
		secGlobal section = iota
		secHomes  section = iota
		secTM     section = iota
		secOther  section = iota
	)

	cur := secNone
	var tmCur *TimeMachineShare
	isTM := false

	flushTM := func() {
		if tmCur != nil && isTM {
			cfg.TimeMachine = append(cfg.TimeMachine, *tmCur)
		}
		tmCur = nil
		isTM = false
	}

	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			flushTM()
			name := trimmed[1 : len(trimmed)-1]
			switch strings.ToLower(name) {
			case "global":
				cur = secGlobal
			case "homes":
				cur = secHomes
				if cfg.Homes == nil {
					cfg.Homes = &SMBHomesConfig{
						Browseable:    "no",
						ReadOnly:      "no",
						CreateMask:    "0644",
						DirectoryMask: "0755",
					}
				}
			case "printers", "print$":
				cur = secOther
			default:
				cur = secTM
				tmCur = &TimeMachineShare{Name: name}
			}
			continue
		}

		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}

		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		val := strings.TrimSpace(parts[1])

		switch cur {
		case secGlobal:
			switch key {
			case "workgroup":
				cfg.Global.Workgroup = val
			case "server string":
				cfg.Global.ServerString = val
			}
		case secHomes:
			if cfg.Homes == nil {
				continue
			}
			switch key {
			case "path":
				cfg.Homes.Path = val
			case "browseable", "browsable":
				cfg.Homes.Browseable = val
			case "read only":
				cfg.Homes.ReadOnly = val
			case "create mask", "create mode":
				cfg.Homes.CreateMask = val
			case "directory mask", "directory mode":
				cfg.Homes.DirectoryMask = val
			}
		case secTM:
			if tmCur == nil {
				continue
			}
			switch key {
			case "path":
				tmCur.Path = val
			case "fruit:time machine":
				if strings.EqualFold(val, "yes") {
					isTM = true
				}
			case "fruit:time machine max size":
				tmCur.MaxSize = val
			case "valid users":
				tmCur.ValidUsers = val
			}
		}
	}
	flushTM()
	return cfg, nil
}

// DirsToCreate returns all directory paths referenced by this config that
// must exist before the config is applied.
func (c *SMBConfig) DirsToCreate() []string {
	dirs := []string{}
	if c.Homes != nil && c.Homes.Path != "" {
		// Strip %U and similar substitutions to get the base directory
		base := strings.TrimSuffix(c.Homes.Path, "/%U")
		base = strings.TrimSuffix(base, "/%u")
		if base != "" {
			dirs = append(dirs, base)
		}
	}
	for _, tm := range c.TimeMachine {
		if tm.Path != "" {
			dirs = append(dirs, tm.Path)
		}
	}
	return dirs
}
