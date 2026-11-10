package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"dumpstore/internal/ansible"
	"dumpstore/internal/auth"
	"dumpstore/internal/autosnap"
	"dumpstore/internal/broker"
	"dumpstore/internal/jobs"
	"dumpstore/internal/operations"
	"dumpstore/internal/ops"
	"dumpstore/internal/replication"
	"dumpstore/internal/schema"
	"dumpstore/internal/system"
	"dumpstore/internal/zfs"
)

// Input validation: whitelist regexes are stricter than a denylist and eliminate
// entire classes of injection (newlines, glob expansion, shell metacharacters, etc.).

var (
	// reZFSName matches a valid ZFS dataset/pool path: pool or pool/a/b/c
	// Components: letters, digits, underscore, hyphen, period, colon.
	// Each component must begin with a letter — ZFS rejects numeric-only components like "pool/123".
	reZFSName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_.:-]*(/[a-zA-Z][a-zA-Z0-9_.:-]*)*$`)

	// reSnapLabel matches the label part of a snapshot name (after the '@').
	reSnapLabel = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:-]*$`)

	// reUnixName matches a valid POSIX username or group name.
	reUnixName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9._-]{0,31}$`)

	// reSMBShare matches a valid SMB share name (max 80 chars).
	reSMBShare = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,80}$`)

	// reZFSSize matches a valid ZFS size value: a positive integer with an
	// optional K/M/G/T/P/E suffix (case-insensitive), optionally followed by B.
	// Capped at 18 digits to reject values that are astronomically large.
	reZFSSize = regexp.MustCompile(`(?i)^[1-9][0-9]{0,17}[KMGTPE]?B?$`)

	// reShellPath matches a safe absolute filesystem path.
	reShellPath = regexp.MustCompile(`^/[a-zA-Z0-9/_.-]+$`)

	// reIQN matches a valid iSCSI Qualified Name (RFC 3720).
	reIQN = regexp.MustCompile(`^iqn\.\d{4}-\d{2}\.[a-z0-9.-]+:[a-zA-Z0-9._:-]+$`)

	// reOctalMask matches a 3- or 4-digit octal permission mask.
	reOctalMask = regexp.MustCompile(`^[0-7]{3,4}$`)

	// reSSHKeyType matches the key-type prefix of an SSH public key.
	reSSHKeyType = regexp.MustCompile(`^(ssh-|ecdsa-sha2-|sk-)`)

	// reRemoteSpec matches a `user@host` SSH destination. Host may be a
	// hostname or IPv4 literal; IPv6 in brackets is not supported.
	reRemoteSpec = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9._-]*@[a-zA-Z0-9.-]+$`)

	// reVdevName matches a vdev identifier as accepted by zpool commands:
	// a device path (/dev/sdb), a short device name (sdb, gpt/data,
	// ata-FOO_BAR-part1) or a numeric guid printed by `zpool status` for
	// missing devices.
	reVdevName = regexp.MustCompile(`^/?[a-zA-Z0-9][a-zA-Z0-9/_.:-]*$`)

	// reCompressionValue matches a ZFS compression property value
	// (off, on, lz4, zstd, zstd-fast, gzip-9, …).
	reCompressionValue = regexp.MustCompile(`^[a-z0-9-]+$`)
)

// validZFSName returns true if s is a safe ZFS dataset/pool path (no snapshot suffix).
func validZFSName(s string) bool { return reZFSName.MatchString(s) }

// validSnapLabel returns true if s is a safe snapshot label (the part after '@').
func validSnapLabel(s string) bool { return reSnapLabel.MatchString(s) }

// validUnixName returns true if s is a valid POSIX username or group name.
func validUnixName(s string) bool { return reUnixName.MatchString(s) }

// validSMBShare returns true if s is a valid SMB share name.
func validSMBShare(s string) bool { return reSMBShare.MatchString(s) }

// validShellPath returns true if s is a safe absolute path.
func validShellPath(s string) bool { return reShellPath.MatchString(s) }

// validRemoteSpec returns true if s is a safe `user@host` SSH destination.
func validRemoteSpec(s string) bool { return reRemoteSpec.MatchString(s) }

// validVdevName returns true if s is a safe vdev identifier (device path,
// short device name, or guid).
func validVdevName(s string) bool { return reVdevName.MatchString(s) }

// validPoolName returns true if s is a safe pool name (no dataset path).
func validPoolName(s string) bool { return validZFSName(s) && !strings.Contains(s, "/") }

// validSSHKey returns true if s looks like an SSH public key (single line, known prefix).
func validSSHKey(s string) bool {
	return !strings.ContainsAny(s, "\n\r") && reSSHKeyType.MatchString(s)
}

// safePropertyValue returns true if s contains no shell-dangerous or control
// characters. Used for dataset property values which are legitimately complex
// (e.g. sharenfs="rw=@10.0.0.0/24") and cannot be matched with a simple whitelist.
func safePropertyValue(s string) bool {
	return !strings.ContainsAny(s, ";\n\r`|&$*()?!~{}\\\"'")
}

// safePassword returns true if s contains no newline or carriage-return characters.
// Newlines corrupt the chpasswd and smbpasswd stdin input which is line-delimited.
func safePassword(s string) bool {
	return !strings.ContainsAny(s, "\n\r")
}

// validZFSSize returns true if s is a valid ZFS size string (e.g. "10G", "500M", "1TB")
// or the special value "none" (which clears quota/reservation properties).
func validZFSSize(s string) bool {
	return s == "none" || reZFSSize.MatchString(s)
}

// validUnixNameList returns true if s is empty or a comma-separated list of valid POSIX names.
func validUnixNameList(s string) bool {
	if s == "" {
		return true
	}
	for _, name := range strings.Split(s, ",") {
		if !validUnixName(strings.TrimSpace(name)) {
			return false
		}
	}
	return true
}

// decodeJSON decodes a single JSON value from r.Body into v.
// It rejects requests with trailing non-whitespace data after the JSON value.
func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("request body must contain a single JSON value")
	}
	return nil
}

// datasetExists reports whether a dataset with the given name exists.
func datasetExists(name string) (bool, error) {
	datasets, err := zfs.ListDatasets()
	if err != nil {
		return false, err
	}
	for _, d := range datasets {
		if d.Name == name {
			return true, nil
		}
	}
	return false, nil
}

// snapshotExists reports whether a snapshot with the given name exists.
func snapshotExists(name string) (bool, error) {
	snaps, err := zfs.ListSnapshots()
	if err != nil {
		return false, err
	}
	for _, s := range snaps {
		if s.Name == name {
			return true, nil
		}
	}
	return false, nil
}

// apiError is returned as JSON for all non-2xx responses.
// Tasks is populated for Ansible-backed operations so the UI can show the op-log
// even when the request fails. Conflicts is populated for 409s from the
// dataset reference guard (#183).
type apiError struct {
	Error     string             `json:"error"`
	Tasks     []ansible.TaskStep `json:"tasks,omitempty"`
	Conflicts []refConflict      `json:"conflicts,omitempty"`
}

// Handler holds dependencies for the HTTP API.
type Handler struct {
	runner     *ansible.Runner
	ops        *ops.Runner
	operations *operations.Registry // write-operation records + tagged progress (#184)
	version    string
	broker     *broker.Broker
	jobs       *jobs.Manager
	repl       *replication.Runner
	autosnap   *autosnap.Runner
	userMu     sync.Mutex   // serialises user/group write ops to avoid /etc/group lock contention
	smbMu      sync.Mutex   // serialises smb.conf read-modify-write cycles
	refMu      sync.Mutex   // serialises dataset destroy/rename/re-mount against adding references to datasets (#183); taken before smbMu and iscsiMu
	iscsiMu    sync.Mutex   // serialises ctl.conf read-modify-write cycles (#186)
	authMu     sync.RWMutex // protects concurrent access to authCfg fields
	authCfg    *auth.Config
	authStore  *auth.SessionStore
	configPath string
	otelInfo   any          // effective OTEL status for sysinfo; injected via SetOtelInfo
	metrics    http.Handler // Prometheus exposition for /metrics; injected via SetMetricsHandler
}

// NewHandler creates a Handler with the given dependencies.
func NewHandler(runner *ansible.Runner, version string, b *broker.Broker, jm *jobs.Manager, repl *replication.Runner, autosnap *autosnap.Runner, authCfg *auth.Config, authStore *auth.SessionStore, configPath string) *Handler {
	registerRuntimeMetrics(version)
	return &Handler{
		runner:     runner,
		ops:        ops.NewRunner(),
		operations: operations.New(b.PublishNoCache),
		version:    version,
		broker:     b,
		jobs:       jm,
		repl:       repl,
		autosnap:   autosnap,
		authCfg:    authCfg,
		authStore:  authStore,
		configPath: configPath,
	}
}

// AuthCfgRead calls fn while holding the authCfg read lock.
func (h *Handler) AuthCfgRead(fn func(*auth.Config)) {
	h.authMu.RLock()
	defer h.authMu.RUnlock()
	fn(h.authCfg)
}

// AuthCfgWrite calls fn while holding the authCfg write lock.
func (h *Handler) AuthCfgWrite(fn func(*auth.Config)) {
	h.authMu.Lock()
	defer h.authMu.Unlock()
	fn(h.authCfg)
}

// auditLog emits a structured audit log line for every mutating API operation.
// req_id is injected automatically by the journalHandler middleware.
// Since the service has no authentication, operator identity is the client IP.
func auditLog(ctx context.Context, action, target string, err error) {
	args := []any{"remote_ip", remoteAddr(ctx), "action", action, "target", target, "outcome", "ok"}
	if err != nil {
		args[7] = "err"
		args = append(args, "error", err.Error())
	}
	slog.InfoContext(ctx, "audit", args...)
}

// runOp executes a playbook and records each task step under the request's
// operation (#184), which publishes it to the ansible.progress SSE topic
// tagged with the operation ID and seq so the frontend can show live
// progress. ctx is used for trace-span parenting and the operation ID only;
// the runner drops its cancellation.
func (h *Handler) runOp(ctx context.Context, playbook string, vars map[string]string) (*ansible.PlaybookOutput, error) {
	id := operations.IDFromContext(ctx)
	return h.runner.RunStreaming(ctx, playbook, vars, func(step ansible.TaskStep) {
		h.operations.Step(id, step)
	})
}

// runLocal executes argv steps through the in-process ops layer (#115),
// recording/publishing each step exactly like runOp so the frontend's live
// op-log works identically to playbook-backed writes.
func (h *Handler) runLocal(ctx context.Context, steps ...ops.Step) (*ops.Result, error) {
	id := operations.IDFromContext(ctx)
	return h.ops.Run(ctx, steps, func(step ansible.TaskStep) {
		h.operations.Step(id, step)
	})
}

// SetOtelInfo injects the effective OpenTelemetry status for the sysinfo
// response. Set once at startup by main; this package cannot import
// internal/otel (cycle via internal/logging).
func (h *Handler) SetOtelInfo(v any) { h.otelInfo = v }

// SysInfo is the GET /api/v1/system/info response.
type SysInfo struct {
	AppVersion string `json:"app_version"`
	Otel       any    `json:"otel,omitempty"`
	system.Info
}

func (h *Handler) getSysInfo(context.Context, struct{}) (SysInfo, error) {
	return SysInfo{AppVersion: h.version, Otel: h.otelInfo, Info: system.Get()}, nil
}

func (h *Handler) getNetwork(context.Context, struct{}) ([]system.NetworkInterface, error) {
	return system.GetNetworkInterfaces()
}

func (h *Handler) getVersion(ctx context.Context, _ struct{}) (VersionInfo, error) {
	v, err := zfs.Version()
	if err != nil {
		slog.WarnContext(ctx, "zpool version failed", "err", err)
		v = ""
	}
	return VersionInfo{Version: v}, nil
}

// VersionInfo is the GET /api/v1/system/version response.
type VersionInfo struct {
	Version string `json:"version" doc:"OpenZFS version; empty when it cannot be determined"`
}

// getEvents handles GET /api/v1/events?topics=pool.query,dataset.query,...
// It streams Server-Sent Events to the client until the connection is closed.
func (h *Handler) getEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(r.Context(), w, http.StatusInternalServerError, fmt.Errorf("streaming not supported by this transport"), nil)
		return
	}

	// Parse and validate requested topics; unknown names are silently ignored.
	var topics []string
	for _, t := range strings.Split(r.URL.Query().Get("topics"), ",") {
		t = strings.TrimSpace(t)
		if broker.ValidTopics[t] {
			topics = append(topics, t)
		}
	}
	if len(topics) == 0 {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("no valid topics requested"), nil)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // tell Nginx not to buffer this stream
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Subscribe to each requested topic.
	channels := make(map[string]chan []byte, len(topics))
	for _, t := range topics {
		channels[t] = h.broker.Subscribe(t)
	}
	defer func() {
		for t, ch := range channels {
			h.broker.Unsubscribe(t, ch)
		}
	}()

	// Fan-in: one goroutine per topic forwards messages into a single merged channel.
	// This avoids reflect.Select while keeping the main loop simple.
	type sseEvent struct {
		topic string
		data  []byte
	}
	merged := make(chan sseEvent, 16)
	var wg sync.WaitGroup

	// cancel lets any goroutine terminate the whole SSE connection (e.g. when
	// the broker closes a subscriber channel because the client is too slow).
	// The client's EventSource will auto-reconnect and get fresh cached state.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	for topic, ch := range channels {
		wg.Add(1)
		go func(topic string, ch chan []byte) {
			defer wg.Done()
			for {
				select {
				case data, ok := <-ch:
					if !ok {
						// Broker dropped this subscriber (lagging client).
						// Cancel the whole connection so EventSource reconnects.
						cancel()
						return
					}
					select {
					case merged <- sseEvent{topic, data}:
					case <-ctx.Done():
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}(topic, ch)
	}
	go func() { wg.Wait(); close(merged) }()

	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-merged:
			if !ok {
				return
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.topic, ev.data)
			flusher.Flush()
		case <-heartbeat.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

func writeJSON(ctx context.Context, w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.ErrorContext(ctx, "writeJSON encode failed", "err", err)
	}
}

// writeRunOpError is a convenience wrapper for the common pattern of returning
// an error from runOp. It extracts task steps from out (if non-nil) and writes
// a 500 response.
func writeRunOpError(ctx context.Context, w http.ResponseWriter, err error, out *ansible.PlaybookOutput) {
	var steps []ansible.TaskStep
	if out != nil {
		steps = out.Steps()
	}
	writeError(ctx, w, http.StatusInternalServerError, err, steps)
}

// writeOpsError is writeRunOpError for the in-process ops layer.
func writeOpsError(ctx context.Context, w http.ResponseWriter, err error, res *ops.Result) {
	var steps []ansible.TaskStep
	if res != nil {
		steps = res.Steps()
	}
	writeError(ctx, w, http.StatusInternalServerError, err, steps)
}

func writeError(ctx context.Context, w http.ResponseWriter, code int, err error, steps []ansible.TaskStep) {
	writeAPIError(ctx, w, code, apiError{Error: err.Error(), Tasks: steps})
}

// writeAPIError logs and writes a fully populated apiError body.
func writeAPIError(ctx context.Context, w http.ResponseWriter, code int, e apiError) {
	slog.ErrorContext(ctx, "api error", "status", code, "err", e.Error)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(e)
}

// getSchema handles GET /api/v1/system/schema
// Returns property definitions and system metadata filtered for the current OS.
func (h *Handler) getSchema(context.Context, struct{}) (SchemaInfo, error) {
	return SchemaInfo{
		OS:                runtime.GOOS,
		DatasetProperties: schema.ForOS(runtime.GOOS),
		UserShells:        system.ListShells(),
		Capabilities:      zfs.Caps(),
	}, nil
}

// SchemaInfo is the GET /api/v1/system/schema response: what the UI needs to render
// dataset and user forms for this host.
type SchemaInfo struct {
	OS                string            `json:"os"`
	DatasetProperties []schema.Property `json:"dataset_properties"`
	UserShells        []string          `json:"user_shells"`
	Capabilities      zfs.Capabilities  `json:"capabilities"`
}
