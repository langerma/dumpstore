//go:build integration

// Package integration drives a deployed dumpstore instance over HTTP and
// verifies real ZFS state changes. It expects the Lima dev VM to be running
// with dumpstore deployed:
//
//	make vm-linux-start
//	make vm-linux-deploy
//	make test-integration
//
// Fixtures that cannot be created through the API (files inside datasets,
// pre-cleaning stale state) are set up via `limactl shell` in the VM.
// See README.md in this directory for configuration knobs.
package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	baseURL   = envOr("DUMPSTORE_URL", "http://localhost:8080")
	vmName    = envOr("DUMPSTORE_VM", "dumpstore-linux")
	adminUser = envOr("DUMPSTORE_USER", "admin")
	adminPass = envOr("DUMPSTORE_PASS", "admin")
	testPool  = envOr("DUMPSTORE_TEST_POOL", "tank")
	testDisks = splitNonEmpty(envOr("DUMPSTORE_TEST_DISKS", "/dev/vdc,/dev/vdd,/dev/vde"))
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func splitNonEmpty(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// client holds the shared authenticated session. Redirects are not followed
// so login tests can assert on the 302 responses directly.
var client = &http.Client{
	Timeout: 3 * time.Minute,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

var loginOnce sync.Once

func TestMain(m *testing.M) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cookiejar:", err)
		os.Exit(1)
	}
	client.Jar = jar

	// Fail fast with instructions if no server is listening.
	resp, err := client.Get(baseURL + "/login")
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot reach dumpstore at %s: %v\n", baseURL, err)
		fmt.Fprintln(os.Stderr, "start the dev VM first:  make vm-linux-start && make vm-linux-deploy")
		os.Exit(1)
	}
	resp.Body.Close()

	os.Exit(m.Run())
}

// login authenticates the shared client once per test run.
func login(t *testing.T) {
	t.Helper()
	loginOnce.Do(func() {
		resp := postLogin(t, adminUser, adminPass)
		if loc := resp.Header.Get("Location"); resp.StatusCode != http.StatusFound || loc != "/" {
			t.Fatalf("login as %s failed: status %d, Location %q (expected 302 -> /)",
				adminUser, resp.StatusCode, loc)
		}
	})
}

// postLogin submits the login form and returns the (unfollowed) response.
func postLogin(t *testing.T, user, pass string) *http.Response {
	t.Helper()
	form := url.Values{"username": {user}, "password": {pass}}
	resp, err := client.PostForm(baseURL+"/auth/login", form)
	if err != nil {
		t.Fatalf("POST /auth/login: %v", err)
	}
	resp.Body.Close()
	return resp
}

// api performs an authenticated JSON request and returns status code and body.
func api(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()
	login(t)

	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, baseURL+path, rdr)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response of %s %s: %v", method, path, err)
	}
	return resp.StatusCode, b
}

// apiOK is api plus an assertion that the status is 2xx.
func apiOK(t *testing.T, method, path string, body any) []byte {
	t.Helper()
	status, b := api(t, method, path, body)
	if status < 200 || status > 299 {
		t.Fatalf("%s %s: status %d, body: %s", method, path, status, truncate(b, 2000))
	}
	return b
}

func decode[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("decode response: %v\nbody: %s", err, truncate(b, 2000))
	}
	return v
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
}

// vmExec runs a shell script as root inside the Lima VM and returns stdout.
func vmExec(t *testing.T, script string) string {
	t.Helper()
	out, err := vmExecErr(script)
	if err != nil {
		t.Fatalf("vm command failed: %v\nscript: %s\noutput: %s", err, script, out)
	}
	return out
}

// vmExecErr is vmExec without the fatal-on-error behavior, for cleanup paths.
func vmExecErr(script string) (string, error) {
	cmd := exec.Command("limactl", "shell", "--tty=false", vmName, "--", "sudo", "sh", "-c", script)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// waitFor polls cond until it returns true or the timeout expires.
func waitFor(t *testing.T, desc string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, desc)
}

// --- typed views of API responses (only the fields the tests assert on) ---

type dataset struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Mountpoint  string `json:"mountpoint"`
	Compression string `json:"compression"`
}

type snapshot struct {
	Name    string `json:"name"`
	Dataset string `json:"dataset"`
}

type pool struct {
	Name   string `json:"name"`
	Health string `json:"health"`
}

type vdevEntry struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

type poolDetail struct {
	Name  string      `json:"name"`
	State string      `json:"state"`
	Scan  string      `json:"scan"`
	Vdevs []vdevEntry `json:"vdevs"`
}

type spaceRow struct {
	Name  string `json:"name"`
	Used  uint64 `json:"used"`
	Quota uint64 `json:"quota"`
}

type job struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Status string `json:"status"`
	Stderr string `json:"stderr"`
	Error  string `json:"error"`
}

func listDatasets(t *testing.T) []dataset {
	t.Helper()
	return decode[[]dataset](t, apiOK(t, "GET", "/api/datasets", nil))
}

func datasetByName(t *testing.T, name string) (dataset, bool) {
	t.Helper()
	for _, d := range listDatasets(t) {
		if d.Name == name {
			return d, true
		}
	}
	return dataset{}, false
}

func listSnapshots(t *testing.T) []snapshot {
	t.Helper()
	return decode[[]snapshot](t, apiOK(t, "GET", "/api/snapshots", nil))
}

func poolByName(t *testing.T, name string) (pool, bool) {
	t.Helper()
	for _, p := range decode[[]pool](t, apiOK(t, "GET", "/api/pools", nil)) {
		if p.Name == name {
			return p, true
		}
	}
	return pool{}, false
}

func poolStatus(t *testing.T, name string) (poolDetail, bool) {
	t.Helper()
	for _, d := range decode[[]poolDetail](t, apiOK(t, "GET", "/api/poolstatus", nil)) {
		if d.Name == name {
			return d, true
		}
	}
	return poolDetail{}, false
}

// destroyDatasetInVM force-destroys a dataset tree directly in the VM.
// Used for cleanup so a failed test cannot strand state for the next run.
func destroyDatasetInVM(name string) {
	_, _ = vmExecErr("zfs destroy -r " + name + " 2>/dev/null || true")
}
