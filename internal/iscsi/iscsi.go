// Package iscsi reads iSCSI target configuration from the platform's native
// backend: targetcli/LIO on Linux, ctld on FreeBSD.
//
// Linux: read-only; targetcli writes go through Ansible playbooks.
// FreeBSD: dumpstore owns /etc/ctl.conf (#186). This package parses it into
// a []Target model and renders it back whole (ctld.go); the API layer
// deploys the rendered file via playbooks/iscsi_ctld_apply.yml.
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
	Portals    []string `json:"portals"`    // e.g. ["0.0.0.0:3260"]
	AuthMode   string   `json:"auth_mode"`  // "none" | "chap"
	Initiators []string `json:"initiators"` // empty = allow-all

	// CHAP credentials (ctld only). Never serialised: GET /api/iscsi-targets
	// must not expose the secret.
	CHAPUser   string `json:"-"`
	CHAPSecret string `json:"-"`
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
