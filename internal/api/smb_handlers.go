package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strconv"

	"dumpstore/internal/ansible"
	"dumpstore/internal/smb"
	"dumpstore/internal/system"
	"dumpstore/internal/zfs"
)

// requireSMBInit returns a 409 error if smb.conf does not exist at the
// expected OS path.
func requireSMBInit() error {
	if !smb.IsInitialized(runtime.GOOS) {
		return errStatus(http.StatusConflict,
			fmt.Errorf("Samba not initialised — run POST /api/v1/smb/init first"))
	}
	return nil
}

// applyConfig renders cfg to a temp file and runs smb_apply.yml to deploy it.
// The temp file is removed after the playbook exits regardless of outcome.
func (h *Handler) applyConfig(ctx context.Context, cfg *smb.SMBConfig) (*ansible.PlaybookOutput, error) {
	tmp, err := os.CreateTemp("", "dumpstore-smb-*.conf")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tmp.Close()
	defer os.Remove(tmp.Name())

	goos := runtime.GOOS
	if err := cfg.RenderToFile(tmp.Name(), goos); err != nil {
		return nil, err
	}

	dirs := cfg.DirsToCreate()
	dirsJSON, _ := json.Marshal(dirs)
	servicesJSON, _ := json.Marshal(smb.ServiceNames(goos))

	// smb_apply.yml reads service status with the OS's own command (#196).
	serviceManager := "systemd"
	if goos == "freebsd" {
		serviceManager = "rc"
	}

	vars := map[string]string{
		"src":             tmp.Name(),
		"smb_conf":        smb.ConfPath(goos),
		"dirs":            string(dirsJSON),
		"samba_services":  string(servicesJSON),
		"service_manager": serviceManager,
	}

	// wsdd announces the host under Samba's workgroup; render its options
	// file alongside smb.conf whenever wsdd is installed.
	if st, ok, conf := wsddTarget(); ok && conf != "" {
		wtmp, err := os.CreateTemp("", "dumpstore-wsdd-*")
		if err != nil {
			return nil, fmt.Errorf("create temp file: %w", err)
		}
		wtmp.Close()
		defer os.Remove(wtmp.Name())
		if err := cfg.RenderWSDDToFile(wtmp.Name()); err != nil {
			return nil, err
		}
		vars["wsdd_src"] = wtmp.Name()
		vars["wsdd_conf"] = conf
		vars["wsdd_unit"] = st.UnitName
		vars["wsdd_restart"] = strconv.FormatBool(st.Active)
	}

	return h.runOp(ctx, "smb_apply.yml", vars)
}

// wsddTarget returns the wsdd service status, whether it is installed, and
// the options file dumpstore renders for it. confPath is "" on FreeBSD (the
// rc.d script reads the workgroup from smb4.conf) and for Linux units that do
// not take their arguments from $WSDD_PARAMS (left untouched).
func wsddTarget() (st system.ServiceStatus, installed bool, confPath string) {
	st, _ = system.LookupService("wsdd")
	installed = st.Installed
	if installed && runtime.GOOS != "freebsd" {
		confPath = system.UnitParamsFile(st.UnitName, smb.WSDDParamsVar)
	}
	return st, installed, confPath
}

// WSDDStatus is the wsdd part of GET /api/v1/smb/status. When wsdd is not
// installed only installed and install_hint are present; otherwise
// install_hint is absent and the WSDDService fields are present.
type WSDDStatus struct {
	Installed   bool   `json:"installed"`
	InstallHint string `json:"install_hint,omitempty" doc:"Package-manager command to install wsdd (only when not installed)"`
	*WSDDService
}

// WSDDService describes an installed wsdd service.
type WSDDService struct {
	Unit       string `json:"unit"`
	State      string `json:"state"`
	Active     bool   `json:"active"`
	Enabled    bool   `json:"enabled"`
	ConfPath   string `json:"conf_path" doc:"Options file dumpstore renders; empty on FreeBSD and for units that do not read $WSDD_PARAMS"`
	Manageable bool   `json:"manageable"`
	Managed    bool   `json:"managed"`
	Workgroup  string `json:"workgroup"`
}

// wsddStatus describes wsdd for GET /api/v1/smb/status.
func wsddStatus(cfg *smb.SMBConfig) *WSDDStatus {
	st, installed, conf := wsddTarget()
	if !installed {
		return &WSDDStatus{Installed: false, InstallHint: system.WSDDInstallHint()}
	}
	freebsd := runtime.GOOS == "freebsd"
	return &WSDDStatus{
		Installed: true,
		WSDDService: &WSDDService{
			Unit:     st.UnitName,
			State:    st.State,
			Active:   st.Active,
			Enabled:  st.Enabled,
			ConfPath: conf,
			// FreeBSD: the rc.d script derives the workgroup from smb4.conf.
			Manageable: freebsd || conf != "",
			Managed:    freebsd || (conf != "" && smb.WSDDManaged(conf)),
			Workgroup:  cfg.WSDDWorkgroup(),
		},
	}
}

// applyWSDD handles POST /api/v1/smb/wsdd/apply
// Re-applies the current Samba config so wsdd's options file is rendered —
// for hosts where wsdd was installed after Samba was initialised.
func (h *Handler) applyWSDD(ctx context.Context, _ struct{}) (TaskResult, error) {
	if err := requireSMBInit(); err != nil {
		return TaskResult{}, err
	}
	h.smbMu.Lock()
	defer h.smbMu.Unlock()

	if _, ok, conf := wsddTarget(); !ok || conf == "" {
		return TaskResult{}, errStatus(http.StatusConflict,
			fmt.Errorf("wsdd is not installed or its unit does not read $%s", smb.WSDDParamsVar))
	}
	cfg, err := smb.ParseSMBConfig(runtime.GOOS)
	if err != nil {
		return TaskResult{}, err
	}
	out, err := h.applyConfig(ctx, &cfg)
	auditLog(ctx, "smb.wsdd", "", err)
	if err != nil {
		return TaskResult{}, playbookError(err, out)
	}
	return TaskResult{Tasks: out.Steps()}, nil
}

// SMBStatus is the GET /api/v1/smb/status response.
type SMBStatus struct {
	Initialized bool        `json:"initialized"`
	ConfPath    string      `json:"conf_path"`
	OS          string      `json:"os"`
	ConfMtime   string      `json:"conf_mtime,omitempty" doc:"smb.conf modification time (RFC 3339, UTC); absent if the file does not exist"`
	WSDD        *WSDDStatus `json:"wsdd,omitempty" doc:"Absent if smb.conf cannot be parsed"`
}

// getSMBStatus handles GET /api/v1/smb/status
func (h *Handler) getSMBStatus(context.Context, struct{}) (SMBStatus, error) {
	goos := runtime.GOOS
	confPath := smb.ConfPath(goos)
	resp := SMBStatus{
		Initialized: smb.IsInitialized(goos),
		ConfPath:    confPath,
		OS:          goos,
	}
	if fi, err := os.Stat(confPath); err == nil {
		resp.ConfMtime = fi.ModTime().UTC().Format("2006-01-02T15:04:05Z")
	}
	if cfg, err := smb.ParseSMBConfig(goos); err == nil {
		resp.WSDD = wsddStatus(&cfg)
	}
	return resp, nil
}

// SMBInitResult is the POST /api/v1/smb/init response.
type SMBInitResult struct {
	Initialized bool               `json:"initialized"`
	Tasks       []ansible.TaskStep `json:"tasks"`
}

// initSamba handles POST /api/v1/smb/init
// Checks smbd is installed, backs up any existing config, creates the
// usershares directory, then writes dumpstore's own smb.conf from template.
// Preserves workgroup and server string from an existing config if present.
// Safe to call multiple times.
func (h *Handler) initSamba(ctx context.Context, _ struct{}) (SMBInitResult, error) {
	h.smbMu.Lock()
	defer h.smbMu.Unlock()

	goos := runtime.GOOS

	// Fail fast with a distro-specific install hint if smbd is missing.
	if hint := system.SambaInstallHint(); hint != "" {
		return SMBInitResult{}, errStatus(http.StatusUnprocessableEntity,
			fmt.Errorf("Samba not installed — run: %s", hint))
	}

	// Parse existing config to carry over workgroup/server string if present.
	// Falls back to defaults if the file doesn't exist yet.
	cfg, err := smb.ParseSMBConfig(goos)
	if err != nil {
		return SMBInitResult{}, err
	}

	// Step 1: back up the existing conf, create the sambashare group and usershares dir.
	out1, err := h.runOp(ctx, "smb_init.yml", map[string]string{})
	auditLog(ctx, "smb.init", "", err)
	if err != nil {
		return SMBInitResult{}, playbookError(err, out1)
	}

	// Step 2: write our own clean smb.conf from template.
	out2, err := h.applyConfig(ctx, &cfg)
	if err != nil {
		var steps []ansible.TaskStep
		steps = append(steps, out1.Steps()...)
		if out2 != nil {
			steps = append(steps, out2.Steps()...)
		}
		return SMBInitResult{}, opError(err, steps)
	}

	var allSteps []ansible.TaskStep
	allSteps = append(allSteps, out1.Steps()...)
	allSteps = append(allSteps, out2.Steps()...)

	return SMBInitResult{Initialized: smb.IsInitialized(goos), Tasks: allSteps}, nil
}

// getSMBShares handles GET /api/v1/smb/shares
func (h *Handler) getSMBShares(context.Context, struct{}) ([]smb.Usershare, error) {
	if err := requireSMBInit(); err != nil {
		return nil, err
	}
	// Read the share files directly: `net usershare info` hides shares whose
	// path is gone, and those re-activate when the path reappears (#183).
	return smb.ListUsershares(smb.UsershareDir(runtime.GOOS))
}

// SMBShareResult is the result of adding or removing a dataset's usershare.
type SMBShareResult struct {
	Dataset   string             `json:"dataset"`
	Sharename string             `json:"sharename"`
	Tasks     []ansible.TaskStep `json:"tasks"`
}

// SetSMBShareRequest is the PUT /api/v1/smb/shares/{name} body.
type SetSMBShareRequest struct {
	Dataset string `json:"dataset" doc:"Dataset whose mountpoint is shared"`
}

// setSMBShare handles PUT /api/v1/smb/shares/{name}
func (h *Handler) setSMBShare(ctx context.Context, in struct {
	shareNameParam
	Body SetSMBShareRequest `body:""`
}) (SMBShareResult, error) {
	if err := requireSMBInit(); err != nil {
		return SMBShareResult{}, err
	}
	dataset := in.Body.Dataset
	if dataset == "" {
		return SMBShareResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("dataset name required"))
	}
	if !validZFSName(dataset) {
		return SMBShareResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid dataset name"))
	}
	req := struct{ Sharename string }{in.Sharename}
	if req.Sharename == "" {
		return SMBShareResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("sharename is required"))
	}
	if !validSMBShare(req.Sharename) {
		return SMBShareResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid sharename"))
	}
	h.refMu.Lock() // the dataset must not be destroyed or moved while the share is added (#183)
	defer h.refMu.Unlock()
	out, err := h.runOp(ctx, "smb_usershare_set.yml", map[string]string{
		"dataset":   dataset,
		"sharename": req.Sharename,
	})
	auditLog(ctx, "smb_share.set", dataset, err)
	if err != nil {
		return SMBShareResult{}, playbookError(err, out)
	}
	return SMBShareResult{Dataset: dataset, Sharename: req.Sharename, Tasks: out.Steps()}, nil
}

// deleteSMBShare handles DELETE /api/v1/smb/shares/{name}
func (h *Handler) deleteSMBShare(ctx context.Context, in deleteSMBShareParams) (SMBShareRemoved, error) {
	if err := requireSMBInit(); err != nil {
		return SMBShareRemoved{}, err
	}
	sharename := in.Name
	if !validSMBShare(sharename) {
		return SMBShareRemoved{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid sharename"))
	}
	shares, err := smb.ListUsershares(smb.UsershareDir(runtime.GOOS))
	if err != nil {
		return SMBShareRemoved{}, err
	}
	i := slices.IndexFunc(shares, func(s smb.Usershare) bool { return s.Name == sharename })
	if i < 0 {
		return SMBShareRemoved{}, errStatus(http.StatusNotFound, fmt.Errorf("SMB share %q not found", sharename))
	}
	path := shares[i].Path
	out, err := h.runOp(ctx, "smb_usershare_unset.yml", map[string]string{
		"sharename": sharename,
	})
	auditLog(ctx, "smb_share.delete", sharename+" ("+path+")", err)
	if err != nil {
		return SMBShareRemoved{}, playbookError(err, out)
	}
	return SMBShareRemoved{Sharename: sharename, Path: path, Tasks: out.Steps()}, nil
}

// SMBShareRemoved is the DELETE /api/v1/smb/shares/{name} response.
type SMBShareRemoved struct {
	Sharename string             `json:"sharename"`
	Path      string             `json:"path" doc:"Path the share pointed to"`
	Tasks     []ansible.TaskStep `json:"tasks"`
}

// SambaUserList is the GET /api/v1/smb/users response.
type SambaUserList struct {
	Available bool     `json:"available" doc:"false if Samba is not available; users is then empty"`
	Users     []string `json:"users"`
}

// getSambaUsers handles GET /api/v1/smb/users
func (h *Handler) getSambaUsers(context.Context, struct{}) (SambaUserList, error) {
	users, err := system.ListSambaUsers()
	if errors.Is(err, system.ErrSambaNotAvailable) {
		return SambaUserList{Available: false, Users: []string{}}, nil
	}
	if err != nil {
		return SambaUserList{}, err
	}
	if users == nil {
		users = []string{}
	}
	return SambaUserList{Available: true, Users: users}, nil
}

// AddSambaUserRequest is the PUT /api/v1/smb/users/{name} body.
type AddSambaUserRequest struct {
	Password string `json:"password" doc:"Samba password; must not contain newlines"`
}

// addSambaUser handles PUT /api/v1/smb/users/{name}
func (h *Handler) addSambaUser(ctx context.Context, in struct {
	smbUserParam
	Body AddSambaUserRequest `body:""`
}) (UserResult, error) {
	if err := requireSMBInit(); err != nil {
		return UserResult{}, err
	}
	name := in.Name
	if name == "" {
		return UserResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("username required"))
	}
	if !validUnixName(name) {
		return UserResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid username"))
	}
	req := in.Body
	if req.Password == "" {
		return UserResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("password is required"))
	}
	if !safePassword(req.Password) {
		return UserResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("password must not contain newline characters"))
	}
	h.userMu.Lock()
	defer h.userMu.Unlock()
	out, err := h.runOp(ctx, "smb_user_add.yml", map[string]string{
		"username":     name,
		"smb_password": req.Password,
	})
	auditLog(ctx, "smb_user.add", name, err)
	if err != nil {
		return UserResult{}, playbookError(err, out)
	}
	return UserResult{Username: name, Tasks: out.Steps()}, nil
}

// removeSambaUser handles DELETE /api/v1/smb/users/{name}
func (h *Handler) removeSambaUser(ctx context.Context, in smbUserParam) (UserResult, error) {
	if err := requireSMBInit(); err != nil {
		return UserResult{}, err
	}
	name := in.Name
	if name == "" {
		return UserResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("username required"))
	}
	if !validUnixName(name) {
		return UserResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid username"))
	}
	h.userMu.Lock()
	defer h.userMu.Unlock()
	out, err := h.runOp(ctx, "smb_user_remove.yml", map[string]string{
		"username": name,
	})
	auditLog(ctx, "smb_user.remove", name, err)
	if err != nil {
		return UserResult{}, playbookError(err, out)
	}
	return UserResult{Username: name, Tasks: out.Steps()}, nil
}

// SMBHomesShare describes an enabled [homes] share.
type SMBHomesShare struct {
	Path          string `json:"path"`
	Browseable    string `json:"browseable"`
	ReadOnly      string `json:"read_only"`
	CreateMask    string `json:"create_mask"`
	DirectoryMask string `json:"directory_mask"`
}

// SMBHomes is the GET /api/v1/smb/homes response: only enabled when [homes]
// is disabled, otherwise enabled plus the SMBHomesShare fields.
type SMBHomes struct {
	Enabled bool `json:"enabled"`
	*SMBHomesShare
}

// getSMBHomes handles GET /api/v1/smb/homes
func (h *Handler) getSMBHomes(context.Context, struct{}) (SMBHomes, error) {
	cfg, err := smb.ParseSMBConfig(runtime.GOOS)
	if err != nil {
		return SMBHomes{}, err
	}
	if cfg.Homes == nil {
		return SMBHomes{Enabled: false}, nil
	}
	return SMBHomes{Enabled: true, SMBHomesShare: &SMBHomesShare{
		Path:          cfg.Homes.Path,
		Browseable:    cfg.Homes.Browseable,
		ReadOnly:      cfg.Homes.ReadOnly,
		CreateMask:    cfg.Homes.CreateMask,
		DirectoryMask: cfg.Homes.DirectoryMask,
	}}, nil
}

// SetSMBHomesRequest is the PUT /api/v1/smb/homes body.
type SetSMBHomesRequest struct {
	Dataset       string `json:"dataset,omitempty" doc:"Dataset whose mountpoint + /%U becomes the path (used when path is empty)"`
	Path          string `json:"path,omitempty" doc:"Share path; required unless dataset is given"`
	Browseable    string `json:"browseable,omitempty" enum:"yes,no" doc:"Default no"`
	ReadOnly      string `json:"read_only,omitempty" enum:"yes,no" doc:"Default no"`
	CreateMask    string `json:"create_mask,omitempty" doc:"3- or 4-digit octal (default 0644)"`
	DirectoryMask string `json:"directory_mask,omitempty" doc:"3- or 4-digit octal (default 0755)"`
}

// SetSMBHomesResult is the PUT /api/v1/smb/homes response.
type SetSMBHomesResult struct {
	Enabled bool `json:"enabled"`
	SMBHomesShare
	Tasks []ansible.TaskStep `json:"tasks"`
}

// setSMBHomes handles PUT /api/v1/smb/homes
func (h *Handler) setSMBHomes(ctx context.Context, in struct {
	Body SetSMBHomesRequest `body:""`
}) (SetSMBHomesResult, error) {
	if err := requireSMBInit(); err != nil {
		return SetSMBHomesResult{}, err
	}
	req := in.Body

	// Held from mountpoint lookup through apply so the dataset can't be
	// destroyed or moved in between (#183).
	h.refMu.Lock()
	defer h.refMu.Unlock()

	// Resolve dataset → mountpoint if path not supplied directly
	if req.Dataset != "" && req.Path == "" {
		if !validZFSName(req.Dataset) {
			return SetSMBHomesResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid dataset name"))
		}
		mp, err := datasetMountpoint(req.Dataset)
		if err != nil {
			return SetSMBHomesResult{}, err
		}
		if mp == "" {
			return SetSMBHomesResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("dataset %q has no mountpoint", req.Dataset))
		}
		req.Path = mp + "/%U"
	}
	if req.Path == "" {
		return SetSMBHomesResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("path is required (or provide dataset)"))
	}

	// Apply defaults
	if req.Browseable == "" {
		req.Browseable = "no"
	}
	if req.ReadOnly == "" {
		req.ReadOnly = "no"
	}
	if req.CreateMask == "" {
		req.CreateMask = "0644"
	}
	if req.DirectoryMask == "" {
		req.DirectoryMask = "0755"
	}

	if req.Browseable != "yes" && req.Browseable != "no" {
		return SetSMBHomesResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("browseable must be yes or no"))
	}
	if req.ReadOnly != "yes" && req.ReadOnly != "no" {
		return SetSMBHomesResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("read_only must be yes or no"))
	}
	if !reOctalMask.MatchString(req.CreateMask) {
		return SetSMBHomesResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("create_mask must be a 3- or 4-digit octal value"))
	}
	if !reOctalMask.MatchString(req.DirectoryMask) {
		return SetSMBHomesResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("directory_mask must be a 3- or 4-digit octal value"))
	}

	h.smbMu.Lock()
	defer h.smbMu.Unlock()

	cfg, err := smb.ParseSMBConfig(runtime.GOOS)
	if err != nil {
		return SetSMBHomesResult{}, err
	}
	cfg.Homes = &smb.SMBHomesConfig{
		Path:          req.Path,
		Browseable:    req.Browseable,
		ReadOnly:      req.ReadOnly,
		CreateMask:    req.CreateMask,
		DirectoryMask: req.DirectoryMask,
	}

	out, err := h.applyConfig(ctx, &cfg)
	auditLog(ctx, "smb_homes.set", req.Dataset, err)
	if err != nil {
		return SetSMBHomesResult{}, playbookError(err, out)
	}
	return SetSMBHomesResult{
		Enabled: true,
		SMBHomesShare: SMBHomesShare{
			Path:          req.Path,
			Browseable:    req.Browseable,
			ReadOnly:      req.ReadOnly,
			CreateMask:    req.CreateMask,
			DirectoryMask: req.DirectoryMask,
		},
		Tasks: out.Steps(),
	}, nil
}

// deleteSMBHomes handles DELETE /api/v1/smb/homes
func (h *Handler) deleteSMBHomes(ctx context.Context, _ struct{}) (TaskResult, error) {
	if err := requireSMBInit(); err != nil {
		return TaskResult{}, err
	}
	h.smbMu.Lock()
	defer h.smbMu.Unlock()

	cfg, err := smb.ParseSMBConfig(runtime.GOOS)
	if err != nil {
		return TaskResult{}, err
	}
	cfg.Homes = nil

	out, err := h.applyConfig(ctx, &cfg)
	auditLog(ctx, "smb_homes.delete", "", err)
	if err != nil {
		return TaskResult{}, playbookError(err, out)
	}
	return TaskResult{Tasks: out.Steps()}, nil
}

// getTimeMachineShares handles GET /api/v1/smb/timemachine
func (h *Handler) getTimeMachineShares(context.Context, struct{}) ([]smb.TimeMachineShare, error) {
	cfg, err := smb.ParseSMBConfig(runtime.GOOS)
	if err != nil {
		return nil, err
	}
	shares := cfg.TimeMachine
	if shares == nil {
		shares = []smb.TimeMachineShare{}
	}
	return shares, nil
}

// CreateTimeMachineShareRequest is the PUT /api/v1/smb/timemachine/{name} body.
type CreateTimeMachineShareRequest struct {
	Dataset    string `json:"dataset,omitempty" doc:"Dataset whose mountpoint becomes the path (used when path is empty)"`
	Path       string `json:"path,omitempty" doc:"Share path; required unless dataset is given"`
	MaxSize    string `json:"max_size,omitempty" doc:"Time Machine size limit, e.g. 500G"`
	ValidUsers string `json:"valid_users,omitempty" doc:"Samba valid users list"`
}

// TimeMachineSharesResult is the PUT /api/v1/smb/timemachine/{name} response.
type TimeMachineSharesResult struct {
	Shares []smb.TimeMachineShare `json:"shares" doc:"All Time Machine shares after the change"`
	Tasks  []ansible.TaskStep     `json:"tasks"`
}

// createTimeMachineShare handles PUT /api/v1/smb/timemachine/{name}
func (h *Handler) createTimeMachineShare(ctx context.Context, in struct {
	shareNameParam
	Body CreateTimeMachineShareRequest `body:""`
}) (TimeMachineSharesResult, error) {
	if err := requireSMBInit(); err != nil {
		return TimeMachineSharesResult{}, err
	}
	req := struct {
		CreateTimeMachineShareRequest
		Sharename string
	}{in.Body, in.Sharename}
	if req.Sharename == "" {
		return TimeMachineSharesResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("sharename is required"))
	}
	if !validSMBShare(req.Sharename) {
		return TimeMachineSharesResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid share name"))
	}

	// Held from mountpoint lookup through apply so the dataset can't be
	// destroyed or moved in between (#183).
	h.refMu.Lock()
	defer h.refMu.Unlock()

	// Resolve dataset → mountpoint
	if req.Dataset != "" && req.Path == "" {
		if !validZFSName(req.Dataset) {
			return TimeMachineSharesResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid dataset name"))
		}
		mp, err := datasetMountpoint(req.Dataset)
		if err != nil {
			return TimeMachineSharesResult{}, err
		}
		if mp == "" {
			return TimeMachineSharesResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("dataset %q has no mountpoint", req.Dataset))
		}
		req.Path = mp
	}
	if req.Path == "" {
		return TimeMachineSharesResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("path is required (or provide dataset)"))
	}

	h.smbMu.Lock()
	defer h.smbMu.Unlock()

	cfg, err := smb.ParseSMBConfig(runtime.GOOS)
	if err != nil {
		return TimeMachineSharesResult{}, err
	}

	// Replace if share with same name exists, otherwise append
	newShare := smb.TimeMachineShare{
		Name:       req.Sharename,
		Path:       req.Path,
		MaxSize:    req.MaxSize,
		ValidUsers: req.ValidUsers,
	}
	replaced := false
	for i, s := range cfg.TimeMachine {
		if s.Name == req.Sharename {
			cfg.TimeMachine[i] = newShare
			replaced = true
			break
		}
	}
	if !replaced {
		cfg.TimeMachine = append(cfg.TimeMachine, newShare)
	}

	out, err := h.applyConfig(ctx, &cfg)
	auditLog(ctx, "smb_timemachine.create", req.Dataset, err)
	if err != nil {
		return TimeMachineSharesResult{}, playbookError(err, out)
	}
	shares := cfg.TimeMachine
	if shares == nil {
		shares = []smb.TimeMachineShare{}
	}
	return TimeMachineSharesResult{Shares: shares, Tasks: out.Steps()}, nil
}

// deleteTimeMachineShare handles DELETE /api/v1/smb/timemachine/{name}
func (h *Handler) deleteTimeMachineShare(ctx context.Context, in shareNameParam) (TaskResult, error) {
	if err := requireSMBInit(); err != nil {
		return TaskResult{}, err
	}
	sharename := in.Sharename
	if sharename == "" {
		return TaskResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("sharename is required"))
	}
	if !validSMBShare(sharename) {
		return TaskResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid share name"))
	}

	h.smbMu.Lock()
	defer h.smbMu.Unlock()

	cfg, err := smb.ParseSMBConfig(runtime.GOOS)
	if err != nil {
		return TaskResult{}, err
	}

	filtered := cfg.TimeMachine[:0]
	for _, s := range cfg.TimeMachine {
		if s.Name != sharename {
			filtered = append(filtered, s)
		}
	}
	cfg.TimeMachine = filtered

	out, err := h.applyConfig(ctx, &cfg)
	auditLog(ctx, "smb_timemachine.delete", sharename, err)
	if err != nil {
		return TaskResult{}, playbookError(err, out)
	}
	return TaskResult{Tasks: out.Steps()}, nil
}

// datasetMountpoint looks up the mountpoint for the named dataset.
// Returns "" if the dataset is not found or has no usable mountpoint.
func datasetMountpoint(dataset string) (string, error) {
	datasets, err := zfs.ListDatasets()
	if err != nil {
		return "", fmt.Errorf("list datasets: %w", err)
	}
	for _, ds := range datasets {
		if ds.Name == dataset {
			if !zfs.HasMountpoint(ds.Mountpoint) {
				return "", nil
			}
			return ds.Mountpoint, nil
		}
	}
	return "", nil
}
