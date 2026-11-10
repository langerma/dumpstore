package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"dumpstore/internal/ansible"
	"dumpstore/internal/system"
)

// getUsers handles GET /api/v1/users
func (h *Handler) getUsers(context.Context, struct{}) ([]system.User, error) {
	return system.ListUsers()
}

// getGroups handles GET /api/v1/groups
func (h *Handler) getGroups(context.Context, struct{}) ([]system.Group, error) {
	return system.ListGroups()
}

// UserResult is the result of a write on one local user.
type UserResult struct {
	Username string             `json:"username"`
	Tasks    []ansible.TaskStep `json:"tasks"`
}

// GroupResult is the result of a write on one local group.
type GroupResult struct {
	Groupname string             `json:"groupname"`
	Tasks     []ansible.TaskStep `json:"tasks"`
}

// SSHKeyList is the GET /api/v1/users/{name}/ssh-keys response.
type SSHKeyList struct {
	Keys []system.AuthorizedKey `json:"keys" doc:"Entries of the user's ~/.ssh/authorized_keys"`
}

// CreateUserRequest is the POST /api/v1/users body.
type CreateUserRequest struct {
	Username    string `json:"username"`
	Shell       string `json:"shell,omitempty" doc:"Login shell (default /bin/bash)"`
	UID         string `json:"uid,omitempty" doc:"Numeric UID; empty lets the OS choose"`
	Group       string `json:"group,omitempty" doc:"Primary group"`
	Groups      string `json:"groups,omitempty" doc:"Comma-separated supplementary groups"`
	Password    string `json:"password,omitempty" doc:"Plaintext password; must not contain newlines"`
	CreateGroup bool   `json:"create_group,omitempty" doc:"Create the primary group (named after the user unless group is set)"`
	SMBUser     bool   `json:"smb_user,omitempty" doc:"Also register the user with Samba"`
}

// createUser handles POST /api/v1/users
// Body: {"username":"alice","shell":"/bin/bash","uid":"1001","group":"storage","groups":"wheel,backup","password":"<plaintext>","create_group":true,"smb_user":false}
func (h *Handler) createUser(ctx context.Context, in struct {
	Body CreateUserRequest `body:""`
}) (UserResult, error) {
	req := in.Body
	if req.Username == "" {
		return UserResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("username is required"))
	}
	if req.Shell == "" {
		req.Shell = "/bin/bash"
	}
	if !validUnixName(req.Username) {
		return UserResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid username"))
	}
	if !validShellPath(req.Shell) {
		return UserResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid shell path"))
	}
	if req.Group != "" && !validUnixName(req.Group) {
		return UserResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid group name"))
	}
	if !validUnixNameList(req.Groups) {
		return UserResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid supplementary group name"))
	}
	if req.Password != "" && !safePassword(req.Password) {
		return UserResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("password must not contain newline characters"))
	}

	createGroup := "false"
	if req.CreateGroup {
		createGroup = "true"
	}
	smbUser := "false"
	if req.SMBUser {
		smbUser = "true"
	}
	h.userMu.Lock()
	defer h.userMu.Unlock()
	vars := map[string]string{
		"username":     req.Username,
		"shell":        req.Shell,
		"uid":          req.UID,
		"group":        req.Group,
		"user_groups":  req.Groups,
		"password":     req.Password,
		"create_group": createGroup,
		"smb_user":     smbUser,
	}
	out, err := h.runOp(ctx, "user_create.yml", vars)
	auditLog(ctx, "user.create", req.Username, err)
	if err != nil {
		return UserResult{}, playbookError(err, out)
	}
	h.publishUserGroup()
	return UserResult{Username: req.Username, Tasks: out.Steps()}, nil
}

// protectedUsers lists accounts that must never be deleted regardless of UID.
var protectedUsers = map[string]bool{"nobody": true, "nfsnobody": true}

// protectedGroups lists groups that must never be deleted regardless of GID.
var protectedGroups = map[string]bool{"nogroup": true, "nobody": true, "nfsnobody": true}

// findUser returns the local user called name, or a 404 error.
func findUser(name string) (*system.User, error) {
	users, err := system.ListUsers()
	if err != nil {
		return nil, err
	}
	for i := range users {
		if users[i].Username == name {
			return &users[i], nil
		}
	}
	return nil, errStatus(http.StatusNotFound, fmt.Errorf("user %q not found", name))
}

// findGroup returns the local group called name, or a 404 error.
func findGroup(name string) (*system.Group, error) {
	groups, err := system.ListGroups()
	if err != nil {
		return nil, err
	}
	for i := range groups {
		if groups[i].Name == name {
			return &groups[i], nil
		}
	}
	return nil, errStatus(http.StatusNotFound, fmt.Errorf("group %q not found", name))
}

// deleteUser handles DELETE /api/v1/users/{name}
// Looks up the user's UID and rejects system users (uid < UIDMin).
func (h *Handler) deleteUser(ctx context.Context, in userParam) (UserResult, error) {
	name := in.Name
	if name == "" {
		return UserResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("username required"))
	}
	if !validUnixName(name) {
		return UserResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid username"))
	}

	target, err := findUser(name)
	if err != nil {
		return UserResult{}, err
	}
	if protectedUsers[name] {
		return UserResult{}, errStatus(http.StatusForbidden, fmt.Errorf("refusing to delete protected user %q", name))
	}
	if target.UID < system.UIDMin() {
		return UserResult{}, errStatus(http.StatusForbidden, fmt.Errorf("refusing to delete system user (uid %d < %d)", target.UID, system.UIDMin()))
	}

	h.userMu.Lock()
	defer h.userMu.Unlock()
	out, err := h.runOp(ctx, "user_delete.yml", map[string]string{
		"username": name,
		"uid":      fmt.Sprintf("%d", target.UID),
	})
	auditLog(ctx, "user.delete", name, err)
	if err != nil {
		return UserResult{}, playbookError(err, out)
	}
	h.publishUserGroup()
	return UserResult{Username: name, Tasks: out.Steps()}, nil
}

// ModifyUserRequest is the PUT /api/v1/users/{name} body. Empty fields are
// left unchanged.
type ModifyUserRequest struct {
	Shell      string `json:"shell,omitempty" doc:"Login shell"`
	Group      string `json:"group,omitempty" doc:"Primary group"`
	UserGroups string `json:"user_groups,omitempty" doc:"Comma-separated supplementary groups"`
	Password   string `json:"password,omitempty" doc:"Plaintext password; must not contain newlines"`
	Home       string `json:"home,omitempty" doc:"Home directory"`
	MoveHome   bool   `json:"move_home,omitempty" doc:"Move the contents of the old home directory to the new one"`
	SMBSync    bool   `json:"smb_sync,omitempty" doc:"Also update the user's Samba password"`
}

// modifyUser handles PUT /api/v1/users/{name}
// Body: {"shell":"/bin/bash","group":"storage","user_groups":"wheel,backup","password":"<plaintext>","home":"/home/foo","move_home":true}
func (h *Handler) modifyUser(ctx context.Context, in struct {
	userParam
	Body ModifyUserRequest `body:""`
}) (UserResult, error) {
	name := in.Name
	if name == "" {
		return UserResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("username required"))
	}
	if !validUnixName(name) {
		return UserResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid username"))
	}
	req := in.Body
	if req.Shell != "" && !validShellPath(req.Shell) {
		return UserResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid shell path"))
	}
	if req.Group != "" && !validUnixName(req.Group) {
		return UserResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid group name"))
	}
	if !validUnixNameList(req.UserGroups) {
		return UserResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid supplementary group name"))
	}
	if req.Home != "" && !validShellPath(req.Home) {
		return UserResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid home directory path"))
	}
	if req.Password != "" && !safePassword(req.Password) {
		return UserResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("password must not contain newline characters"))
	}

	target, err := findUser(name)
	if err != nil {
		return UserResult{}, err
	}
	if protectedUsers[name] {
		return UserResult{}, errStatus(http.StatusForbidden, fmt.Errorf("refusing to modify protected user %q", name))
	}
	if target.UID < system.UIDMin() {
		return UserResult{}, errStatus(http.StatusForbidden, fmt.Errorf("refusing to modify system user (uid %d < %d)", target.UID, system.UIDMin()))
	}

	moveHome := "false"
	if req.MoveHome {
		moveHome = "true"
	}
	smbSync := "false"
	if req.SMBSync {
		smbSync = "true"
	}

	h.userMu.Lock()
	defer h.userMu.Unlock()
	out, err := h.runOp(ctx, "user_modify.yml", map[string]string{
		"username":      name,
		"uid":           fmt.Sprintf("%d", target.UID),
		"shell":         req.Shell,
		"primary_group": req.Group,
		"user_groups":   req.UserGroups,
		"password":      req.Password,
		"home":          req.Home,
		"move_home":     moveHome,
		"smb_sync":      smbSync,
	})
	auditLog(ctx, "user.modify", name, err)
	if err != nil {
		return UserResult{}, playbookError(err, out)
	}
	h.publishUserGroup()
	return UserResult{Username: name, Tasks: out.Steps()}, nil
}

// listSSHKeys handles GET /api/v1/users/{name}/ssh-keys
func (h *Handler) listSSHKeys(_ context.Context, in userParam) (SSHKeyList, error) {
	name := in.Name
	if name == "" || !validUnixName(name) {
		return SSHKeyList{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid username"))
	}
	target, err := findUser(name)
	if err != nil {
		return SSHKeyList{}, err
	}
	lines, err := system.ListAuthorizedKeys(target.Home)
	if err != nil {
		return SSHKeyList{}, err
	}
	keys := make([]system.AuthorizedKey, 0, len(lines))
	for _, l := range lines {
		keys = append(keys, system.ParseAuthorizedKey(l))
	}
	return SSHKeyList{Keys: keys}, nil
}

// SSHKeyRequest is the body of POST and DELETE /api/v1/users/{name}/ssh-keys/{fingerprint}.
type SSHKeyRequest struct {
	Key string `json:"key" doc:"Public key line, e.g. ssh-ed25519 AAAA... comment"`
}

// addSSHKey handles POST /api/v1/users/{name}/ssh-keys
// Body: {"key":"ssh-ed25519 AAAA..."}
func (h *Handler) addSSHKey(ctx context.Context, in struct {
	userParam
	Body SSHKeyRequest `body:""`
}) (TaskResult, error) {
	name := in.Name
	if name == "" || !validUnixName(name) {
		return TaskResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid username"))
	}
	req := in.Body
	req.Key = strings.TrimSpace(req.Key)
	if !validSSHKey(req.Key) {
		return TaskResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid SSH public key"))
	}

	target, err := findUser(name)
	if err != nil {
		return TaskResult{}, err
	}
	if target.UID < system.UIDMin() {
		return TaskResult{}, errStatus(http.StatusForbidden, fmt.Errorf("refusing to modify system user"))
	}

	out, err := h.runOp(ctx, "user_ssh_key_add.yml", map[string]string{
		"username": name,
		"gid":      fmt.Sprintf("%d", target.GID),
		"home":     target.Home,
		"key":      req.Key,
	})
	auditLog(ctx, "sshkey.add", name, err)
	if err != nil {
		return TaskResult{}, playbookError(err, out)
	}
	return TaskResult{Tasks: out.Steps()}, nil
}

// removeSSHKey handles DELETE /api/v1/users/{name}/ssh-keys/{fingerprint}
// Removes the key whose SHA256 fingerprint is {fingerprint}; no body.
func (h *Handler) removeSSHKey(ctx context.Context, in struct {
	userParam
	Fingerprint string `path:"fingerprint" doc:"SHA256 fingerprint as returned by the list, e.g. SHA256:abc…; contains '/' and '+', which are percent-encoded in the path"`
}) (TaskResult, error) {
	name := in.Name
	if name == "" || !validUnixName(name) {
		return TaskResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid username"))
	}
	if !strings.HasPrefix(in.Fingerprint, "SHA256:") {
		return TaskResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("fingerprint must start with SHA256:"))
	}

	target, err := findUser(name)
	if err != nil {
		return TaskResult{}, err
	}
	if target.UID < system.UIDMin() {
		return TaskResult{}, errStatus(http.StatusForbidden, fmt.Errorf("refusing to modify system user"))
	}
	lines, err := system.ListAuthorizedKeys(target.Home)
	if err != nil {
		return TaskResult{}, err
	}
	i := slices.IndexFunc(lines, func(l string) bool { return system.ParseAuthorizedKey(l).Fingerprint == in.Fingerprint })
	if i < 0 {
		return TaskResult{}, errStatus(http.StatusNotFound, fmt.Errorf("no SSH key with fingerprint %s", in.Fingerprint))
	}

	out, err := h.runOp(ctx, "user_ssh_key_remove.yml", map[string]string{
		"home": target.Home,
		"key":  lines[i],
	})
	auditLog(ctx, "sshkey.remove", name, err)
	if err != nil {
		return TaskResult{}, playbookError(err, out)
	}
	return TaskResult{Tasks: out.Steps()}, nil
}

// CreateGroupRequest is the POST /api/v1/groups body.
type CreateGroupRequest struct {
	Groupname string `json:"groupname"`
	GID       string `json:"gid,omitempty" doc:"Numeric GID; empty lets the OS choose"`
}

// createGroup handles POST /api/v1/groups
// Body: {"groupname":"storage","gid":"1500"}
func (h *Handler) createGroup(ctx context.Context, in struct {
	Body CreateGroupRequest `body:""`
}) (GroupResult, error) {
	req := in.Body
	if req.Groupname == "" {
		return GroupResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("groupname is required"))
	}
	if !validUnixName(req.Groupname) {
		return GroupResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid group name"))
	}

	h.userMu.Lock()
	defer h.userMu.Unlock()
	out, err := h.runOp(ctx, "group_create.yml", map[string]string{
		"groupname": req.Groupname,
		"gid":       req.GID,
	})
	auditLog(ctx, "group.create", req.Groupname, err)
	if err != nil {
		return GroupResult{}, playbookError(err, out)
	}
	h.publishUserGroup()
	return GroupResult{Groupname: req.Groupname, Tasks: out.Steps()}, nil
}

// deleteGroup handles DELETE /api/v1/groups/{name}
// Looks up the group's GID and rejects system groups (gid < UIDMin).
func (h *Handler) deleteGroup(ctx context.Context, in groupParam) (GroupResult, error) {
	name := in.Name
	if name == "" {
		return GroupResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("groupname required"))
	}
	if !validUnixName(name) {
		return GroupResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid group name"))
	}

	target, err := findGroup(name)
	if err != nil {
		return GroupResult{}, err
	}
	if protectedGroups[name] {
		return GroupResult{}, errStatus(http.StatusForbidden, fmt.Errorf("refusing to delete protected group %q", name))
	}
	if target.GID < system.UIDMin() {
		return GroupResult{}, errStatus(http.StatusForbidden, fmt.Errorf("refusing to delete system group (gid %d < %d)", target.GID, system.UIDMin()))
	}

	h.userMu.Lock()
	defer h.userMu.Unlock()
	out, err := h.runOp(ctx, "group_delete.yml", map[string]string{
		"groupname": name,
		"gid":       fmt.Sprintf("%d", target.GID),
	})
	auditLog(ctx, "group.delete", name, err)
	if err != nil {
		return GroupResult{}, playbookError(err, out)
	}
	h.publishUserGroup()
	return GroupResult{Groupname: name, Tasks: out.Steps()}, nil
}

// ModifyGroupRequest is the PUT /api/v1/groups/{name} body.
type ModifyGroupRequest struct {
	NewName string `json:"new_name,omitempty" doc:"Rename the group"`
	GID     string `json:"gid,omitempty" doc:"New numeric GID"`
	Members string `json:"members,omitempty" doc:"Comma-separated member list; replaces the current members (empty clears them)"`
}

// modifyGroup handles PUT /api/v1/groups/{name}
// Body: {"new_name":"newgrp","gid":"1501","members":"alice,bob"}
func (h *Handler) modifyGroup(ctx context.Context, in struct {
	groupParam
	Body ModifyGroupRequest `body:""`
}) (GroupResult, error) {
	name := in.Name
	if name == "" {
		return GroupResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("groupname required"))
	}
	if !validUnixName(name) {
		return GroupResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid group name"))
	}
	req := in.Body
	if req.NewName != "" && !validUnixName(req.NewName) {
		return GroupResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid new group name"))
	}
	if !validUnixNameList(req.Members) {
		return GroupResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid member name"))
	}

	target, err := findGroup(name)
	if err != nil {
		return GroupResult{}, err
	}
	if protectedGroups[name] {
		return GroupResult{}, errStatus(http.StatusForbidden, fmt.Errorf("refusing to modify protected group %q", name))
	}
	if target.GID < system.UIDMin() {
		return GroupResult{}, errStatus(http.StatusForbidden, fmt.Errorf("refusing to modify system group (gid %d < %d)", target.GID, system.UIDMin()))
	}

	if req.Members != "" {
		users, err := system.ListUsers()
		if err != nil {
			return GroupResult{}, err
		}
		knownUsers := make(map[string]bool, len(users))
		for _, u := range users {
			knownUsers[u.Username] = true
		}
		var unknown []string
		for _, m := range strings.Split(req.Members, ",") {
			m = strings.TrimSpace(m)
			if m != "" && !knownUsers[m] {
				unknown = append(unknown, m)
			}
		}
		if len(unknown) > 0 {
			return GroupResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("unknown member(s): %s", strings.Join(unknown, ", ")))
		}
	}

	resultName := name
	if req.NewName != "" {
		resultName = req.NewName
	}

	h.userMu.Lock()
	defer h.userMu.Unlock()
	out, err := h.runOp(ctx, "group_modify.yml", map[string]string{
		"groupname":      name,
		"gid":            fmt.Sprintf("%d", target.GID),
		"new_name":       req.NewName,
		"new_gid":        req.GID,
		"members":        req.Members,
		"update_members": "true",
	})
	auditLog(ctx, "group.modify", name, err)
	if err != nil {
		return GroupResult{}, playbookError(err, out)
	}
	h.publishUserGroup()
	return GroupResult{Groupname: resultName, Tasks: out.Steps()}, nil
}

// publishUserGroup re-reads /etc/passwd and /etc/group and immediately pushes
// both topics to all SSE subscribers. Called after every successful user/group
// write so clients see the change without waiting for the next poller tick.
func (h *Handler) publishUserGroup() {
	if users, err := system.ListUsers(); err == nil {
		h.broker.Publish("user.query", users)
	}
	if groups, err := system.ListGroups(); err == nil {
		h.broker.Publish("group.query", groups)
	}
}
