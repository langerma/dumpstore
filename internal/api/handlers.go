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
	"dumpstore/internal/broker"
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

	// reShellPath matches a safe absolute filesystem path.
	reShellPath = regexp.MustCompile(`^/[a-zA-Z0-9/_.-]+$`)

	// reIQN matches a valid iSCSI Qualified Name (RFC 3720).
	reIQN = regexp.MustCompile(`^iqn\.\d{4}-\d{2}\.[a-z0-9.-]+:[a-zA-Z0-9._:-]+$`)

	// reOctalMask matches a 3- or 4-digit octal permission mask.
	reOctalMask = regexp.MustCompile(`^[0-7]{3,4}$`)

	// reSSHKeyType matches the key-type prefix of an SSH public key.
	reSSHKeyType = regexp.MustCompile(`^(ssh-|ecdsa-sha2-|sk-)`)
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

// apiError is returned as JSON for all non-2xx responses.
// Tasks is populated for Ansible-backed operations so the UI can show the op-log
// even when the request fails.
type apiError struct {
	Error string             `json:"error"`
	Tasks []ansible.TaskStep `json:"tasks,omitempty"`
}

// Handler holds dependencies for the HTTP API.
type Handler struct {
	runner  *ansible.Runner
	version string
	broker  *broker.Broker
	userMu  sync.Mutex // serialises user/group write ops to avoid /etc/group lock contention
}

// NewHandler creates a Handler with the given ansible Runner, app version string,
// and subscription broker (used by the SSE endpoint).
func NewHandler(runner *ansible.Runner, version string, b *broker.Broker) *Handler {
	return &Handler{runner: runner, version: version, broker: b}
}

// runOp executes a playbook and publishes each task step to the ansible.progress
// SSE topic as it completes, so the frontend can show live progress.
func (h *Handler) runOp(playbook string, vars map[string]string) (*ansible.PlaybookOutput, error) {
	return h.runner.RunStreaming(playbook, vars, func(step ansible.TaskStep) {
		h.broker.PublishNoCache("ansible.progress", step)
	})
}

// RegisterRoutes attaches all API routes to mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sysinfo", h.getSysInfo)
	mux.HandleFunc("GET /api/version", h.getVersion)
	mux.HandleFunc("GET /api/poolstatus", h.getPoolStatuses)
	mux.HandleFunc("GET /api/pools", h.getPools)
	mux.HandleFunc("GET /api/datasets", h.getDatasets)
	mux.HandleFunc("POST /api/datasets", h.createDataset)
	mux.HandleFunc("DELETE /api/datasets/{name...}", h.deleteDataset)
	mux.HandleFunc("GET /metrics", h.getMetrics)
	mux.HandleFunc("GET /api/smart", h.getSMART)
	mux.HandleFunc("GET /api/dataset-props/{name...}", h.getDatasetProps)
	mux.HandleFunc("PATCH /api/datasets/{name...}", h.setDatasetProps)
	mux.HandleFunc("GET /api/snapshots", h.getSnapshots)
	mux.HandleFunc("GET /api/iostat", h.getIOStat)
	mux.HandleFunc("POST /api/snapshots", h.createSnapshot)
	mux.HandleFunc("POST /api/snapshots/delete-batch", h.deleteSnapshotBatch)
	mux.HandleFunc("DELETE /api/snapshots/{snapshot...}", h.deleteSnapshot)
	mux.HandleFunc("GET /api/events", h.getEvents)
	mux.HandleFunc("GET /api/chown/{dataset...}", h.getDatasetOwnership)
	mux.HandleFunc("POST /api/chown/{dataset...}", h.setDatasetOwnership)
	mux.HandleFunc("GET /api/acl-status", h.getACLStatus)
	mux.HandleFunc("GET /api/acl/{dataset...}", h.getDatasetACL)
	mux.HandleFunc("POST /api/acl/{dataset...}", h.setACLEntry)
	mux.HandleFunc("DELETE /api/acl/{dataset...}", h.removeACLEntry)
	mux.HandleFunc("GET /api/users", h.getUsers)
	mux.HandleFunc("POST /api/users", h.createUser)
	mux.HandleFunc("PUT /api/users/{name}", h.modifyUser)
	mux.HandleFunc("DELETE /api/users/{name}", h.deleteUser)
	mux.HandleFunc("GET /api/users/{name}/sshkeys", h.listSSHKeys)
	mux.HandleFunc("POST /api/users/{name}/sshkeys", h.addSSHKey)
	mux.HandleFunc("DELETE /api/users/{name}/sshkeys", h.removeSSHKey)
	mux.HandleFunc("GET /api/groups", h.getGroups)
	mux.HandleFunc("POST /api/groups", h.createGroup)
	mux.HandleFunc("PUT /api/groups/{name}", h.modifyGroup)
	mux.HandleFunc("DELETE /api/groups/{name}", h.deleteGroup)
	mux.HandleFunc("GET /api/smb-users", h.getSambaUsers)
	mux.HandleFunc("POST /api/smb-users/{name}", h.addSambaUser)
	mux.HandleFunc("DELETE /api/smb-users/{name}", h.removeSambaUser)
	mux.HandleFunc("POST /api/smb-config/pam", h.configureSambaPAM)
	mux.HandleFunc("GET /api/smb-shares", h.getSMBShares)
	mux.HandleFunc("POST /api/smb-share/{dataset...}", h.setSMBShare)
	mux.HandleFunc("DELETE /api/smb-share/{dataset...}", h.deleteSMBShare)
	mux.HandleFunc("GET /api/smb/homes", h.getSMBHomes)
	mux.HandleFunc("POST /api/smb/homes", h.setSMBHomes)
	mux.HandleFunc("DELETE /api/smb/homes", h.deleteSMBHomes)
	mux.HandleFunc("GET /api/smb/timemachine", h.getTimeMachineShares)
	mux.HandleFunc("POST /api/smb/timemachine", h.createTimeMachineShare)
	mux.HandleFunc("DELETE /api/smb/timemachine/{sharename}", h.deleteTimeMachineShare)
	mux.HandleFunc("POST /api/scrub/{pool}", h.startScrub)
	mux.HandleFunc("DELETE /api/scrub/{pool}", h.cancelScrub)
	mux.HandleFunc("GET /api/scrub-schedules", h.listScrubSchedules)
	mux.HandleFunc("PUT /api/scrub-schedule/{pool}", h.setScrubSchedule)
	mux.HandleFunc("DELETE /api/scrub-schedule/{pool}", h.deleteScrubSchedule)
	mux.HandleFunc("GET /api/auto-snapshot-schedules", h.listAutoSnapshotSchedules)
	mux.HandleFunc("GET /api/auto-snapshot/{name...}", h.getAutoSnapshotProps)
	mux.HandleFunc("PUT /api/auto-snapshot/{name...}", h.setAutoSnapshotProps)
	mux.HandleFunc("GET /api/iscsi-targets", h.getISCSITargets)
	mux.HandleFunc("POST /api/iscsi-targets", h.createISCSITarget)
	mux.HandleFunc("DELETE /api/iscsi-targets", h.deleteISCSITarget)
	mux.HandleFunc("GET /api/schema", h.getSchema)
}

func (h *Handler) getSysInfo(w http.ResponseWriter, r *http.Request) {
	type response struct {
		AppVersion string `json:"app_version"`
		system.Info
	}
	writeJSON(r.Context(), w, response{AppVersion: h.version, Info: system.Get()})
}

func (h *Handler) getVersion(w http.ResponseWriter, r *http.Request) {
	v, err := zfs.Version()
	if err != nil {
		slog.WarnContext(r.Context(), "zpool version failed", "err", err)
		v = ""
	}
	writeJSON(r.Context(), w, map[string]string{"version": v})
}

// getEvents handles GET /api/events?topics=pool.query,dataset.query,...
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
	ctx := r.Context()

	for topic, ch := range channels {
		wg.Add(1)
		go func(topic string, ch chan []byte) {
			defer wg.Done()
			for {
				select {
				case data, ok := <-ch:
					if !ok {
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

func writeError(ctx context.Context, w http.ResponseWriter, code int, err error, steps []ansible.TaskStep) {
	slog.ErrorContext(ctx, "api error", "status", code, "err", err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(apiError{Error: err.Error(), Tasks: steps})
}

// getSchema handles GET /api/schema
// Returns property definitions and system metadata filtered for the current OS.
func (h *Handler) getSchema(w http.ResponseWriter, r *http.Request) {
	writeJSON(r.Context(), w, map[string]any{
		"os":                 runtime.GOOS,
		"dataset_properties": schema.ForOS(runtime.GOOS),
		"user_shells":        system.ListShells(),
	})
}
