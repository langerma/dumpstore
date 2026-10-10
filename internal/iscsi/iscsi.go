// Package iscsi reads iSCSI target configuration from the platform's native
// backend: targetcli/LIO on Linux, ctld on FreeBSD.
//
// This package is read-only. Write operations (create, delete) are handled
// by Ansible playbooks invoked through the API layer.
package iscsi

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Target describes an iSCSI target backed by a ZFS volume.
type Target struct {
	IQN        string   `json:"iqn"`
	ZvolName   string   `json:"zvol_name"`   // e.g. "tank/vm1"
	ZvolDevice string   `json:"zvol_device"` // e.g. "/dev/zvol/tank/vm1"
	LUN        int      `json:"lun"`
	Portals    []string `json:"portals"`     // e.g. ["0.0.0.0:3260"]
	AuthMode   string   `json:"auth_mode"`   // "none" | "chap"
	Initiators []string `json:"initiators"`  // empty = allow-all
}

// Backend returns "linux" if targetcli is in PATH, "freebsd" if ctld is, or "".
func Backend() string {
	if _, err := exec.LookPath("targetcli"); err == nil {
		return "linux"
	}
	if _, err := exec.LookPath("ctld"); err == nil {
		return "freebsd"
	}
	return ""
}

// ListTargets returns all iSCSI targets, dispatching based on the available
// backend. Returns an empty slice when no backend is installed.
func ListTargets() ([]Target, error) {
	switch Backend() {
	case "linux":
		return listLinuxTargets()
	case "freebsd":
		return listFreeBSDTargets()
	default:
		return []Target{}, nil
	}
}

// ── Linux / targetcli ─────────────────────────────────────────────────────────

const linuxSaveconfigPath = "/etc/rtslib-fb-target/saveconfig.json"

// linuxSaveconfig is the subset of targetcli's JSON saveconfig we need.
type linuxSaveconfig struct {
	StorageObjects []struct {
		Dev    string `json:"dev"`
		Name   string `json:"name"`
		Plugin string `json:"plugin"`
	} `json:"storage_objects"`
	Targets []struct {
		Fabric string `json:"fabric"`
		WWN    string `json:"wwn"` // the target IQN
		TPGs   []struct {
			Tag     int  `json:"tag"`
			Enable  bool `json:"enable"`
			Portals []struct {
				IP   string `json:"ip_address"`
				Port int    `json:"port"`
			} `json:"portals"`
			LUNs []struct {
				Index         int    `json:"index"`
				StorageObject string `json:"storage_object"`
			} `json:"luns"`
			NodeACLs []struct {
				NodeWWN string `json:"node_wwn"`
			} `json:"node_acls"`
			Attributes struct {
				Authentication int `json:"authentication"`
			} `json:"attributes"`
		} `json:"tpgs"`
	} `json:"targets"`
}

func listLinuxTargets() ([]Target, error) {
	data, err := os.ReadFile(linuxSaveconfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []Target{}, nil
		}
		return nil, fmt.Errorf("reading targetcli saveconfig: %w", err)
	}
	return parseLinuxSaveconfig(data)
}

// parseLinuxSaveconfig extracts zvol-backed iSCSI targets from targetcli's
// saveconfig JSON.
func parseLinuxSaveconfig(data []byte) ([]Target, error) {
	var cfg linuxSaveconfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing targetcli saveconfig: %w", err)
	}

	// Map backstore name → device path for block backstores.
	backstoreByName := make(map[string]string)
	for _, so := range cfg.StorageObjects {
		if so.Plugin == "block" {
			backstoreByName[so.Name] = so.Dev
		}
	}

	var targets []Target
	for _, t := range cfg.Targets {
		if t.Fabric != "iscsi" {
			continue
		}
		for _, tpg := range t.TPGs {
			// Resolve block device via first LUN whose backstore is a block device.
			var zvolDev string
			var lun int
			for _, l := range tpg.LUNs {
				// StorageObject path: "/backstores/block/tank-vm1"
				parts := strings.Split(l.StorageObject, "/")
				name := parts[len(parts)-1]
				if dev, ok := backstoreByName[name]; ok {
					zvolDev = dev
					lun = l.Index
					break
				}
			}
			if zvolDev == "" {
				continue // not a zvol-backed target
			}

			var portals []string
			for _, p := range tpg.Portals {
				portals = append(portals, fmt.Sprintf("%s:%d", p.IP, p.Port))
			}

			var initiators []string
			for _, acl := range tpg.NodeACLs {
				initiators = append(initiators, acl.NodeWWN)
			}
			if initiators == nil {
				initiators = []string{}
			}

			authMode := "none"
			if tpg.Attributes.Authentication == 1 {
				authMode = "chap"
			}

			// "/dev/zvol/tank/vm1" → "tank/vm1"
			zvolName := strings.TrimPrefix(zvolDev, "/dev/zvol/")

			targets = append(targets, Target{
				IQN:        t.WWN,
				ZvolName:   zvolName,
				ZvolDevice: zvolDev,
				LUN:        lun,
				Portals:    portals,
				AuthMode:   authMode,
				Initiators: initiators,
			})
		}
	}
	return targets, nil
}

// ── FreeBSD / ctld ────────────────────────────────────────────────────────────

const freebsdCtlConf = "/etc/ctl.conf"

func listFreeBSDTargets() ([]Target, error) {
	data, err := os.ReadFile(freebsdCtlConf)
	if err != nil {
		if os.IsNotExist(err) {
			return []Target{}, nil
		}
		return nil, fmt.Errorf("reading ctl.conf: %w", err)
	}
	return parseCtlConf(string(data))
}

// parseCtlConf extracts iSCSI targets backed by zvols from ctl.conf content.
// Two-pass: first collect portal-group → listen addresses, then parse targets.
func parseCtlConf(content string) ([]Target, error) {
	lines := strings.Split(content, "\n")

	// Pass 1: collect portal-group blocks.
	portalGroups := make(map[string][]string) // name → []"ip:port"
	i := 0
	for i < len(lines) {
		line := ctlStripComment(strings.TrimSpace(lines[i]))
		if strings.HasPrefix(line, "portal-group ") && strings.HasSuffix(line, "{") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				pgName := parts[1]
				block, end := extractBlock(lines, i)
				for _, bl := range block {
					bl = ctlStripComment(strings.TrimSpace(bl))
					if strings.HasPrefix(bl, "listen ") {
						addr := strings.TrimSpace(strings.TrimPrefix(bl, "listen "))
						portalGroups[pgName] = append(portalGroups[pgName], addr)
					}
				}
				i = end + 1
				continue
			}
		}
		i++
	}

	// Pass 2: parse target blocks.
	var targets []Target
	i = 0
	for i < len(lines) {
		line := ctlStripComment(strings.TrimSpace(lines[i]))
		if strings.HasPrefix(line, "target ") && strings.HasSuffix(line, "{") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				iqn := parts[1]
				block, end := extractBlock(lines, i)
				if t := parseTargetBlock(iqn, block, portalGroups); t != nil {
					targets = append(targets, *t)
				}
				i = end + 1
				continue
			}
		}
		i++
	}
	return targets, nil
}

// parseTargetBlock extracts fields from the lines inside a target { } block.
func parseTargetBlock(iqn string, lines []string, portalGroups map[string][]string) *Target {
	t := &Target{
		IQN:        iqn,
		AuthMode:   "none",
		Initiators: []string{},
	}

	i := 0
	for i < len(lines) {
		line := ctlStripComment(strings.TrimSpace(lines[i]))
		if line == "" {
			i++
			continue
		}

		// lun <n> { path ... }
		if strings.HasPrefix(line, "lun ") && strings.HasSuffix(line, "{") {
			block, end := extractBlock(lines, i)
			for _, bl := range block {
				bl = ctlStripComment(strings.TrimSpace(bl))
				if strings.HasPrefix(bl, "path ") {
					dev := strings.TrimSpace(strings.TrimPrefix(bl, "path "))
					t.ZvolDevice = dev
					t.ZvolName = strings.TrimPrefix(dev, "/dev/zvol/")
				}
			}
			i = end + 1
			continue
		}

		// portal-group <name>   (without a { — it's a reference, not a block)
		if strings.HasPrefix(line, "portal-group ") && !strings.HasSuffix(line, "{") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				pgName := parts[1]
				if portals, ok := portalGroups[pgName]; ok {
					t.Portals = append(t.Portals, portals...)
				}
			}
		}

		// chap "user" "password" — indicates CHAP auth
		if strings.HasPrefix(line, "chap ") {
			t.AuthMode = "chap"
		}

		// initiator-name <iqn>
		if strings.HasPrefix(line, "initiator-name ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				t.Initiators = append(t.Initiators, parts[1])
			}
		}

		i++
	}

	if t.ZvolName == "" {
		return nil // not zvol-backed
	}
	if len(t.Portals) == 0 {
		t.Portals = []string{"0.0.0.0:3260"}
	}
	return t
}

// extractBlock returns the lines inside the { } block whose opening line is at
// startIdx, and the index of the closing '}'. Handles nested blocks.
func extractBlock(lines []string, startIdx int) ([]string, int) {
	depth := 0
	var block []string
	for i := startIdx; i < len(lines); i++ {
		l := ctlStripComment(strings.TrimSpace(lines[i]))
		if strings.HasSuffix(l, "{") {
			depth++
			if i == startIdx {
				continue // skip the opening "target ... {" line itself
			}
		}
		if l == "}" {
			depth--
			if depth == 0 {
				return block, i
			}
		}
		if i != startIdx {
			block = append(block, lines[i])
		}
	}
	return block, len(lines) - 1
}

// ctlStripComment removes a trailing # comment from a ctl.conf line.
func ctlStripComment(line string) string {
	if idx := strings.Index(line, "#"); idx >= 0 {
		return strings.TrimSpace(line[:idx])
	}
	return line
}
