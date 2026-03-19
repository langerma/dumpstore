package api

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestValidZFSName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"simple pool", "tank", true},
		{"pool with dataset", "tank/data", true},
		{"nested dataset", "tank/data/child", true},
		{"with dots", "tank/my.data", true},
		{"with colons", "tank/my:data", true},
		{"with dashes", "tank/my-data", true},
		{"with underscores", "tank/my_data", true},
		{"numeric after first char", "tank1/data2", true},
		{"empty string", "", false},
		{"starts with number", "1tank", false},
		{"starts with dot", ".tank", false},
		{"starts with slash", "/tank", false},
		{"trailing slash", "tank/", false},
		{"double slash", "tank//data", false},
		{"component starts with number", "tank/1data", false},
		{"contains space", "tank/my data", false},
		{"contains at sign", "tank/d@ta", false},
		{"contains semicolon", "tank/d;ta", false},
		{"single char", "t", true},
		{"single char dataset", "t/d", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validZFSName(tt.input); got != tt.want {
				t.Errorf("validZFSName(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidSnapLabel(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"simple label", "daily", true},
		{"with numbers", "snap2024", true},
		{"starts with number", "2024snap", true},
		{"with dots", "auto.2024", true},
		{"with colons", "snap:v1", true},
		{"with dashes", "snap-daily", true},
		{"with underscores", "snap_daily", true},
		{"empty string", "", false},
		{"starts with dot", ".snap", false},
		{"starts with dash", "-snap", false},
		{"contains space", "my snap", false},
		{"contains slash", "snap/label", false},
		{"contains at sign", "snap@label", false},
		{"single char alpha", "s", true},
		{"single digit", "1", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validSnapLabel(tt.input); got != tt.want {
				t.Errorf("validSnapLabel(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidUnixName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"simple name", "alice", true},
		{"starts with underscore", "_svc", true},
		{"with numbers", "user01", true},
		{"with dots", "user.name", true},
		{"with dashes", "user-name", true},
		{"with underscores", "user_name", true},
		{"single char", "a", true},
		{"single underscore", "_", true},
		{"max length 32 chars", "abcdefghijklmnopqrstuvwxyz012345", true},
		{"too long 33 chars", "abcdefghijklmnopqrstuvwxyz0123456", false},
		{"empty string", "", false},
		{"starts with number", "1user", false},
		{"starts with dash", "-user", false},
		{"starts with dot", ".user", false},
		{"contains space", "my user", false},
		{"contains slash", "user/name", false},
		{"contains colon", "user:name", false},
		{"contains at sign", "user@host", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validUnixName(tt.input); got != tt.want {
				t.Errorf("validUnixName(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidSMBShare(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"simple name", "myshare", true},
		{"with numbers", "share01", true},
		{"with dots", "my.share", true},
		{"with dashes", "my-share", true},
		{"with underscores", "my_share", true},
		{"single char", "s", true},
		{"starts with number", "1share", true},
		{"starts with dot", ".share", true},
		{"starts with dash", "-share", true},
		{"80 chars", strings.Repeat("a", 80), true},
		{"81 chars too long", strings.Repeat("a", 81), false},
		{"empty string", "", false},
		{"contains space", "my share", false},
		{"contains slash", "my/share", false},
		{"contains at sign", "my@share", false},
		{"contains colon", "my:share", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validSMBShare(tt.input); got != tt.want {
				t.Errorf("validSMBShare(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidShellPath(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"bash", "/bin/bash", true},
		{"zsh", "/usr/bin/zsh", true},
		{"nologin", "/usr/sbin/nologin", true},
		{"false", "/bin/false", true},
		{"with dots", "/usr/local/bin/my.shell", true},
		{"with dashes", "/usr/local/bin/my-shell", true},
		{"with underscores", "/usr/local/bin/my_shell", true},
		{"empty string", "", false},
		{"no leading slash", "bin/bash", false},
		{"just slash", "/", false},
		{"trailing slash", "/bin/", true},
		{"double slash", "/bin//bash", true},
		{"path with dots", "/bin/../bash", true},
		{"contains space", "/bin/my shell", false},
		{"contains semicolon", "/bin/bash;rm", false},
		{"contains backtick", "/bin/`bash`", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validShellPath(tt.input); got != tt.want {
				t.Errorf("validShellPath(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidIQN(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"standard iqn", "iqn.2024-01.com.example:target0", true},
		{"with dots in domain", "iqn.2024-01.com.example.storage:lun1", true},
		{"with colons in target", "iqn.2024-12.org.myhost:disk:0", true},
		{"with dashes in target", "iqn.2024-01.com.example:my-target", true},
		{"with dots in target", "iqn.2024-01.com.example:my.target", true},
		{"with underscores in target", "iqn.2024-01.com.example:my_target", true},
		{"empty string", "", false},
		{"no iqn prefix", "2024-01.com.example:target0", false},
		{"wrong date format", "iqn.24-01.com.example:target0", false},
		{"missing month", "iqn.2024.com.example:target0", false},
		{"missing colon separator", "iqn.2024-01.com.example.target0", false},
		{"uppercase domain", "iqn.2024-01.COM.EXAMPLE:target0", false},
		{"empty target after colon", "iqn.2024-01.com.example:", false},
		{"spaces in iqn", "iqn.2024-01.com.example:my target", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validIQN(tt.input); got != tt.want {
				t.Errorf("validIQN(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestSafePropertyValue(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"simple value", "on", true},
		{"numeric value", "1024", true},
		{"path-like value", "/mnt/data", true},
		{"with equals", "rw=@10.0.0.0/24", true},
		{"with comma", "rw=@10.0.0.0/24,rw=@192.168.1.0/24", true},
		{"empty string", "", true},
		{"with colon", "lz4:on", true},
		{"with at sign", "rw=@host", true},
		{"contains semicolon", "on;rm -rf", false},
		{"contains backtick", "on`cmd`", false},
		{"contains pipe", "on|cmd", false},
		{"contains ampersand", "on&cmd", false},
		{"contains dollar", "on$VAR", false},
		{"contains newline", "on\ncmd", false},
		{"contains carriage return", "on\rcmd", false},
		{"contains backslash", "on\\n", false},
		{"contains double quote", `on"cmd"`, false},
		{"contains single quote", "on'cmd'", false},
		{"contains asterisk", "on*", false},
		{"contains open paren", "on(", false},
		{"contains close paren", "on)", false},
		{"contains question mark", "on?", false},
		{"contains exclamation", "on!", false},
		{"contains tilde", "on~", false},
		{"contains open brace", "on{", false},
		{"contains close brace", "on}", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := safePropertyValue(tt.input); got != tt.want {
				t.Errorf("safePropertyValue(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidUnixNameList(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"empty string", "", true},
		{"single name", "wheel", true},
		{"two names", "wheel,audio", true},
		{"three names", "wheel,audio,video", true},
		{"with spaces around commas", "wheel, audio, video", true},
		{"name with underscore", "_svc,users", true},
		{"name with dash", "my-group,users", true},
		{"invalid name in list", "wheel,1bad,audio", false},
		{"empty element", "wheel,,audio", false},
		{"trailing comma", "wheel,audio,", false},
		{"leading comma", ",wheel,audio", false},
		{"name too long", strings.Repeat("a", 33), false},
		{"single invalid name", "1bad", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validUnixNameList(tt.input); got != tt.want {
				t.Errorf("validUnixNameList(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidAutoSnapValue(t *testing.T) {
	tests := []struct {
		name string
		prop string
		val  string
		want bool
	}{
		{"main prop true", "com.sun:auto-snapshot", "true", true},
		{"main prop false", "com.sun:auto-snapshot", "false", true},
		{"main prop empty", "com.sun:auto-snapshot", "", true},
		{"main prop invalid", "com.sun:auto-snapshot", "yes", false},
		{"main prop numeric", "com.sun:auto-snapshot", "1", false},
		{"keep count 1", "com.sun:auto-snapshot:daily", "1", true},
		{"keep count 10", "com.sun:auto-snapshot:hourly", "10", true},
		{"keep count 9999", "com.sun:auto-snapshot:weekly", "9999", true},
		{"keep count empty", "com.sun:auto-snapshot:daily", "", true},
		{"keep count 0 invalid", "com.sun:auto-snapshot:daily", "0", false},
		{"keep count 10000 too long", "com.sun:auto-snapshot:daily", "10000", false},
		{"keep count negative", "com.sun:auto-snapshot:daily", "-1", false},
		{"keep count non-numeric", "com.sun:auto-snapshot:daily", "abc", false},
		{"keep count leading zero", "com.sun:auto-snapshot:daily", "01", false},
		{"keep count true invalid", "com.sun:auto-snapshot:frequent", "true", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validAutoSnapValue(tt.prop, tt.val); got != tt.want {
				t.Errorf("validAutoSnapValue(%q, %q) = %v, want %v", tt.prop, tt.val, got, tt.want)
			}
		})
	}
}

func TestValidPortalIP(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"ipv4 localhost", "127.0.0.1", true},
		{"ipv4 private", "192.168.1.1", true},
		{"ipv4 all zeros", "0.0.0.0", true},
		{"ipv6 loopback", "::1", true},
		{"ipv6 full", "2001:0db8:85a3:0000:0000:8a2e:0370:7334", true},
		{"ipv6 shortened", "2001:db8::1", true},
		{"empty string", "", false},
		{"hostname", "example.com", false},
		{"ipv4 too many octets", "1.2.3.4.5", false},
		{"ipv4 octet too large", "256.1.1.1", false},
		{"garbage", "not-an-ip", false},
		{"ipv4 with port", "192.168.1.1:3260", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validPortalIP(tt.input); got != tt.want {
				t.Errorf("validPortalIP(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidPort(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"port 1", "1", true},
		{"port 80", "80", true},
		{"port 443", "443", true},
		{"port 3260", "3260", true},
		{"port 65535", "65535", true},
		{"port 0 invalid", "0", false},
		{"port 65536 too high", "65536", false},
		{"negative port", "-1", false},
		{"empty string", "", false},
		{"non-numeric", "abc", false},
		{"float", "80.5", false},
		{"with spaces", " 80", false},
		{"very large number", "999999", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validPort(tt.input); got != tt.want {
				t.Errorf("validPort(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestDecodeJSON(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
		Size int    `json:"size"`
	}

	tests := []struct {
		name    string
		body    string
		wantErr bool
		wantVal payload
	}{
		{
			name:    "valid object",
			body:    `{"name":"tank/data","size":100}`,
			wantErr: false,
			wantVal: payload{Name: "tank/data", Size: 100},
		},
		{
			name:    "valid object with whitespace",
			body:    `  { "name": "tank/data", "size": 100 }  `,
			wantErr: false,
			wantVal: payload{Name: "tank/data", Size: 100},
		},
		{
			name:    "empty body",
			body:    "",
			wantErr: true,
		},
		{
			name:    "invalid json",
			body:    `{name: invalid}`,
			wantErr: true,
		},
		{
			name:    "two json objects",
			body:    `{"name":"a","size":1}{"name":"b","size":2}`,
			wantErr: true,
		},
		{
			name:    "json with trailing garbage",
			body:    `{"name":"a","size":1} extra`,
			wantErr: true,
		},
		{
			name:    "partial fields",
			body:    `{"name":"tank/data"}`,
			wantErr: false,
			wantVal: payload{Name: "tank/data", Size: 0},
		},
		{
			name:    "null body",
			body:    `null`,
			wantErr: false,
			wantVal: payload{},
		},
		{
			name:    "wrong type value",
			body:    `{"name":123}`,
			wantErr: true,
		},
		{
			name:    "array instead of object",
			body:    `[1,2,3]`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, "/test", io.NopCloser(bytes.NewBufferString(tt.body)))
			if err != nil {
				t.Fatalf("failed to create request: %v", err)
			}
			req.Header.Set("Content-Type", "application/json")

			var got payload
			err = decodeJSON(req, &got)

			if (err != nil) != tt.wantErr {
				t.Errorf("decodeJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if got.Name != tt.wantVal.Name || got.Size != tt.wantVal.Size {
					t.Errorf("decodeJSON() got = %+v, want %+v", got, tt.wantVal)
				}
			}
		})
	}
}
