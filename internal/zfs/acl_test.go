package zfs

import "testing"

func TestNormalizeACLType(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"posix", "posix"},
		{"posixacl", "posix"},
		{"POSIX", "posix"},
		{"PosixAcl", "posix"},
		{"nfsv4", "nfsv4"},
		{"nfsv4acls", "nfsv4"},
		{"NFSv4", "nfsv4"},
		{"off", "off"},
		{"", "off"},
		{"unknown", "off"},
		{"disabled", "off"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeACLType(tt.input)
			if got != tt.want {
				t.Errorf("normalizeACLType(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParsePOSIXACLLine(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  ACLEntry
		ok    bool
	}{
		{
			name:  "owner no qualifier",
			input: "user::rwx",
			want:  ACLEntry{Tag: "user", Qualifier: "", Perms: "rwx", Default: false},
			ok:    true,
		},
		{
			name:  "named user",
			input: "user:alice:r-x",
			want:  ACLEntry{Tag: "user", Qualifier: "alice", Perms: "r-x", Default: false},
			ok:    true,
		},
		{
			name:  "owning group",
			input: "group::r--",
			want:  ACLEntry{Tag: "group", Qualifier: "", Perms: "r--", Default: false},
			ok:    true,
		},
		{
			name:  "named group",
			input: "group:devs:rwx",
			want:  ACLEntry{Tag: "group", Qualifier: "devs", Perms: "rwx", Default: false},
			ok:    true,
		},
		{
			name:  "mask 2-part",
			input: "mask::r-x",
			want:  ACLEntry{Tag: "mask", Qualifier: "", Perms: "r-x", Default: false},
			ok:    true,
		},
		{
			name:  "other",
			input: "other::---",
			want:  ACLEntry{Tag: "other", Qualifier: "", Perms: "---", Default: false},
			ok:    true,
		},
		{
			name:  "default entry",
			input: "default:user::rwx",
			want:  ACLEntry{Tag: "user", Qualifier: "", Perms: "rwx", Default: true},
			ok:    true,
		},
		{
			name:  "default named user",
			input: "default:user:alice:rwx",
			want:  ACLEntry{Tag: "user", Qualifier: "alice", Perms: "rwx", Default: true},
			ok:    true,
		},
		{
			name:  "effective rights comment stripped",
			input: "user:alice:rwx\t#effective:r--",
			want:  ACLEntry{Tag: "user", Qualifier: "alice", Perms: "rwx", Default: false},
			ok:    true,
		},
		{
			name:  "empty line",
			input: "",
			want:  ACLEntry{},
			ok:    false,
		},
		{
			name:  "single field",
			input: "single",
			want:  ACLEntry{},
			ok:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parsePOSIXACLLine(tt.input)
			if ok != tt.ok {
				t.Fatalf("parsePOSIXACLLine(%q) ok = %v, want %v", tt.input, ok, tt.ok)
			}
			if !tt.ok {
				return
			}
			if got.Tag != tt.want.Tag {
				t.Errorf("Tag = %q, want %q", got.Tag, tt.want.Tag)
			}
			if got.Qualifier != tt.want.Qualifier {
				t.Errorf("Qualifier = %q, want %q", got.Qualifier, tt.want.Qualifier)
			}
			if got.Perms != tt.want.Perms {
				t.Errorf("Perms = %q, want %q", got.Perms, tt.want.Perms)
			}
			if got.Default != tt.want.Default {
				t.Errorf("Default = %v, want %v", got.Default, tt.want.Default)
			}
		})
	}
}

func TestParseNFSv4ACLLine(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  ACLEntry
		ok    bool
	}{
		{
			name:  "allow no flags",
			input: "A::OWNER@:rwaDxtTnNcCoy",
			want:  ACLEntry{Tag: "A", Flags: "", Qualifier: "OWNER@", Perms: "rwaDxtTnNcCoy"},
			ok:    true,
		},
		{
			name:  "allow with flags",
			input: "A:fd:GROUP@:rwaDxtTnNcCoy",
			want:  ACLEntry{Tag: "A", Flags: "fd", Qualifier: "GROUP@", Perms: "rwaDxtTnNcCoy"},
			ok:    true,
		},
		{
			name:  "deny",
			input: "D::alice@localdomain:x",
			want:  ACLEntry{Tag: "D", Flags: "", Qualifier: "alice@localdomain", Perms: "x"},
			ok:    true,
		},
		{
			name:  "everyone",
			input: "A::EVERYONE@:rtncy",
			want:  ACLEntry{Tag: "A", Flags: "", Qualifier: "EVERYONE@", Perms: "rtncy"},
			ok:    true,
		},
		{
			name:  "invalid single token",
			input: "bad",
			want:  ACLEntry{},
			ok:    false,
		},
		{
			name:  "only three parts",
			input: "only:three:parts",
			want:  ACLEntry{},
			ok:    false,
		},
		{
			name:  "extra colons captured in perms via SplitN",
			input: "A:fd:user@dom:perms:extra",
			want:  ACLEntry{Tag: "A", Flags: "fd", Qualifier: "user@dom", Perms: "perms:extra"},
			ok:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseNFSv4ACLLine(tt.input)
			if ok != tt.ok {
				t.Fatalf("parseNFSv4ACLLine(%q) ok = %v, want %v", tt.input, ok, tt.ok)
			}
			if !tt.ok {
				return
			}
			if got.Tag != tt.want.Tag {
				t.Errorf("Tag = %q, want %q", got.Tag, tt.want.Tag)
			}
			if got.Flags != tt.want.Flags {
				t.Errorf("Flags = %q, want %q", got.Flags, tt.want.Flags)
			}
			if got.Qualifier != tt.want.Qualifier {
				t.Errorf("Qualifier = %q, want %q", got.Qualifier, tt.want.Qualifier)
			}
			if got.Perms != tt.want.Perms {
				t.Errorf("Perms = %q, want %q", got.Perms, tt.want.Perms)
			}
		})
	}
}
