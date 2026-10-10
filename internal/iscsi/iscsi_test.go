package iscsi

import (
	"os"
	"testing"
)

// TestParseLinuxSaveconfig parses a saveconfig.json written by targetcli-fb
// (Ubuntu dev VM) after creating a zvol-backed target through dumpstore.
// targetcli stores the target IQN under "wwn", not "name".
func TestParseLinuxSaveconfig(t *testing.T) {
	data, err := os.ReadFile("testdata/saveconfig.json")
	if err != nil {
		t.Fatal(err)
	}
	targets, err := parseLinuxSaveconfig(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("want 1 target, got %d: %+v", len(targets), targets)
	}
	got := targets[0]
	if got.IQN != "iqn.2026-10.local.dumpstore:itest" {
		t.Errorf("IQN = %q, want iqn.2026-10.local.dumpstore:itest", got.IQN)
	}
	if got.ZvolName != "tank/itest-vol" || got.ZvolDevice != "/dev/zvol/tank/itest-vol" {
		t.Errorf("zvol = %q (%q), want tank/itest-vol", got.ZvolName, got.ZvolDevice)
	}
	if got.AuthMode != "none" || len(got.Portals) != 1 || got.Portals[0] != "0.0.0.0:3260" {
		t.Errorf("auth/portals = %q %v", got.AuthMode, got.Portals)
	}
}
