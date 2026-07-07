package zfs

import "testing"

func TestRewriteRecognized(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want bool
	}{
		{
			name: "supported: usage error mentions rewrite",
			out:  "missing dataset argument\nusage:\n\trewrite [-rvx] [-o <offset>] [-l <length>] <directory|file ...>\n",
			want: true,
		},
		{
			name: "unsupported: unrecognized command",
			out:  "unrecognized command 'rewrite'\nusage: zfs command args ...\n",
			want: false,
		},
		{
			name: "zfs binary missing",
			out:  "",
			want: false,
		},
	}
	for _, tc := range cases {
		if got := rewriteRecognized(tc.out); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestDraidSupported(t *testing.T) {
	withDraid := `This system supports ZFS pool feature flags.

The following features are supported:

FEAT DESCRIPTION
-------------------------------------------------------------
async_destroy                         (read-only compatible)
     Destroy filesystems asynchronously.
draid
     Support for distributed spare RAID
zstd_compress
     zstd compression algorithm support.
`
	withoutDraid := `This system supports ZFS pool feature flags.

The following features are supported:

FEAT DESCRIPTION
-------------------------------------------------------------
async_destroy                         (read-only compatible)
     Destroy filesystems asynchronously.
zstd_compress
     zstd compression algorithm support.
`
	if !draidSupported(withDraid) {
		t.Error("expected draid to be detected in feature list")
	}
	if draidSupported(withoutDraid) {
		t.Error("draid detected in feature list that lacks it")
	}
	if draidSupported("") {
		t.Error("draid detected in empty output")
	}
}
