package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dumpstore/internal/ansible"
	"dumpstore/internal/operations"
)

func newOpsTestHandler() (*Handler, http.Handler) {
	h := &Handler{operations: operations.New(nil)}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/thing", func(w http.ResponseWriter, r *http.Request) {
		id := operations.IDFromContext(r.Context())
		h.operations.Step(id, ansible.TaskStep{Name: "do thing", Status: "changed"})
		if r.URL.Query().Get("fail") != "" {
			writeError(r.Context(), w, http.StatusConflict, errString("nope"), nil)
			return
		}
		writeJSON(r.Context(), w, map[string]string{"op": id})
	})
	mux.HandleFunc("GET /api/thing", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(r.Context(), w, map[string]string{"op": operations.IDFromContext(r.Context())})
	})
	mux.HandleFunc("GET /api/operations/{id}", h.getOperation)
	return h, h.Operations(mux)
}

type errString string

func (e errString) Error() string { return string(e) }

func do(t *testing.T, srv http.Handler, method, path, opID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if opID != "" {
		req.Header.Set(OperationIDHeader, opID)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestOperationsMiddleware(t *testing.T) {
	h, srv := newOpsTestHandler()

	// Client-chosen ID: echoed, in ctx, recorded with status + body.
	rec := do(t, srv, "POST", "/api/thing", "client-id-0001")
	if rec.Code != 200 || rec.Header().Get(OperationIDHeader) != "client-id-0001" {
		t.Fatalf("code=%d header=%q", rec.Code, rec.Header().Get(OperationIDHeader))
	}
	if !strings.Contains(rec.Body.String(), `"op":"client-id-0001"`) {
		t.Errorf("ctx ID not seen by handler: %s", rec.Body)
	}
	op, ok := h.operations.Get("client-id-0001", 0)
	if !ok || op.State != operations.StateSucceeded || op.HTTPStatus != 200 || len(op.Steps) != 1 {
		t.Fatalf("op = %+v", op)
	}
	if strings.TrimSpace(string(op.Result)) != strings.TrimSpace(rec.Body.String()) {
		t.Errorf("result %s != body %s", op.Result, rec.Body)
	}

	// Duplicate → 409, invalid → 400; neither reaches the handler.
	if rec := do(t, srv, "POST", "/api/thing", "client-id-0001"); rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), `"duplicate_operation":true`) {
		t.Errorf("duplicate: code=%d body=%s", rec.Code, rec.Body)
	}
	if op, _ := h.operations.Get("client-id-0001", 0); op.HTTPStatus != 200 {
		t.Errorf("duplicate overwrote the original record: %+v", op)
	}
	if rec := do(t, srv, "POST", "/api/thing", "bad id!"); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid: code=%d", rec.Code)
	}

	// No header → generated ID.
	rec = do(t, srv, "POST", "/api/thing", "")
	gen := rec.Header().Get(OperationIDHeader)
	if !operations.ValidID(gen) {
		t.Fatalf("generated ID %q", gen)
	}
	if _, ok := h.operations.Get(gen, 0); !ok {
		t.Error("generated op not recorded")
	}

	// Failure status is recorded.
	do(t, srv, "POST", "/api/thing?fail=1", "client-id-0002")
	if op, _ := h.operations.Get("client-id-0002", 0); op.State != operations.StateFailed || op.HTTPStatus != 409 {
		t.Errorf("failed op = %+v", op)
	}

	// GET requests are untouched: no header, no ctx ID, even with a header.
	rec = do(t, srv, "GET", "/api/thing", "client-id-0003")
	if rec.Header().Get(OperationIDHeader) != "" || !strings.Contains(rec.Body.String(), `"op":""`) {
		t.Errorf("GET was wrapped: header=%q body=%s", rec.Header().Get(OperationIDHeader), rec.Body)
	}
	if _, ok := h.operations.Get("client-id-0003", 0); ok {
		t.Error("GET created an operation")
	}
}

func TestGetOperationEndpoint(t *testing.T) {
	_, srv := newOpsTestHandler()
	do(t, srv, "POST", "/api/thing", "client-id-0001")

	rec := do(t, srv, "GET", "/api/operations/client-id-0001?after=0", "")
	var op operations.Op
	if err := json.Unmarshal(rec.Body.Bytes(), &op); err != nil || rec.Code != 200 {
		t.Fatalf("code=%d err=%v body=%s", rec.Code, err, rec.Body)
	}
	if op.ID != "client-id-0001" || len(op.Steps) != 1 || op.Steps[0].Seq != 1 || op.Steps[0].Op != op.ID {
		t.Errorf("op = %+v", op)
	}
	rec = do(t, srv, "GET", "/api/operations/client-id-0001?after=1", "")
	json.Unmarshal(rec.Body.Bytes(), &op) //nolint:errcheck
	if len(op.Steps) != 0 {
		t.Errorf("after=1 steps = %+v", op.Steps)
	}
	if rec := do(t, srv, "GET", "/api/operations/client-id-0001?after=x", ""); rec.Code != 400 {
		t.Errorf("bad after: code=%d", rec.Code)
	}
	if rec := do(t, srv, "GET", "/api/operations/unknown-0001", ""); rec.Code != 404 {
		t.Errorf("unknown: code=%d", rec.Code)
	}
}
