package smb

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTrip_Linux(t *testing.T) {
	roundTrip(t, "linux")
}

func TestRoundTrip_FreeBSD(t *testing.T) {
	roundTrip(t, "freebsd")
}

func roundTrip(t *testing.T, goos string) {
	t.Helper()

	orig := SMBConfig{
		Global: SMBGlobal{
			Workgroup:    "TESTGROUP",
			ServerString: "Test Samba",
		},
		Homes: &SMBHomesConfig{
			Path:          "/tank/homes/%U",
			Browseable:    "no",
			ReadOnly:      "no",
			CreateMask:    "0644",
			DirectoryMask: "0755",
		},
		TimeMachine: []TimeMachineShare{
			{
				Name:       "TimeMachine",
				Path:       "/tank/tm",
				MaxSize:    "500G",
				ValidUsers: "@backup",
			},
		},
	}

	tmp := filepath.Join(t.TempDir(), "smb.conf")
	if err := orig.RenderToFile(tmp, goos); err != nil {
		t.Fatalf("RenderToFile: %v", err)
	}

	// Swap ConfPath to point at our temp file by patching env indirectly:
	// we do it by reading the file directly via ParseSMBConfig after overriding
	// the lookup with a wrapper that reads from tmp.
	data, err := os.ReadFile(tmp)
	if err != nil {
		t.Fatalf("read rendered file: %v", err)
	}

	// Write to the expected OS path temporarily by monkey-patching via a
	// sub-function that accepts a path directly (internal helper).
	parsed, err := parseFromBytes(data)
	if err != nil {
		t.Fatalf("parseFromBytes: %v", err)
	}

	if parsed.Global.Workgroup != orig.Global.Workgroup {
		t.Errorf("Workgroup: got %q want %q", parsed.Global.Workgroup, orig.Global.Workgroup)
	}
	if parsed.Global.ServerString != orig.Global.ServerString {
		t.Errorf("ServerString: got %q want %q", parsed.Global.ServerString, orig.Global.ServerString)
	}
	if parsed.Homes == nil {
		t.Fatal("Homes: got nil, want non-nil")
	}
	if parsed.Homes.Path != orig.Homes.Path {
		t.Errorf("Homes.Path: got %q want %q", parsed.Homes.Path, orig.Homes.Path)
	}
	if parsed.Homes.Browseable != orig.Homes.Browseable {
		t.Errorf("Homes.Browseable: got %q want %q", parsed.Homes.Browseable, orig.Homes.Browseable)
	}
	if parsed.Homes.CreateMask != orig.Homes.CreateMask {
		t.Errorf("Homes.CreateMask: got %q want %q", parsed.Homes.CreateMask, orig.Homes.CreateMask)
	}
	if len(parsed.TimeMachine) != 1 {
		t.Fatalf("TimeMachine: got %d entries, want 1", len(parsed.TimeMachine))
	}
	tm := parsed.TimeMachine[0]
	if tm.Name != "TimeMachine" {
		t.Errorf("TM.Name: got %q want %q", tm.Name, "TimeMachine")
	}
	if tm.Path != "/tank/tm" {
		t.Errorf("TM.Path: got %q want %q", tm.Path, "/tank/tm")
	}
	if tm.MaxSize != "500G" {
		t.Errorf("TM.MaxSize: got %q want %q", tm.MaxSize, "500G")
	}
	if tm.ValidUsers != "@backup" {
		t.Errorf("TM.ValidUsers: got %q want %q", tm.ValidUsers, "@backup")
	}
}

func TestRoundTrip_NoHomes(t *testing.T) {
	orig := SMBConfig{
		Global: SMBGlobal{Workgroup: "WORKGROUP", ServerString: "Samba Server"},
	}
	tmp := filepath.Join(t.TempDir(), "smb.conf")
	if err := orig.RenderToFile(tmp, "linux"); err != nil {
		t.Fatalf("RenderToFile: %v", err)
	}
	data, _ := os.ReadFile(tmp)
	parsed, err := parseFromBytes(data)
	if err != nil {
		t.Fatalf("parseFromBytes: %v", err)
	}
	if parsed.Homes != nil {
		t.Errorf("Homes: expected nil when not configured, got %+v", parsed.Homes)
	}
	if len(parsed.TimeMachine) != 0 {
		t.Errorf("TimeMachine: expected empty, got %d entries", len(parsed.TimeMachine))
	}
}

func TestDirsToCreate(t *testing.T) {
	cfg := SMBConfig{
		Homes: &SMBHomesConfig{Path: "/tank/homes/%U"},
		TimeMachine: []TimeMachineShare{
			{Name: "TM", Path: "/tank/tm"},
		},
	}
	dirs := cfg.DirsToCreate()
	if len(dirs) != 2 {
		t.Fatalf("expected 2 dirs, got %d: %v", len(dirs), dirs)
	}
	if dirs[0] != "/tank/homes" {
		t.Errorf("dirs[0]: got %q want /tank/homes", dirs[0])
	}
	if dirs[1] != "/tank/tm" {
		t.Errorf("dirs[1]: got %q want /tank/tm", dirs[1])
	}
}
