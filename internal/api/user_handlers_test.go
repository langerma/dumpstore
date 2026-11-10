package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"dumpstore/internal/ansible"
	"dumpstore/internal/broker"
	"dumpstore/internal/operations"
	"dumpstore/internal/system"
)

// statusOf returns the HTTP status a typed handler's error maps to.
func statusOf(err error) int {
	if he, ok := errors.AsType[*httpError](err); ok {
		return he.code
	}
	if err != nil {
		return http.StatusInternalServerError
	}
	return http.StatusOK
}

// fakeAnsibleHandler returns a Handler whose runner execs a stub
// ansible-playbook that records its --extra-vars JSON per playbook and
// succeeds without output. vars(playbook) returns the recorded map.
func fakeAnsibleHandler(t *testing.T) (*Handler, func(playbook string) map[string]string) {
	t.Helper()
	bin := t.TempDir()
	rec := t.TempDir()
	script := "#!/bin/sh\n" +
		"pb=; ev=\n" +
		"while [ $# -gt 0 ]; do case \"$1\" in\n" +
		"  -i) shift ;;\n" +
		"  --extra-vars) shift; ev=\"$1\" ;;\n" +
		"  *) pb=$(basename \"$1\") ;;\n" +
		"esac; shift; done\n" +
		"printf '%s' \"$ev\" > \"" + rec + "/$pb\"\n"
	if err := os.WriteFile(filepath.Join(bin, "ansible-playbook"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	h := &Handler{
		runner:     ansible.NewRunner(t.TempDir()),
		operations: operations.New(nil),
		broker:     broker.New(),
	}
	return h, func(playbook string) map[string]string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(rec, playbook))
		if err != nil {
			t.Fatalf("%s was not run: %v", playbook, err)
		}
		var m map[string]string
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("%s extra vars %q: %v", playbook, data, err)
		}
		return m
	}
}

// TestSSHKeyProtectedUsers keeps the SSH key handlers in parity with the
// user handlers: every protected account is refused with 403 (#197).
func TestSSHKeyProtectedUsers(t *testing.T) {
	h := &Handler{}
	ctx := context.Background()
	for name := range protectedUsers {
		add := struct {
			userParam
			Body SSHKeyRequest `body:""`
		}{userParam{Name: name}, SSHKeyRequest{Key: "ssh-ed25519 AAAA test"}}
		if _, err := h.addSSHKey(ctx, add); statusOf(err) != http.StatusForbidden {
			t.Errorf("addSSHKey(%s): %v, want 403", name, err)
		}
		rm := struct {
			userParam
			Fingerprint string `path:"fingerprint" doc:"SHA256 fingerprint as returned by the list, e.g. SHA256:abc…; contains '/' and '+', which are percent-encoded in the path"`
		}{userParam{Name: name}, "SHA256:abc"}
		if _, err := h.removeSSHKey(ctx, rm); statusOf(err) != http.StatusForbidden {
			t.Errorf("removeSSHKey(%s): %v, want 403", name, err)
		}
	}
}

// TestCreateUserSMBNeedsPassword: smb_user without a password is a 400,
// not a silent 201 without Samba registration (#197).
func TestCreateUserSMBNeedsPassword(t *testing.T) {
	_, err := (&Handler{}).createUser(context.Background(), struct {
		Body CreateUserRequest `body:""`
	}{CreateUserRequest{Username: "alice", SMBUser: true}})
	if statusOf(err) != http.StatusBadRequest || !strings.Contains(err.Error(), "smb_user requires a password") {
		t.Fatalf("createUser(smb_user, no password) = %v, want 400 smb_user requires a password", err)
	}
}

// TestUIDMinPassedToPlaybooks: the playbook asserts use the handlers' limit
// (system.UIDMin), not a hard-coded 1000 (#197).
func TestUIDMinPassedToPlaybooks(t *testing.T) {
	h, vars := fakeAnsibleHandler(t)
	ctx := context.Background()
	want := strconv.Itoa(system.UIDMin())

	if _, err := h.createUser(ctx, struct {
		Body CreateUserRequest `body:""`
	}{CreateUserRequest{Username: "alice", UID: want}}); err != nil {
		t.Fatalf("createUser: %v", err)
	}
	if _, err := h.createGroup(ctx, struct {
		Body CreateGroupRequest `body:""`
	}{CreateGroupRequest{Groupname: "staff2", GID: want}}); err != nil {
		t.Fatalf("createGroup: %v", err)
	}
	for _, pb := range []string{"user_create.yml", "group_create.yml"} {
		if got := vars(pb)["uid_min"]; got != want {
			t.Errorf("%s uid_min = %q, want %q", pb, got, want)
		}
	}
}
