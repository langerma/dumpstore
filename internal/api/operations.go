package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"dumpstore/internal/operations"
)

// OperationIDHeader carries the client-chosen operation ID of a write
// request; the server echoes it (or a generated one) in the response (#184).
const OperationIDHeader = "X-Operation-ID"

// isWriteRequest reports whether r is an API write that becomes an operation.
func isWriteRequest(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return strings.HasPrefix(r.URL.Path, "/api/")
}

// Operations wraps next so every API write request is tracked as an
// operation: the ID is validated (or generated), registered, echoed in the
// response header and put in the request context, and the final status and
// body are recorded once next returns.
func (h *Handler) Operations(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isWriteRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		id := r.Header.Get(OperationIDHeader)
		if id == "" {
			id = operations.NewID()
		}
		if err := h.operations.Begin(id); err != nil {
			if errors.Is(err, operations.ErrDuplicate) {
				// Usually a transport-level retransmission of a request
				// whose response was lost (browsers retry on a reset
				// keep-alive socket). The flag tells the client to fetch
				// the original result instead of reporting an error.
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "duplicate_operation": true})
				return
			}
			writeError(r.Context(), w, http.StatusBadRequest, err, nil)
			return
		}
		w.Header().Set(OperationIDHeader, id)
		rec := &opRecorder{ResponseWriter: w, status: http.StatusOK}
		defer func() { h.operations.Finish(id, rec.status, rec.body()) }()
		next.ServeHTTP(rec, r.WithContext(operations.WithID(r.Context(), id)))
	})
}

// opRecorder records the status code and tees up to MaxResultBytes+1 bytes
// of the response body (one extra byte marks a truncated result).
type opRecorder struct {
	http.ResponseWriter
	status int
	buf    bytes.Buffer
}

func (o *opRecorder) WriteHeader(code int) {
	o.status = code
	o.ResponseWriter.WriteHeader(code)
}

func (o *opRecorder) Write(p []byte) (int, error) {
	if room := operations.MaxResultBytes + 1 - o.buf.Len(); room > 0 {
		o.buf.Write(p[:min(room, len(p))])
	}
	return o.ResponseWriter.Write(p)
}

func (o *opRecorder) Flush() {
	if f, ok := o.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (o *opRecorder) body() []byte { return o.buf.Bytes() }

// getOperation handles GET /api/operations/{id}?after=<seq>.
// Returns the operation record with steps whose seq > after.
func (h *Handler) getOperation(w http.ResponseWriter, r *http.Request) {
	after := 0
	if s := r.URL.Query().Get("after"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("after must be a non-negative integer"), nil)
			return
		}
		after = n
	}
	op, ok := h.operations.Get(r.PathValue("id"), after)
	if !ok {
		writeError(r.Context(), w, http.StatusNotFound, fmt.Errorf("operation not found or expired"), nil)
		return
	}
	writeJSON(r.Context(), w, op)
}
