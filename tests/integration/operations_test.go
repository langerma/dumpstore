//go:build integration

package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// opStep is one ansible.progress event / operation step (#184).
type opStep struct {
	Op     string `json:"op"`
	Seq    int    `json:"seq"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Msg    string `json:"msg"`
}

type operation struct {
	ID         string          `json:"id"`
	State      string          `json:"state"`
	HTTPStatus int             `json:"http_status"`
	Steps      []opStep        `json:"steps"`
	Result     json.RawMessage `json:"result"`
}

type tasksBody struct {
	Tasks []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"tasks"`
}

// newClient returns a separately logged-in client (own cookie jar/session).
func newClient(t *testing.T) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Timeout: 3 * time.Minute}
	resp, err := c.PostForm(baseURL+"/auth/login", url.Values{"username": {adminUser}, "password": {adminPass}})
	if err != nil {
		t.Fatalf("second client login: %v", err)
	}
	resp.Body.Close()
	return c
}

// opRequest performs a JSON write with an optional X-Operation-ID header and
// returns status, echoed operation ID and body.
func opRequest(t *testing.T, c *http.Client, method, path, opID string, body any) (int, string, []byte) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(method, baseURL+path, bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if opID != "" {
		req.Header.Set("X-Operation-ID", opID)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("X-Operation-ID"), out
}

// TestOperations covers #184: two clients write concurrently while one of
// them watches ansible.progress. Every step is tagged with its operation ID
// and a contiguous seq, so each client can show only its own steps; the
// operation record returns the same steps and result as the HTTP response.
func TestOperations(t *testing.T) {
	login(t)
	clientB := newClient(t)
	ds := testPool + "/itest-ops"
	createDataset(t, ds)
	suffix := time.Now().Format("150405")
	idA, idB := "itest-op-A-"+suffix, "itest-op-B-"+suffix

	// Client B listens on the shared progress stream.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", baseURL+"/api/v1/events?topics=ansible.progress", nil)
	resp, err := clientB.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("events: %v %v", err, resp)
	}
	defer resp.Body.Close()
	var mu sync.Mutex
	seen := map[string][]opStep{}
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			data, ok := strings.CutPrefix(sc.Text(), "data: ")
			if !ok {
				continue
			}
			var s opStep
			if json.Unmarshal([]byte(data), &s) == nil {
				mu.Lock()
				seen[s.Op] = append(seen[s.Op], s)
				mu.Unlock()
			}
		}
	}()
	time.Sleep(500 * time.Millisecond) // let the subscription register

	// A and B each create a snapshot concurrently with their own ID.
	type res struct {
		status int
		id     string
		body   []byte
	}
	results := map[string]res{}
	var wg sync.WaitGroup
	for _, w := range []struct {
		c    *http.Client
		id   string
		snap string
	}{{client, idA, "opsA"}, {clientB, idB, "opsB"}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, echoed, b := opRequest(t, w.c, "POST", "/api/v1/snapshots", w.id, map[string]any{"dataset": ds, "snapname": w.snap})
			mu.Lock()
			results[w.id] = res{st, echoed, b}
			mu.Unlock()
		}()
	}
	wg.Wait()

	for _, id := range []string{idA, idB} {
		r := results[id]
		if r.status != http.StatusOK && r.status != http.StatusCreated {
			t.Fatalf("%s: status %d body %s", id, r.status, truncate(r.body, 500))
		}
		if r.id != id {
			t.Errorf("%s: echoed ID %q", id, r.id)
		}
		want := decode[tasksBody](t, r.body).Tasks
		if len(want) == 0 {
			t.Fatalf("%s: response has no tasks", id)
		}

		// Live stream: only this op's steps under its ID, seq 1..n contiguous.
		waitFor(t, "progress events for "+id, 10*time.Second, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return len(seen[id]) >= len(want)
		})
		mu.Lock()
		live := append([]opStep(nil), seen[id]...)
		mu.Unlock()
		if len(live) != len(want) {
			t.Fatalf("%s: %d live steps, response has %d tasks", id, len(live), len(want))
		}
		for i, s := range live {
			if s.Seq != i+1 || s.Name != want[i].Name || s.Status != want[i].Status {
				t.Errorf("%s: live step %d = %+v, want seq %d %+v", id, i, s, i+1, want[i])
			}
		}

		// Operation record: same steps and result as the HTTP response.
		op := decode[operation](t, apiOK(t, "GET", v1("/operations/%s", id), nil))
		if op.State != "succeeded" || op.HTTPStatus != r.status || len(op.Steps) != len(live) {
			t.Fatalf("%s: record %+v", id, op)
		}
		var gotRes, wantRes any
		_ = json.Unmarshal(op.Result, &gotRes)
		_ = json.Unmarshal(r.body, &wantRes)
		if a, b := mustJSON(gotRes), mustJSON(wantRes); a != b {
			t.Errorf("%s: record result %s != response %s", id, a, b)
		}
		tail := decode[operation](t, apiOK(t, "GET", v1("/operations/%s", id)+"?after=1", nil))
		if len(tail.Steps) != len(op.Steps)-1 {
			t.Errorf("%s: after=1 returned %d steps, want %d", id, len(tail.Steps), len(op.Steps)-1)
		}
	}

	// A duplicate ID is refused without running anything.
	if st, _, b := opRequest(t, client, "POST", "/api/v1/snapshots", idA, map[string]any{"dataset": ds, "snapname": "opsDup"}); st != http.StatusConflict || !bytes.Contains(b, []byte(`"duplicate_operation":true`)) {
		t.Errorf("duplicate ID: status %d body %s", st, truncate(b, 300))
	}
	for _, s := range listSnapshots(t) {
		if s.Name == ds+"@opsDup" {
			t.Error("duplicate-ID request was executed")
		}
	}
	// An invalid ID is refused.
	if st, _, _ := opRequest(t, client, "POST", "/api/v1/snapshots", "bad id!", map[string]any{"dataset": ds, "snapname": "opsBad"}); st != http.StatusBadRequest {
		t.Errorf("invalid ID: status %d", st)
	}
	// A write without the header gets a generated, fetchable ID.
	st, gen, _ := opRequest(t, client, "POST", "/api/v1/snapshots", "", map[string]any{"dataset": ds, "snapname": "opsGen"})
	if st >= 300 || gen == "" {
		t.Fatalf("generated ID: status %d id %q", st, gen)
	}
	if op := decode[operation](t, apiOK(t, "GET", v1("/operations/%s", gen), nil)); op.ID != gen || op.State != "succeeded" {
		t.Errorf("generated op record %+v", op)
	}
	apiStatus(t, http.StatusNotFound, "GET", "/api/v1/operations/itest-op-unknown", nil)
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
