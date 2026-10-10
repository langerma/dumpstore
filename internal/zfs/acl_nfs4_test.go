package zfs

import "testing"

func TestParseFreeBSDNFSv4Line(t *testing.T) {
	cases := []struct {
		line string
		want ACLEntry
	}{
		{"owner@:rwxp--aARWcCos:-------:allow", ACLEntry{Tag: "A", Qualifier: "OWNER@", Perms: "rwxatTnNcCoy"}},
		{"everyone@:r-x---a-R-c--s:-------:allow", ACLEntry{Tag: "A", Qualifier: "EVERYONE@", Perms: "rxtncy"}},
		{"user:alice:rwx-----------:fd-----:allow", ACLEntry{Tag: "A", Flags: "fd", Qualifier: "alice", Perms: "rwx"}},
		{"group:staff:r-------------:-d-----:deny", ACLEntry{Tag: "D", Flags: "dg", Qualifier: "staff", Perms: "r"}},
	}
	for _, c := range cases {
		got, ok := parseFreeBSDNFSv4Line(c.line)
		if !ok || got != c.want {
			t.Errorf("parse %q = %+v (ok %v), want %+v", c.line, got, ok, c.want)
		}
	}
	for _, bad := range []string{"", "# file: /tank/x", "user:alice:rwx:fd:maybe", "owner@:rwx"} {
		if _, ok := parseFreeBSDNFSv4Line(bad); ok {
			t.Errorf("parse %q: expected failure", bad)
		}
	}
}

func TestFreeBSDACESpec(t *testing.T) {
	cases := []struct {
		ace, want string
	}{
		{"A:fd:alice:rwx", "user:alice:rwx:fd:allow"},
		{"A::alice@localdomain:rwaDdxtTnNcCoy", "user:alice:rwpDdxaARWcCos::allow"},
		{"D:g:staff:w", "group:staff:w::deny"},
		{"A::OWNER@:rwx", "owner@:rwx::allow"},
	}
	for _, c := range cases {
		e, ok := ParseNFSv4ACE(c.ace)
		if !ok {
			t.Fatalf("ParseNFSv4ACE(%q) failed", c.ace)
		}
		got, err := FreeBSDACESpec(e)
		if err != nil || got != c.want {
			t.Errorf("FreeBSDACESpec(%q) = %q, %v; want %q", c.ace, got, err, c.want)
		}
	}
	for _, bad := range []string{"X::alice:rwx", "A::alice:rwq", "A:z:alice:rwx", "A::alice:", "A:::rwx"} {
		e, _ := ParseNFSv4ACE(bad)
		if spec, err := FreeBSDACESpec(e); err == nil {
			t.Errorf("FreeBSDACESpec(%q) = %q, expected error", bad, spec)
		}
	}
}

// A FreeBSD entry parsed and translated back must name the same entry, so
// removals built from listed entries match exactly.
func TestFreeBSDACERoundTrip(t *testing.T) {
	for _, line := range []string{
		"user:alice:rwxp--aARWcCos:fd-----:allow",
		"group:staff:r-x---a-R-c--s:-------:deny",
		"owner@:rwxp--aARWcCos:-------:allow",
	} {
		e, ok := parseFreeBSDNFSv4Line(line)
		if !ok {
			t.Fatalf("parse %q failed", line)
		}
		spec, err := FreeBSDACESpec(e)
		if err != nil {
			t.Fatalf("spec for %q: %v", line, err)
		}
		back, ok := parseFreeBSDNFSv4Line(spec)
		if !ok || back != e {
			t.Errorf("round trip %q → %q → %+v, want %+v", line, spec, back, e)
		}
	}
}
