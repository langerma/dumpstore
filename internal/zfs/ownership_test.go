package zfs

import (
	"os/user"
	"testing"
)

func TestGetMountpointOwnership(t *testing.T) {
	me, err := user.Current()
	if err != nil {
		t.Skipf("no current user: %v", err)
	}
	owner, group, err := GetMountpointOwnership(t.TempDir())
	if err != nil {
		t.Fatalf("GetMountpointOwnership: %v", err)
	}
	if owner != me.Username {
		t.Errorf("owner = %q, want %q", owner, me.Username)
	}
	if group == "" {
		t.Errorf("empty group")
	}
	if _, _, err := GetMountpointOwnership("/nonexistent-dumpstore-path"); err == nil {
		t.Errorf("expected error for missing path")
	}
}
