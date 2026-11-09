package iscsi

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
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

func TestRenderSharesPortalGroups(t *testing.T) {
	out, err := Render([]Target{
		{IQN: "iqn.2026-11.io.dumpstore:b", ZvolName: "tank/b", Portals: []string{"0.0.0.0:3260"}, AuthMode: "none"},
		{IQN: "iqn.2026-11.io.dumpstore:a", ZvolName: "tank/a", Portals: []string{"0.0.0.0:3260"}, AuthMode: "none"},
		{IQN: "iqn.2026-11.io.dumpstore:c", ZvolName: "tank/c", Portals: []string{":::3260"}, AuthMode: "none"},
		{IQN: "iqn.2026-11.io.dumpstore:d", ZvolName: "tank/d", Portals: []string{"192.0.2.1:3261"}, AuthMode: "none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if n := strings.Count(s, "portal-group dumpstore-0.0.0.0:3260 {"); n != 1 {
		t.Errorf("want one shared portal group for 0.0.0.0:3260, got %d:\n%s", n, s)
	}
	if n := strings.Count(s, "\nportal-group "); n != 3 {
		t.Errorf("want 3 portal groups, got %d:\n%s", n, s)
	}
	if !strings.Contains(s, "listen [::]:3260\n") {
		t.Errorf("IPv6 portal not bracketed:\n%s", s)
	}
	if strings.Index(s, "target iqn.2026-11.io.dumpstore:a") > strings.Index(s, "target iqn.2026-11.io.dumpstore:b") {
		t.Errorf("targets not sorted by IQN:\n%s", s)
	}
	if !strings.HasPrefix(s, ctlHeader) || !isManaged(out) {
		t.Errorf("missing ownership header:\n%s", s)
	}
}

func TestRenderAuth(t *testing.T) {
	base := func(auth string, inis ...string) Target {
		return Target{IQN: "iqn.2026-11.io.dumpstore:x", ZvolName: "tank/x", Portals: []string{"0.0.0.0:3260"},
			AuthMode: auth, Initiators: inis, CHAPUser: "alice", CHAPSecret: "s3cr#t pass"}
	}
	for _, tc := range []struct {
		name    string
		target  Target
		want    []string
		notWant []string
	}{
		{"none open", base("none"), []string{"\tauth-group no-authentication\n"}, []string{"auth-type", "chap", "initiator-name"}},
		{"none restricted", base("none", "iqn.1994-05.com.redhat:c1"),
			[]string{"\tauth-type none\n", "\tinitiator-name iqn.1994-05.com.redhat:c1\n"}, []string{"auth-group no-auth", "chap"}},
		{"chap", base("chap"), []string{"\tchap \"alice\" \"s3cr#t pass\"\n"}, []string{"auth-type", "auth-group no-auth", "initiator-name"}},
		{"chap restricted", base("chap", "iqn.1994-05.com.redhat:c1"),
			[]string{"\tchap \"alice\" \"s3cr#t pass\"\n", "\tinitiator-name iqn.1994-05.com.redhat:c1\n"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := Render([]Target{tc.target})
			if err != nil {
				t.Fatal(err)
			}
			s := strings.SplitN(string(out), "\ntarget ", 2)[1]
			for _, w := range tc.want {
				if !strings.Contains(s, w) {
					t.Errorf("missing %q in:\n%s", w, s)
				}
			}
			for _, nw := range tc.notWant {
				if strings.Contains(s, nw) {
					t.Errorf("unexpected %q in:\n%s", nw, s)
				}
			}
			if !strings.Contains(s, "\tlun 0 {\n\t\tpath /dev/zvol/tank/x\n\t}\n") {
				t.Errorf("lun block wrong:\n%s", s)
			}
		})
	}
}

func TestRenderRejects(t *testing.T) {
	ok := Target{IQN: "iqn.2026-11.io.dumpstore:x", ZvolName: "tank/x", Portals: []string{"0.0.0.0:3260"}, AuthMode: "none"}
	for name, mut := range map[string]func(*Target){
		"quote in secret":   func(t *Target) { t.AuthMode, t.CHAPUser, t.CHAPSecret = "chap", "u", `a"b` },
		"backslash in user": func(t *Target) { t.AuthMode, t.CHAPUser, t.CHAPSecret = "chap", `a\b`, "s" },
		"empty chap":        func(t *Target) { t.AuthMode = "chap" },
		"two portals":       func(t *Target) { t.Portals = []string{"0.0.0.0:3260", "[::]:3260"} },
		"bad portal":        func(t *Target) { t.Portals = []string{"nope"} },
		"brace in IQN":      func(t *Target) { t.IQN = "iqn.x{" },
		"newline initiator": func(t *Target) { t.Initiators = []string{"iqn.a\n}"} },
		"unknown auth":      func(t *Target) { t.AuthMode = "deny" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := ok
			mut(&bad)
			if _, err := Render([]Target{bad}); err == nil {
				t.Errorf("Render accepted %+v", bad)
			}
		})
	}
	dup := ok
	dup.IQN = "iqn.2026-11.io.dumpstore:y"
	if _, err := Render([]Target{ok, dup}); err == nil {
		t.Error("Render accepted two targets on the same zvol")
	}
}

func TestRenderParseRoundTrip(t *testing.T) {
	in := []Target{
		{IQN: "iqn.2026-11.io.dumpstore:a", ZvolName: "tank/a", Portals: []string{"0.0.0.0:3260"}, AuthMode: "none", Initiators: []string{}},
		{IQN: "iqn.2026-11.io.dumpstore:b", ZvolName: "tank/vms/b", Portals: []string{"[::]:3260"}, AuthMode: "none",
			Initiators: []string{"iqn.1994-05.com.redhat:c1", "iqn.1994-05.com.redhat:c2"}},
		{IQN: "iqn.2026-11.io.dumpstore:c", ZvolName: "tank/c", Portals: []string{"10.0.0.5:3261"}, AuthMode: "chap",
			CHAPUser: "alice", CHAPSecret: "s3cr#t {pass}", Initiators: []string{}},
	}
	out, err := Render(in)
	if err != nil {
		t.Fatal(err)
	}
	got := ParseCtld(string(out))
	for i := range in {
		in[i].ZvolDevice = "/dev/zvol/" + in[i].ZvolName
	}
	if !reflect.DeepEqual(got, in) {
		t.Errorf("round trip mismatch\n got: %+v\nwant: %+v\nfile:\n%s", got, in, out)
	}
	imported, skipped := Import(string(out))
	if len(skipped) != 0 || !reflect.DeepEqual(imported, in) {
		t.Errorf("re-import of a rendered file: skipped %v, got %+v", skipped, imported)
	}
	again, err := Render(imported)
	if err != nil || string(again) != string(out) {
		t.Errorf("render not stable:\n%s\n---\n%s", out, again)
	}
}

func TestRenderEmpty(t *testing.T) {
	out, err := Render(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !isManaged(out) || strings.Contains(string(out), "portal-group") || strings.Contains(string(out), "target") {
		t.Errorf("empty render:\n%s", out)
	}
}

func TestImportLegacy(t *testing.T) {
	data, err := os.ReadFile("testdata/ctl.conf.legacy")
	if err != nil {
		t.Fatal(err)
	}
	if isManaged(data) {
		t.Fatal("legacy fixture must not count as managed")
	}
	targets, skipped := Import(string(data))
	want := []Target{
		{IQN: "iqn.2026-10.local.dumpstore:vm1", ZvolName: "tank/vm1", ZvolDevice: "/dev/zvol/tank/vm1",
			Portals: []string{"0.0.0.0:3260"}, AuthMode: "none", Initiators: []string{}},
		{IQN: "iqn.2026-10.local.dumpstore:vm2", ZvolName: "tank/vm2", ZvolDevice: "/dev/zvol/tank/vm2",
			Portals: []string{"[::]:3260"}, AuthMode: "chap", CHAPUser: "alice", CHAPSecret: "s3cr#t pass",
			Initiators: []string{"iqn.1994-05.com.redhat:client1"}},
	}
	if !reflect.DeepEqual(targets, want) {
		t.Errorf("imported\n got: %+v\nwant: %+v", targets, want)
	}
	joined := strings.Join(skipped, "\n")
	for _, w := range []string{
		"target iqn.2026-10.local.hand:file (", "not a ZFS volume",
		"target iqn.2026-10.local.hand:multi (2 portals)",
		"target iqn.2026-10.local.hand:mutual (auth-group ag-mutual)",
		"global setting: debug 1", "auth-group ag-mutual { … }",
	} {
		if !strings.Contains(joined, w) {
			t.Errorf("skipped list lacks %q:\n%s", w, joined)
		}
	}
	if len(skipped) != 5 {
		t.Errorf("want 5 skipped entries, got %d:\n%s", len(skipped), joined)
	}

	out, err := Render(targets)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "DUMPSTORE") {
		t.Errorf("rendered file still has block markers:\n%s", out)
	}
}

func TestListingHidesCHAP(t *testing.T) {
	b, err := json.Marshal(Target{IQN: "i", AuthMode: "chap", CHAPUser: "alice", CHAPSecret: "topsecret"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "alice") || strings.Contains(string(b), "topsecret") {
		t.Errorf("CHAP credentials serialised: %s", b)
	}
}

func TestListeners(t *testing.T) {
	got := Listeners([]Target{
		{Portals: []string{"0.0.0.0:3260"}},
		{Portals: []string{"0.0.0.0:3260"}},
		{Portals: []string{"[::]:3260"}},
		{Portals: []string{":::3261"}},
		{Portals: []string{"[::1]:3260"}},
		{Portals: []string{"192.0.2.1:3260"}},
	})
	want := []string{"tcp4 *:3260", "tcp4 192.0.2.1:3260", "tcp6 *:3260", "tcp6 *:3261", "tcp6 ::1:3260"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Listeners = %v, want %v", got, want)
	}
}
