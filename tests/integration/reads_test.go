//go:build integration

package integration

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestReadEndpoints asserts every small read endpoint answers 200 with the
// top-level JSON shape the frontend depends on.
func TestReadEndpoints(t *testing.T) {
	type obj = map[string]any
	type arr = []any

	cases := []struct {
		path  string
		check func(t *testing.T, b []byte)
	}{
		{"/api/v1/system/info", func(t *testing.T, b []byte) {
			v := decode[struct {
				OS       string `json:"os"`
				Software []struct {
					Name string `json:"name"`
				} `json:"software"`
				Otel *struct {
					Enabled bool `json:"enabled"`
				} `json:"otel"`
			}](t, b)
			if v.OS == "" || len(v.Software) == 0 || v.Otel == nil {
				t.Errorf("sysinfo missing os/software/otel: %s", truncate(b, 500))
			}
		}},
		{"/api/v1/system/network", func(t *testing.T, b []byte) {
			if len(decode[arr](t, b)) == 0 {
				t.Errorf("no network interfaces reported")
			}
		}},
		{"/api/v1/system/version", func(t *testing.T, b []byte) {
			if decode[map[string]string](t, b)["version"] == "" {
				t.Errorf("empty version: %s", b)
			}
		}},
		{"/api/v1/system/schema", func(t *testing.T, b []byte) {
			v := decode[struct {
				OS           string          `json:"os"`
				Capabilities map[string]bool `json:"capabilities"`
			}](t, b)
			if _, ok := v.Capabilities["rewrite"]; !ok || v.OS == "" {
				t.Errorf("schema missing os or capabilities.rewrite: %s", truncate(b, 500))
			}
		}},
		{"/api/v1/system/smart", func(t *testing.T, b []byte) {
			if _, ok := decode[obj](t, b)["available"]; !ok {
				t.Errorf("smart missing available: %s", b)
			}
		}},
		{"/api/v1/pools/iostat", func(t *testing.T, b []byte) { decode[arr](t, b) }},
		{"/api/v1/pools/status", func(t *testing.T, b []byte) {
			if _, ok := poolStatus(t, testPool); !ok {
				t.Errorf("pool %s not in poolstatus", testPool)
			}
		}},
		{"/api/v1/devices", func(t *testing.T, b []byte) {
			if len(decode[arr](t, b)) == 0 {
				t.Errorf("no block devices reported")
			}
		}},
		{v1("/datasets/%s/properties", testPool), func(t *testing.T, b []byte) {
			if _, ok := decode[obj](t, b)["compression"]; !ok {
				t.Errorf("dataset-props missing compression: %s", truncate(b, 500))
			}
		}},
		{"/api/v1/auth/whoami", func(t *testing.T, b []byte) {
			if u := decode[map[string]string](t, b)["user"]; u != adminUser {
				t.Errorf("whoami user %q, want %q", u, adminUser)
			}
		}},
		{"/api/v1/auth/config", func(t *testing.T, b []byte) {
			if u := decode[obj](t, b)["username"]; u != adminUser {
				t.Errorf("auth/config username %v, want %q", u, adminUser)
			}
		}},
		{"/api/v1/tls", func(t *testing.T, b []byte) {
			if _, ok := decode[obj](t, b)["enabled"]; !ok {
				t.Errorf("tls/status missing enabled: %s", b)
			}
		}},
		{"/api/v1/datasets/acl-status", func(t *testing.T, b []byte) {
			if _, ok := decode[map[string]bool](t, b)[testPool]; !ok {
				t.Errorf("acl-status missing pool root %s: %s", testPool, truncate(b, 500))
			}
		}},
		{"/api/v1/pools/importable", func(t *testing.T, b []byte) { decode[arr](t, b) }},
		{"/api/v1/pools/scrub-schedules", func(t *testing.T, b []byte) {
			if _, ok := decode[obj](t, b)["mode"]; !ok {
				t.Errorf("scrub-schedules missing mode: %s", b)
			}
		}},
		{"/api/v1/auto-snapshot/schedules", func(t *testing.T, b []byte) {
			if _, ok := decode[obj](t, b)[testPool]; !ok {
				t.Errorf("auto-snapshot-schedules missing %s", testPool)
			}
		}},
		{"/api/v1/auto-snapshot/status", func(t *testing.T, b []byte) {
			if _, ok := decode[obj](t, b)["dumpstore_managed"]; !ok {
				t.Errorf("auto-snapshot/status missing dumpstore_managed: %s", b)
			}
		}},
		{"/api/v1/jobs", func(t *testing.T, b []byte) { decode[arr](t, b) }},
		{"/api/v1/services", func(t *testing.T, b []byte) {
			if len(decode[arr](t, b)) == 0 {
				t.Errorf("no services reported")
			}
		}},
		{"/api/v1/users", func(t *testing.T, b []byte) {
			if len(decode[arr](t, b)) == 0 {
				t.Errorf("no users reported")
			}
		}},
		{"/api/v1/groups", func(t *testing.T, b []byte) {
			if len(decode[arr](t, b)) == 0 {
				t.Errorf("no groups reported")
			}
		}},
		{"/api/v1/smb/status", func(t *testing.T, b []byte) {
			if _, ok := decode[obj](t, b)["initialized"]; !ok {
				t.Errorf("smb/status missing initialized: %s", b)
			}
		}},
		{"/api/v1/iscsi/targets", func(t *testing.T, b []byte) {
			// null when targets exist but none are zvol-backed; [] otherwise.
			if s := strings.TrimSpace(string(b)); s != "null" {
				decode[arr](t, b)
			}
		}},
		{"/api/v1/replication/tasks", func(t *testing.T, b []byte) { decode[arr](t, b) }},
	}
	for _, c := range cases {
		t.Run(strings.TrimPrefix(c.path, "/api/v1/"), func(t *testing.T) {
			c.check(t, apiOK(t, "GET", c.path, nil))
		})
	}

	// /metrics is unprotected by default and speaks Prometheus text format.
	resp, err := http.Get(baseURL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") {
		t.Errorf("/metrics: status %d, content-type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /metrics: %v", err)
	}
	// Every family the pre-#168 emitters exposed must still be there (the
	// unit-level golden in metrics_test.go pins types, labels and buckets).
	// The histograms only appear once observed — this test runs after the
	// ansible- and ops-backed writes in acl_chown_test.go and api_test.go.
	for _, name := range []string{
		"process_start_time_seconds", "go_goroutines",
		"go_memstats_alloc_bytes", "go_memstats_alloc_bytes_total", "go_memstats_sys_bytes",
		"go_memstats_heap_alloc_bytes", "go_memstats_heap_sys_bytes", "go_memstats_heap_idle_bytes",
		"go_memstats_heap_inuse_bytes", "go_memstats_heap_released_bytes", "go_memstats_heap_objects",
		"go_memstats_mallocs_total", "go_memstats_frees_total", "go_memstats_next_gc_bytes",
		"go_gc_cycles_total", "go_gc_pause_ns_total", "dumpstore_build_info",
		"http_requests_total", "http_request_duration_seconds",
		"ansible_runs_total", "ansible_run_duration_seconds",
		"dumpstore_op_runs_total", "dumpstore_op_duration_seconds",
	} {
		if !strings.Contains(string(body), "\n# TYPE "+name+" ") {
			t.Errorf("/metrics: family %s missing", name)
		}
	}
	// The path label is the route pattern, never the raw (unbounded) URL.
	if !strings.Contains(string(body), `path="/api/v1/datasets/{name}"`) {
		t.Errorf("/metrics: no http_requests_total sample labelled with the /api/v1/datasets/{name} pattern")
	}
	if strings.Contains(string(body), `path="/api/v1/datasets/`+testPool) {
		t.Errorf("/metrics: raw dataset path used as a label value")
	}
}

// TestReadNegatives covers validation on the read path.
func TestReadNegatives(t *testing.T) {
	apiStatus(t, http.StatusBadRequest, "GET", v1("/datasets/%s/properties", testPool+"@nope"), nil)
	apiStatus(t, http.StatusNotFound, "GET", "/api/v1/jobs/does-not-exist", nil)
	apiStatus(t, http.StatusBadRequest, "GET", "/api/v1/events?topics=bogus", nil)
}

// TestSSEEvents subscribes to a cached topic and expects the broker to
// replay its last value immediately on connect.
func TestSSEEvents(t *testing.T) {
	login(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/api/v1/events?topics=user.query", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("events: status %d, content-type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var sawEvent, sawData bool
	for sc.Scan() && !(sawEvent && sawData) {
		line := sc.Text()
		sawEvent = sawEvent || line == "event: user.query"
		sawData = sawData || (sawEvent && strings.HasPrefix(line, "data: ["))
	}
	if !sawEvent || !sawData {
		t.Fatalf("no user.query event with data received (scan err: %v)", sc.Err())
	}
}
