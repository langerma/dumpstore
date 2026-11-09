package smb

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestListUsershares(t *testing.T) {
	dir := t.TempDir()
	present := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Version 2 file: the share name comes from sharename=.
	write("data", "#VERSION 2\npath="+present+"\ncomment=\nusershare_acl=S-1-1-0:F\nguest_ok=n\nsharename=Data\n")
	// Version 1 file: no sharename=, the file name is the share name.
	write("old", "#VERSION 1\npath=/nonexistent/dumpstore-test\ncomment=\nusershare_acl=S-1-1-0:F\n")
	// Temp and dot files are skipped.
	write(":tmpAbC123", "path=/x\nsharename=tmp\n")
	write(".hidden", "path=/x\nsharename=hidden\n")
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := ListUsershares(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []Usershare{
		{Name: "Data", Path: present},
		{Name: "old", Path: "/nonexistent/dumpstore-test", PathMissing: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestListUsersharesMissingDir(t *testing.T) {
	got, err := ListUsershares(filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("got %#v, want empty non-nil slice", got)
	}
}

func TestParseUsershare(t *testing.T) {
	got := parseUsershare("x", []byte("#VERSION 2\npath=/tank/a=b\nsharename=\n"))
	want := Usershare{Name: "x", Path: "/tank/a=b"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
