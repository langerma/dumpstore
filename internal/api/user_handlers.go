package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"dumpstore/internal/ansible"
	"dumpstore/internal/system"
)

// getUsers handles GET /api/users
func (h *Handler) getUsers(w http.ResponseWriter, r *http.Request) {
	users, err := system.ListUsers()
	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
		return
	}
	writeJSON(r.Context(), w, users)
}

// getGroups handles GET /api/groups
func (h *Handler) getGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := system.ListGroups()
	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
		return
	}
	writeJSON(r.Context(), w, groups)
}

// createUser handles POST /api/users
// Body: {"username":"alice","shell":"/bin/bash","uid":1001,"group":"storage","user_groups":"wheel,backup","password":"$6$...","create_group":true,"smb_user":false}
func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username    string `json:"username"`
		Shell       string `json:"shell"`
		UID         string `json:"uid"`
		Group       string `json:"group"`
		Groups      string `json:"groups"`
		Password    string `json:"password"`
		CreateGroup bool   `json:"create_group"`
		SMBUser     bool   `json:"smb_user"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}
	if req.Username == "" {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("username is required"), nil)
		return
	}
	if req.Shell == "" {
		req.Shell = "/bin/bash"
	}
	if !validUnixName(req.Username) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid username"), nil)
		return
	}
	if !validShellPath(req.Shell) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid shell path"), nil)
		return
	}
	if req.Group != "" && !validUnixName(req.Group) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid group name"), nil)
		return
	}
	if !validUnixNameList(req.Groups) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid supplementary group name"), nil)
		return
	}
	if req.Password != "" && !safePassword(req.Password) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("password must not contain newline characters"), nil)
		return
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
	out, err := h.runOp("user_create.yml", vars)
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(r.Context(), w, http.StatusInternalServerError, err, steps)
		return
	}
	h.publishUserGroup()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"username": req.Username, "tasks": out.Steps()})
}

// protectedUsers lists accounts that must never be deleted regardless of UID.
var protectedUsers = map[string]bool{"nobody": true, "nfsnobody": true}

// protectedGroups lists groups that must never be deleted regardless of GID.
var protectedGroups = map[string]bool{"nogroup": true, "nobody": true, "nfsnobody": true}

// deleteUser handles DELETE /api/users/{name}
// Looks up the user's UID and rejects system users (uid < UIDMin).
func (h *Handler) deleteUser(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("username required"), nil)
		return
	}
	if !validUnixName(name) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid username"), nil)
		return
	}

	users, err := system.ListUsers()
	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
		return
	}
	var target *system.User
	for i := range users {
		if users[i].Username == name {
			target = &users[i]
			break
		}
	}
	if target == nil {
		writeError(r.Context(), w, http.StatusNotFound, fmt.Errorf("user %q not found", name), nil)
		return
	}
	if protectedUsers[name] {
		writeError(r.Context(), w, http.StatusForbidden, fmt.Errorf("refusing to delete protected user %q", name), nil)
		return
	}
	if target.UID < system.UIDMin() {
		writeError(r.Context(), w, http.StatusForbidden, fmt.Errorf("refusing to delete system user (uid %d < %d)", target.UID, system.UIDMin()), nil)
		return
	}

	h.userMu.Lock()
	defer h.userMu.Unlock()
	out, err := h.runOp("user_delete.yml", map[string]string{
		"username": name,
		"uid":      fmt.Sprintf("%d", target.UID),
	})
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(r.Context(), w, http.StatusInternalServerError, err, steps)
		return
	}
	h.publishUserGroup()
	writeJSON(r.Context(), w, map[string]any{"username": name, "tasks": out.Steps()})
}

// modifyUser handles PUT /api/users/{name}
// Body: {"shell":"/bin/bash","group":"storage","user_groups":"wheel,backup","password":"$6$...","home":"/home/foo","move_home":true}
func (h *Handler) modifyUser(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("username required"), nil)
		return
	}
	if !validUnixName(name) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid username"), nil)
		return
	}
	var req struct {
		Shell      string `json:"shell"`
		Group      string `json:"group"`
		UserGroups string `json:"user_groups"`
		Password   string `json:"password"`
		Home       string `json:"home"`
		MoveHome   bool   `json:"move_home"`
		SMBSync    bool   `json:"smb_sync"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}
	if req.Shell != "" && !validShellPath(req.Shell) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid shell path"), nil)
		return
	}
	if req.Group != "" && !validUnixName(req.Group) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid group name"), nil)
		return
	}
	if !validUnixNameList(req.UserGroups) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid supplementary group name"), nil)
		return
	}
	if req.Home != "" && !validShellPath(req.Home) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid home directory path"), nil)
		return
	}
	if req.Password != "" && !safePassword(req.Password) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("password must not contain newline characters"), nil)
		return
	}

	users, err := system.ListUsers()
	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
		return
	}
	var target *system.User
	for i := range users {
		if users[i].Username == name {
			target = &users[i]
			break
		}
	}
	if target == nil {
		writeError(r.Context(), w, http.StatusNotFound, fmt.Errorf("user %q not found", name), nil)
		return
	}
	if protectedUsers[name] {
		writeError(r.Context(), w, http.StatusForbidden, fmt.Errorf("refusing to modify protected user %q", name), nil)
		return
	}
	if target.UID < system.UIDMin() {
		writeError(r.Context(), w, http.StatusForbidden, fmt.Errorf("refusing to modify system user (uid %d < %d)", target.UID, system.UIDMin()), nil)
		return
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
	out, err := h.runOp("user_modify.yml", map[string]string{
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
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(r.Context(), w, http.StatusInternalServerError, err, steps)
		return
	}
	h.publishUserGroup()
	writeJSON(r.Context(), w, map[string]any{"username": name, "tasks": out.Steps()})
}

// listSSHKeys handles GET /api/users/{name}/sshkeys
func (h *Handler) listSSHKeys(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" || !validUnixName(name) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid username"), nil)
		return
	}
	users, err := system.ListUsers()
	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
		return
	}
	var target *system.User
	for i := range users {
		if users[i].Username == name {
			target = &users[i]
			break
		}
	}
	if target == nil {
		writeError(r.Context(), w, http.StatusNotFound, fmt.Errorf("user %q not found", name), nil)
		return
	}
	keys, err := system.ListAuthorizedKeys(target.Home)
	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
		return
	}
	writeJSON(r.Context(), w, map[string]any{"keys": keys})
}

// addSSHKey handles POST /api/users/{name}/sshkeys
// Body: {"key":"ssh-ed25519 AAAA..."}
func (h *Handler) addSSHKey(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" || !validUnixName(name) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid username"), nil)
		return
	}
	var req struct {
		Key string `json:"key"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}
	req.Key = strings.TrimSpace(req.Key)
	if !validSSHKey(req.Key) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid SSH public key"), nil)
		return
	}

	users, err := system.ListUsers()
	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
		return
	}
	var target *system.User
	for i := range users {
		if users[i].Username == name {
			target = &users[i]
			break
		}
	}
	if target == nil {
		writeError(r.Context(), w, http.StatusNotFound, fmt.Errorf("user %q not found", name), nil)
		return
	}
	if target.UID < system.UIDMin() {
		writeError(r.Context(), w, http.StatusForbidden, fmt.Errorf("refusing to modify system user"), nil)
		return
	}

	out, err := h.runOp("user_ssh_key_add.yml", map[string]string{
		"username": name,
		"home":     target.Home,
		"key":      req.Key,
	})
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(r.Context(), w, http.StatusInternalServerError, err, steps)
		return
	}
	writeJSON(r.Context(), w, map[string]any{"tasks": out.Steps()})
}

// removeSSHKey handles DELETE /api/users/{name}/sshkeys
// Body: {"key":"ssh-ed25519 AAAA..."}
func (h *Handler) removeSSHKey(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" || !validUnixName(name) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid username"), nil)
		return
	}
	var req struct {
		Key string `json:"key"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}
	req.Key = strings.TrimSpace(req.Key)
	if req.Key == "" {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("key is required"), nil)
		return
	}

	users, err := system.ListUsers()
	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
		return
	}
	var target *system.User
	for i := range users {
		if users[i].Username == name {
			target = &users[i]
			break
		}
	}
	if target == nil {
		writeError(r.Context(), w, http.StatusNotFound, fmt.Errorf("user %q not found", name), nil)
		return
	}
	if target.UID < system.UIDMin() {
		writeError(r.Context(), w, http.StatusForbidden, fmt.Errorf("refusing to modify system user"), nil)
		return
	}

	out, err := h.runOp("user_ssh_key_remove.yml", map[string]string{
		"home": target.Home,
		"key":  req.Key,
	})
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(r.Context(), w, http.StatusInternalServerError, err, steps)
		return
	}
	writeJSON(r.Context(), w, map[string]any{"tasks": out.Steps()})
}

// createGroup handles POST /api/groups
// Body: {"groupname":"storage","gid":"1500"}
func (h *Handler) createGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Groupname string `json:"groupname"`
		GID       string `json:"gid"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}
	if req.Groupname == "" {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("groupname is required"), nil)
		return
	}
	if !validUnixName(req.Groupname) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid group name"), nil)
		return
	}

	h.userMu.Lock()
	defer h.userMu.Unlock()
	out, err := h.runOp("group_create.yml", map[string]string{
		"groupname": req.Groupname,
		"gid":       req.GID,
	})
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(r.Context(), w, http.StatusInternalServerError, err, steps)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	h.publishUserGroup()
	_ = json.NewEncoder(w).Encode(map[string]any{"groupname": req.Groupname, "tasks": out.Steps()})
}

// deleteGroup handles DELETE /api/groups/{name}
// Looks up the group's GID and rejects system groups (gid < UIDMin).
func (h *Handler) deleteGroup(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("groupname required"), nil)
		return
	}
	if !validUnixName(name) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid group name"), nil)
		return
	}

	groups, err := system.ListGroups()
	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
		return
	}
	var target *system.Group
	for i := range groups {
		if groups[i].Name == name {
			target = &groups[i]
			break
		}
	}
	if target == nil {
		writeError(r.Context(), w, http.StatusNotFound, fmt.Errorf("group %q not found", name), nil)
		return
	}
	if protectedGroups[name] {
		writeError(r.Context(), w, http.StatusForbidden, fmt.Errorf("refusing to delete protected group %q", name), nil)
		return
	}
	if target.GID < system.UIDMin() {
		writeError(r.Context(), w, http.StatusForbidden, fmt.Errorf("refusing to delete system group (gid %d < %d)", target.GID, system.UIDMin()), nil)
		return
	}

	h.userMu.Lock()
	defer h.userMu.Unlock()
	out, err := h.runOp("group_delete.yml", map[string]string{
		"groupname": name,
		"gid":       fmt.Sprintf("%d", target.GID),
	})
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(r.Context(), w, http.StatusInternalServerError, err, steps)
		return
	}
	h.publishUserGroup()
	writeJSON(r.Context(), w, map[string]any{"groupname": name, "tasks": out.Steps()})
}

// modifyGroup handles PUT /api/groups/{name}
// Body: {"new_name":"newgrp","gid":"1501","members":"alice,bob"}
func (h *Handler) modifyGroup(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("groupname required"), nil)
		return
	}
	if !validUnixName(name) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid group name"), nil)
		return
	}
	var req struct {
		NewName string `json:"new_name"`
		GID     string `json:"gid"`
		Members string `json:"members"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err), nil)
		return
	}
	if req.NewName != "" && !validUnixName(req.NewName) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid new group name"), nil)
		return
	}
	if !validUnixNameList(req.Members) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid member name"), nil)
		return
	}

	groups, err := system.ListGroups()
	if err != nil {
		writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
		return
	}
	var target *system.Group
	for i := range groups {
		if groups[i].Name == name {
			target = &groups[i]
			break
		}
	}
	if target == nil {
		writeError(r.Context(), w, http.StatusNotFound, fmt.Errorf("group %q not found", name), nil)
		return
	}
	if protectedGroups[name] {
		writeError(r.Context(), w, http.StatusForbidden, fmt.Errorf("refusing to modify protected group %q", name), nil)
		return
	}
	if target.GID < system.UIDMin() {
		writeError(r.Context(), w, http.StatusForbidden, fmt.Errorf("refusing to modify system group (gid %d < %d)", target.GID, system.UIDMin()), nil)
		return
	}

	if req.Members != "" {
		users, err := system.ListUsers()
		if err != nil {
			writeError(r.Context(), w, http.StatusInternalServerError, err, nil)
			return
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
			writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("unknown member(s): %s", strings.Join(unknown, ", ")), nil)
			return
		}
	}

	resultName := name
	if req.NewName != "" {
		resultName = req.NewName
	}

	h.userMu.Lock()
	defer h.userMu.Unlock()
	out, err := h.runOp("group_modify.yml", map[string]string{
		"groupname":      name,
		"gid":            fmt.Sprintf("%d", target.GID),
		"new_name":       req.NewName,
		"new_gid":        req.GID,
		"members":        req.Members,
		"update_members": "true",
	})
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(r.Context(), w, http.StatusInternalServerError, err, steps)
		return
	}
	h.publishUserGroup()
	writeJSON(r.Context(), w, map[string]any{"groupname": resultName, "tasks": out.Steps()})
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
