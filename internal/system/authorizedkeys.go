package system

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// SSH keys live in a file the user controls, inside a directory the user
// controls, while dumpstore runs as root. Every access below is therefore
// confined to the user's home with os.Root (no path, symlink or ".." can
// leave it) and checks what it actually opened (#215, #216):
//
//   - ~/.ssh must be a directory owned by the user, not a symlink;
//   - ~/.ssh/authorized_keys must be a regular file owned by the user with a
//     single link, so a symlink or a hard link to another file (FreeBSD lets
//     users hard-link files they don't own) is never read or written through;
//   - writes create a fresh temp file (O_EXCL) next to it, chown/chmod it via
//     its descriptor and rename it into place: the old directory entry is
//     replaced, never written through.

// ErrUnsafeKeyFile reports an ~/.ssh or authorized_keys that dumpstore
// refuses to touch. The caller maps it to a 409.
var ErrUnsafeKeyFile = errors.New("unsafe SSH key file")

const (
	sshDir   = ".ssh"
	keysFile = "authorized_keys"
)

func unsafef(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUnsafeKeyFile, fmt.Sprintf(format, args...))
}

// ownerOf returns the owner UID and link count of fi.
func ownerOf(fi fs.FileInfo) (uid int, nlink uint64, ok bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return int(st.Uid), uint64(st.Nlink), true
}

// openSSHDir opens <home>/.ssh as a root confined to it. With create, a
// missing .ssh is created 0700 and owned by uid:gid; without, a missing
// .ssh returns (nil, nil).
func openSSHDir(home string, uid, gid int, create bool) (*os.Root, error) {
	if !filepath.IsAbs(home) {
		return nil, unsafef("home %q is not an absolute path", home)
	}
	h, err := os.OpenRoot(home)
	if err != nil {
		return nil, fmt.Errorf("open home %s: %w", home, err)
	}
	defer h.Close()

	created := false
	fi, err := h.Lstat(sshDir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if !create {
			return nil, nil
		}
		if err := h.Mkdir(sshDir, 0o700); err != nil {
			return nil, fmt.Errorf("create %s/.ssh: %w", home, err)
		}
		created = true
	case err != nil:
		return nil, fmt.Errorf("stat %s/.ssh: %w", home, err)
	case fi.Mode()&fs.ModeSymlink != 0:
		return nil, unsafef("%s/.ssh is a symlink", home)
	case !fi.IsDir():
		return nil, unsafef("%s/.ssh is not a directory", home)
	}

	d, err := h.OpenRoot(sshDir)
	if err != nil {
		return nil, fmt.Errorf("open %s/.ssh: %w", home, err)
	}
	// Check what was opened, not what was stat'ed: the user may have swapped
	// the entry in between.
	dot, err := d.Open(".")
	if err == nil {
		err = checkSSHDir(dot, home, uid, gid, created)
		dot.Close()
	}
	if err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

// checkSSHDir verifies the opened .ssh: a directory owned by uid. One that
// dumpstore just created is handed to uid:gid instead; an existing .ssh
// owned by anyone else is refused, never silently taken over.
func checkSSHDir(dot *os.File, home string, uid, gid int, created bool) error {
	fi, err := dot.Stat()
	if err != nil {
		return err
	}
	owner, _, ok := ownerOf(fi)
	if !ok || !fi.IsDir() {
		return unsafef("%s/.ssh is not a directory", home)
	}
	if created {
		if err := dot.Chown(uid, gid); err != nil {
			return fmt.Errorf("chown %s/.ssh: %w", home, err)
		}
		return dot.Chmod(0o700)
	}
	if owner != uid {
		return unsafef("%s/.ssh is owned by uid %d, not the user (uid %d)", home, owner, uid)
	}
	return nil
}

// readKeysFile returns the content of authorized_keys in d, or nil if it
// doesn't exist.
func readKeysFile(d *os.Root, home string, uid int) ([]byte, error) {
	fi, err := d.Lstat(keysFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return nil, unsafef("%s/.ssh/authorized_keys is a symlink", home)
	}
	f, err := d.Open(keysFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err = f.Stat()
	if err != nil {
		return nil, err
	}
	owner, nlink, ok := ownerOf(fi)
	switch {
	case !ok || !fi.Mode().IsRegular():
		return nil, unsafef("%s/.ssh/authorized_keys is not a regular file", home)
	case nlink != 1:
		return nil, unsafef("%s/.ssh/authorized_keys has %d hard links", home, nlink)
	case owner != uid:
		return nil, unsafef("%s/.ssh/authorized_keys is owned by uid %d, not the user (uid %d)", home, owner, uid)
	}
	return io.ReadAll(io.LimitReader(f, 4<<20))
}

// writeKeysFile atomically replaces authorized_keys in d with data, owned
// uid:gid, mode 0600.
func writeKeysFile(d *os.Root, home string, uid, gid int, data []byte) (err error) {
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return err
	}
	tmp := ".authorized_keys.dumpstore-" + hex.EncodeToString(rnd[:])
	f, err := d.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create temp file in %s/.ssh: %w", home, err)
	}
	defer func() {
		if err != nil {
			d.Remove(tmp)
		}
	}()
	_, werr := f.Write(data)
	err = errors.Join(werr, f.Chown(uid, gid), f.Chmod(0o600), f.Sync(), f.Close())
	if err != nil {
		return fmt.Errorf("write %s/.ssh/%s: %w", home, tmp, err)
	}
	if err := d.Rename(tmp, keysFile); err != nil {
		return fmt.Errorf("replace %s/.ssh/authorized_keys: %w", home, err)
	}
	return nil
}

// keyLines returns the key lines of an authorized_keys file: trimmed, without
// blank lines and comments.
func keyLines(data []byte) []string {
	keys := []string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		keys = append(keys, line)
	}
	return keys
}

// ListAuthorizedKeys returns the key lines of the user's
// ~/.ssh/authorized_keys (empty if absent). It refuses, with
// ErrUnsafeKeyFile, to read through a symlink or hard link or a file the
// user doesn't own.
func ListAuthorizedKeys(home string, uid int) ([]string, error) {
	d, err := openSSHDir(home, uid, -1, false)
	if err != nil || d == nil {
		return []string{}, err
	}
	defer d.Close()
	data, err := readKeysFile(d, home, uid)
	if err != nil {
		return nil, err
	}
	return keyLines(data), nil
}

// AddAuthorizedKey appends key to the user's authorized_keys unless an
// identical line is already present; it creates ~/.ssh and the file if
// needed. Reports whether the file changed.
func AddAuthorizedKey(home string, uid, gid int, key string) (bool, error) {
	key = strings.TrimSpace(key)
	if key == "" || strings.ContainsAny(key, "\r\n") {
		return false, fmt.Errorf("invalid key line")
	}
	d, err := openSSHDir(home, uid, gid, true)
	if err != nil {
		return false, err
	}
	defer d.Close()
	data, err := readKeysFile(d, home, uid)
	if err != nil {
		return false, err
	}
	for _, l := range keyLines(data) {
		if l == key {
			return false, nil
		}
	}
	var buf bytes.Buffer
	buf.Write(data)
	if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		buf.WriteByte('\n')
	}
	buf.WriteString(key + "\n")
	return true, writeKeysFile(d, home, uid, gid, buf.Bytes())
}

// RemoveAuthorizedKeys removes every key line for which match returns true,
// keeping all other lines (comments included) verbatim. Reports how many
// lines were removed; the file is not rewritten when none match.
func RemoveAuthorizedKeys(home string, uid, gid int, match func(line string) bool) (int, error) {
	d, err := openSSHDir(home, uid, gid, false)
	if err != nil || d == nil {
		return 0, err
	}
	defer d.Close()
	data, err := readKeysFile(d, home, uid)
	if err != nil || data == nil {
		return 0, err
	}
	lines := strings.SplitAfter(string(data), "\n")
	var kept strings.Builder
	removed := 0
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t != "" && !strings.HasPrefix(t, "#") && match(t) {
			removed++
			continue
		}
		kept.WriteString(l)
	}
	if removed == 0 {
		return 0, nil
	}
	return removed, writeKeysFile(d, home, uid, gid, []byte(kept.String()))
}
