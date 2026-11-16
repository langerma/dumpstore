// Package smb owns the full Samba configuration lifecycle for dumpstore.
// dumpstore is the sole writer of smb.conf — it renders the complete file
// from a template on every write operation. Manual edits outside dumpstore
// will be overwritten on the next apply.
package smb

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/template"
	"unicode"

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

// confHeader starts every smb.conf rendered from smb.conf.tmpl — unchanged
// since the template was introduced (#82) — so it marks a file dumpstore owns.
const confHeader = "# This file is managed by dumpstore."

// IsInitialized reports whether smb.conf exists and was rendered by
// dumpstore (#193). A distro-default or hand-written smb.conf does not count:
// POST /api/v1/smb/init must back it up to smb.conf.pre-dumpstore first.
func IsInitialized(goos string) bool {
	data, err := os.ReadFile(ConfPath(goos))
	return err == nil && isManaged(data)
}

func isManaged(data []byte) bool { return bytes.HasPrefix(data, []byte(confHeader)) }

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

// reservedSections are smb.conf section names with built-in meaning; a
// share of that name would be parsed back as the special section.
var reservedSections = map[string]bool{"global": true, "homes": true, "printers": true, "print$": true}

// IsReservedSection reports whether name (case-insensitive) is a reserved
// smb.conf section name.
func IsReservedSection(name string) bool { return reservedSections[strings.ToLower(name)] }

// Validate refuses a config whose values could break out of their line in
// the rendered smb.conf (#217): a control character (newline, CR, …) would
// start a new parameter or section, and a backslash at the end of a value
// continues it onto the next line. Share names must not be reserved
// sections. Handlers validate each field with a clear 400 first; this is the
// last line of defence before the owned file is written.
func (c *SMBConfig) Validate() error {
	check := func(field, v string) error {
		if strings.ContainsFunc(v, unicode.IsControl) || strings.Contains(v, `\`) {
			return fmt.Errorf("smb.conf %s %q contains a control character or backslash", field, v)
		}
		return nil
	}
	errs := []error{
		check("workgroup", c.Global.Workgroup),
		check("server string", c.Global.ServerString),
	}
	if h := c.Homes; h != nil {
		errs = append(errs, check("[homes] path", h.Path), check("[homes] browseable", h.Browseable),
			check("[homes] read only", h.ReadOnly), check("[homes] create mask", h.CreateMask),
			check("[homes] directory mask", h.DirectoryMask))
	}
	for _, tm := range c.TimeMachine {
		if IsReservedSection(tm.Name) || strings.ContainsAny(tm.Name, "[]") {
			errs = append(errs, fmt.Errorf("Time Machine share name %q is not allowed", tm.Name))
		}
		errs = append(errs, check("share name", tm.Name), check("["+tm.Name+"] path", tm.Path),
			check("["+tm.Name+"] max size", tm.MaxSize), check("["+tm.Name+"] valid users", tm.ValidUsers))
	}
	return errors.Join(errs...)
}

// RenderToFile renders the full smb.conf from the embedded template and writes it to dst.
func (c *SMBConfig) RenderToFile(dst, goos string) error {
	if err := c.Validate(); err != nil {
		return err
	}
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
