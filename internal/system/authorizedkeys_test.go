package system

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	keyA = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIA1 alice@a"
	keyB = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIB2 alice@b"
)

// newHome returns a home directory owned by the test user plus its uid/gid.
func newHome(t *testing.T) (string, int, int) {
	t.Helper()
	return t.TempDir(), os.Getuid(), os.Getgid()
}

func mode(t *testing.T, p string) os.FileMode {
	t.Helper()
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func TestAddAuthorizedKeyCreatesAndAppends(t *testing.T) {
	home, uid, gid := newHome(t)
	changed, err := AddAuthorizedKey(home, uid, gid, keyA)
	if err != nil || !changed {
		t.Fatalf("first add: changed=%v err=%v", changed, err)
	}
	if m := mode(t, filepath.Join(home, ".ssh")); m != 0o700 {
		t.Errorf(".ssh mode %o, want 700", m)
	}
	kf := filepath.Join(home, ".ssh", "authorized_keys")
	if m := mode(t, kf); m != 0o600 {
		t.Errorf("authorized_keys mode %o, want 600", m)
	}
	if changed, err := AddAuthorizedKey(home, uid, gid, "  "+keyA+"  "); err != nil || changed {
		t.Errorf("duplicate add: changed=%v err=%v, want no change", changed, err)
	}
	if _, err := AddAuthorizedKey(home, uid, gid, keyB); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(kf)
	if string(got) != keyA+"\n"+keyB+"\n" {
		t.Errorf("content %q", got)
	}
	keys, err := ListAuthorizedKeys(home, uid)
	if err != nil || len(keys) != 2 || keys[0] != keyA || keys[1] != keyB {
		t.Errorf("list = %v, %v", keys, err)
	}
	// No temp file left behind.
	ents, _ := os.ReadDir(filepath.Join(home, ".ssh"))
	if len(ents) != 1 {
		t.Errorf(".ssh holds %d entries, want only authorized_keys", len(ents))
	}
}

func TestAddAuthorizedKeyKeepsExistingContent(t *testing.T) {
	home, uid, gid := newHome(t)
	os.Mkdir(filepath.Join(home, ".ssh"), 0o700)
	kf := filepath.Join(home, ".ssh", "authorized_keys")
	os.WriteFile(kf, []byte("# my comment\n"+keyA), 0o600) // no trailing newline
	if _, err := AddAuthorizedKey(home, uid, gid, keyB); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(kf)
	if string(got) != "# my comment\n"+keyA+"\n"+keyB+"\n" {
		t.Errorf("content %q", got)
	}
}

func TestRemoveAuthorizedKeys(t *testing.T) {
	home, uid, gid := newHome(t)
	os.Mkdir(filepath.Join(home, ".ssh"), 0o700)
	kf := filepath.Join(home, ".ssh", "authorized_keys")
	os.WriteFile(kf, []byte("# keep me\n"+keyA+"\n\n"+keyB+"\n"), 0o600)
	n, err := RemoveAuthorizedKeys(home, uid, gid, func(l string) bool { return l == keyA })
	if err != nil || n != 1 {
		t.Fatalf("remove: n=%d err=%v", n, err)
	}
	got, _ := os.ReadFile(kf)
	if string(got) != "# keep me\n\n"+keyB+"\n" {
		t.Errorf("content %q", got)
	}
	n, err = RemoveAuthorizedKeys(home, uid, gid, func(string) bool { return false })
	if err != nil || n != 0 {
		t.Errorf("no match: n=%d err=%v", n, err)
	}
	// Absent .ssh: nothing to remove, nothing created.
	empty, _, _ := newHome(t)
	if n, err := RemoveAuthorizedKeys(empty, uid, gid, func(string) bool { return true }); err != nil || n != 0 {
		t.Errorf("absent .ssh: n=%d err=%v", n, err)
	}
	if _, err := os.Stat(filepath.Join(empty, ".ssh")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("remove created .ssh")
	}
}

// victim is a file outside the home that a malicious user points the key
// file at; every case below must leave it untouched.
func victim(t *testing.T) (string, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "shadow")
	const content = "root:secret:0:0\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p, content
}

func assertUnsafe(t *testing.T, what string, err error) {
	t.Helper()
	if !errors.Is(err, ErrUnsafeKeyFile) {
		t.Errorf("%s: err = %v, want ErrUnsafeKeyFile", what, err)
	}
}

func assertUntouched(t *testing.T, p, want string) {
	t.Helper()
	got, err := os.ReadFile(p)
	if err != nil || string(got) != want {
		t.Errorf("victim %s changed: %q, %v", p, got, err)
	}
	if m := mode(t, p); m != 0o600 {
		t.Errorf("victim mode changed to %o", m)
	}
}

func TestRefusesSymlinkedSSHDir(t *testing.T) {
	home, uid, gid := newHome(t)
	v, content := victim(t)
	if err := os.Symlink(filepath.Dir(v), filepath.Join(home, ".ssh")); err != nil {
		t.Fatal(err)
	}
	_, err := AddAuthorizedKey(home, uid, gid, keyA)
	assertUnsafe(t, "add", err)
	_, err = ListAuthorizedKeys(home, uid)
	assertUnsafe(t, "list", err)
	_, err = RemoveAuthorizedKeys(home, uid, gid, func(string) bool { return true })
	assertUnsafe(t, "remove", err)
	assertUntouched(t, v, content)
	if _, err := os.Stat(filepath.Join(filepath.Dir(v), "authorized_keys")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a key file was created outside the home")
	}
}

func TestRefusesSymlinkedKeyFile(t *testing.T) {
	home, uid, gid := newHome(t)
	v, content := victim(t)
	os.Mkdir(filepath.Join(home, ".ssh"), 0o700)
	if err := os.Symlink(v, filepath.Join(home, ".ssh", "authorized_keys")); err != nil {
		t.Fatal(err)
	}
	_, err := AddAuthorizedKey(home, uid, gid, keyA)
	assertUnsafe(t, "add", err)
	keys, err := ListAuthorizedKeys(home, uid)
	assertUnsafe(t, "list", err)
	if strings.Contains(strings.Join(keys, ""), "secret") {
		t.Errorf("list disclosed the victim's content")
	}
	_, err = RemoveAuthorizedKeys(home, uid, gid, func(string) bool { return true })
	assertUnsafe(t, "remove", err)
	assertUntouched(t, v, content)
}

func TestRefusesHardLinkedKeyFile(t *testing.T) {
	home, uid, gid := newHome(t)
	v, content := victim(t)
	os.Mkdir(filepath.Join(home, ".ssh"), 0o700)
	if err := os.Link(v, filepath.Join(home, ".ssh", "authorized_keys")); err != nil {
		t.Skipf("hard link across temp dirs not possible here: %v", err)
	}
	_, err := AddAuthorizedKey(home, uid, gid, keyA)
	assertUnsafe(t, "add", err)
	_, err = ListAuthorizedKeys(home, uid)
	assertUnsafe(t, "list", err)
	assertUntouched(t, v, content)
}

func TestRefusesNonRegularKeyFile(t *testing.T) {
	home, uid, gid := newHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".ssh", "authorized_keys"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := AddAuthorizedKey(home, uid, gid, keyA)
	assertUnsafe(t, "add", err)
}

func TestRefusesFilesOwnedBySomeoneElse(t *testing.T) {
	// The key file must belong to the user whose keys are edited. As a
	// non-root test user every file we create is ours, so claim to edit
	// another uid: the existing file then has the "wrong" owner.
	home, uid, gid := newHome(t)
	if _, err := AddAuthorizedKey(home, uid, gid, keyA); err != nil {
		t.Fatal(err)
	}
	other := uid + 4242
	_, err := ListAuthorizedKeys(home, other)
	assertUnsafe(t, "list", err)
	_, err = AddAuthorizedKey(home, other, gid, keyB)
	assertUnsafe(t, "add", err)
	_, err = RemoveAuthorizedKeys(home, other, gid, func(string) bool { return true })
	assertUnsafe(t, "remove", err)
	// The file of the real owner is untouched.
	if keys, _ := ListAuthorizedKeys(home, uid); len(keys) != 1 || keys[0] != keyA {
		t.Errorf("keys after refused writes: %v", keys)
	}
}

func TestRejectsMultiLineKey(t *testing.T) {
	home, uid, gid := newHome(t)
	if _, err := AddAuthorizedKey(home, uid, gid, keyA+"\n"+keyB); err == nil {
		t.Fatal("multi-line key accepted")
	}
	if _, err := os.Stat(filepath.Join(home, ".ssh")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("rejected key still created .ssh")
	}
}
