//go:build integration

package integration

import (
	"bufio"
	"context"
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
		{"/api/sysinfo", func(t *testing.T, b []byte) {
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
		{"/api/network", func(t *testing.T, b []byte) {
			if len(decode[arr](t, b)) == 0 {
				t.Errorf("no network interfaces reported")
			}
		}},
		{"/api/version", func(t *testing.T, b []byte) {
			if decode[map[string]string](t, b)["version"] == "" {
				t.Errorf("empty version: %s", b)
			}
		}},
		{"/api/schema", func(t *testing.T, b []byte) {
			v := decode[struct {
				OS           string          `json:"os"`
				Capabilities map[string]bool `json:"capabilities"`
			}](t, b)
			if _, ok := v.Capabilities["rewrite"]; !ok || v.OS == "" {
				t.Errorf("schema missing os or capabilities.rewrite: %s", truncate(b, 500))
			}
		}},
		{"/api/smart", func(t *testing.T, b []byte) {
			if _, ok := decode[obj](t, b)["available"]; !ok {
				t.Errorf("smart missing available: %s", b)
			}
		}},
		{"/api/iostat", func(t *testing.T, b []byte) { decode[arr](t, b) }},
		{"/api/poolstatus", func(t *testing.T, b []byte) {
			if _, ok := poolStatus(t, testPool); !ok {
				t.Errorf("pool %s not in poolstatus", testPool)
			}
		}},
		{"/api/devices", func(t *testing.T, b []byte) {
			if len(decode[arr](t, b)) == 0 {
				t.Errorf("no block devices reported")
			}
		}},
		{"/api/dataset-props/" + testPool, func(t *testing.T, b []byte) {
			if _, ok := decode[obj](t, b)["compression"]; !ok {
				t.Errorf("dataset-props missing compression: %s", truncate(b, 500))
			}
		}},
		{"/api/whoami", func(t *testing.T, b []byte) {
			if u := decode[map[string]string](t, b)["user"]; u != adminUser {
				t.Errorf("whoami user %q, want %q", u, adminUser)
			}
		}},
		{"/api/auth/config", func(t *testing.T, b []byte) {
			if u := decode[obj](t, b)["username"]; u != adminUser {
				t.Errorf("auth/config username %v, want %q", u, adminUser)
			}
		}},
		{"/api/tls/status", func(t *testing.T, b []byte) {
			if _, ok := decode[obj](t, b)["enabled"]; !ok {
				t.Errorf("tls/status missing enabled: %s", b)
			}
		}},
		{"/api/acl-status", func(t *testing.T, b []byte) {
			if _, ok := decode[map[string]bool](t, b)[testPool]; !ok {
				t.Errorf("acl-status missing pool root %s: %s", testPool, truncate(b, 500))
			}
		}},
		{"/api/pools/importable", func(t *testing.T, b []byte) { decode[arr](t, b) }},
		{"/api/scrub-schedules", func(t *testing.T, b []byte) {
			if _, ok := decode[obj](t, b)["mode"]; !ok {
				t.Errorf("scrub-schedules missing mode: %s", b)
			}
		}},
		{"/api/auto-snapshot-schedules", func(t *testing.T, b []byte) {
			if _, ok := decode[obj](t, b)[testPool]; !ok {
				t.Errorf("auto-snapshot-schedules missing %s", testPool)
			}
		}},
		{"/api/auto-snapshot/status", func(t *testing.T, b []byte) {
			if _, ok := decode[obj](t, b)["dumpstore_managed"]; !ok {
				t.Errorf("auto-snapshot/status missing dumpstore_managed: %s", b)
			}
		}},
		{"/api/jobs", func(t *testing.T, b []byte) { decode[arr](t, b) }},
		{"/api/services", func(t *testing.T, b []byte) {
			if len(decode[arr](t, b)) == 0 {
				t.Errorf("no services reported")
			}
		}},
		{"/api/users", func(t *testing.T, b []byte) {
			if len(decode[arr](t, b)) == 0 {
				t.Errorf("no users reported")
			}
		}},
		{"/api/groups", func(t *testing.T, b []byte) {
			if len(decode[arr](t, b)) == 0 {
				t.Errorf("no groups reported")
			}
		}},
		{"/api/smb/status", func(t *testing.T, b []byte) {
			if _, ok := decode[obj](t, b)["initialized"]; !ok {
				t.Errorf("smb/status missing initialized: %s", b)
			}
		}},
		{"/api/iscsi-targets", func(t *testing.T, b []byte) {
			// null when targets exist but none are zvol-backed; [] otherwise.
			if s := strings.TrimSpace(string(b)); s != "null" {
				decode[arr](t, b)
			}
		}},
		{"/api/replication", func(t *testing.T, b []byte) { decode[arr](t, b) }},
	}
	for _, c := range cases {
		t.Run(strings.TrimPrefix(c.path, "/api/"), func(t *testing.T) {
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
}

// TestReadNegatives covers validation on the read path.
func TestReadNegatives(t *testing.T) {
	apiStatus(t, http.StatusBadRequest, "GET", "/api/dataset-props/"+testPool+"@nope", nil)
	apiStatus(t, http.StatusNotFound, "GET", "/api/jobs/does-not-exist", nil)
	apiStatus(t, http.StatusBadRequest, "GET", "/api/events?topics=bogus", nil)
}

// TestSSEEvents subscribes to a cached topic and expects the broker to
// replay its last value immediately on connect.
func TestSSEEvents(t *testing.T) {
	login(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/api/events?topics=user.query", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /api/events: %v", err)
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
