package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"

	"dumpstore/internal/ansible"
	"dumpstore/internal/apispec"
	"dumpstore/internal/ops"
)

// httpError is an error carrying the HTTP status and body a typed handler
// wants returned. Any other error from a typed handler becomes a 500.
type httpError struct {
	code int
	body apiError
}

func (e *httpError) Error() string { return e.body.Error }

// TaskResult is the result of a write whose only output is its op-log
// steps.
type TaskResult struct {
	Tasks []ansible.TaskStep `json:"tasks"`
}

// errStatus returns err as an API error with the given status.
func errStatus(code int, err error) error {
	return &httpError{code: code, body: apiError{Error: err.Error()}}
}

// opError is a failed write: 500 with the op-log steps that ran, so the
// frontend can show which step failed. steps may be nil.
func opError(err error, steps []ansible.TaskStep) error {
	return &httpError{code: http.StatusInternalServerError, body: apiError{Error: err.Error(), Tasks: steps}}
}

// playbookError is opError for an Ansible run; out may be nil.
func playbookError(err error, out *ansible.PlaybookOutput) error {
	if out == nil {
		return opError(err, nil)
	}
	return opError(err, out.Steps())
}

// localError is opError for an in-process ops run; res may be nil.
func localError(err error, res *ops.Result) error {
	if res == nil {
		return opError(err, nil)
	}
	return opError(err, res.Steps())
}

type remoteAddrKey struct{}

// withRemoteAddr puts the client address into the request context, where
// auditLog reads it (handlers no longer see the *http.Request).
func withRemoteAddr(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), remoteAddrKey{}, r.RemoteAddr)))
	})
}

func remoteAddr(ctx context.Context) string {
	s, _ := ctx.Value(remoteAddrKey{}).(string)
	return s
}

// handle registers a typed handler (#181). Req is a struct whose fields
// carry path:"x" / query:"x" tags (decoded from the request) and at most
// one body:"" field (decoded from the JSON body; body:"optional" accepts an
// empty body). Param structs may be embedded. Resp is encoded as the
// JSON response. The spec's parameter, request and response schemas are
// taken from Req and Resp, so they always match what the handler uses.
func handle[Req, Resp any](reg *apispec.Registry, r apispec.Route, fn func(context.Context, Req) (Resp, error)) {
	reqT := reflect.TypeFor[Req]()
	if reqT.Kind() != reflect.Struct {
		panic(fmt.Sprintf("handle %s %s: request type must be a struct, got %s", r.Method, r.Path, reqT))
	}
	r.Params = reqT
	if f, ok := bodyField(reqT); ok {
		r.Request = f.Type
		r.RequestOptional = f.Tag.Get("body") == "optional"
	}
	r.Response = reflect.TypeFor[Resp]()
	r.Handler = withRemoteAddr(typed(fn, r.Status))
	reg.Handle(r)
}

func bodyField(t reflect.Type) (reflect.StructField, bool) {
	for _, f := range reflect.VisibleFields(t) {
		if _, ok := f.Tag.Lookup("body"); ok {
			return f, true
		}
	}
	return reflect.StructField{}, false
}

// typed adapts fn to http.Handler. Error bodies and log lines are exactly
// those of writeError / writeAPIError, so converting a handler does not
// change its responses. status is the success status (0 means 200); 204
// writes no body.
func typed[Req, Resp any](fn func(context.Context, Req) (Resp, error), status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		var req Req
		if err := bindRequest(r, &req); err != nil {
			writeError(ctx, w, http.StatusBadRequest, err, nil)
			return
		}
		resp, err := fn(ctx, req)
		if err != nil {
			if he, ok := errors.AsType[*httpError](err); ok {
				writeAPIError(ctx, w, he.code, he.body)
				return
			}
			writeError(ctx, w, http.StatusInternalServerError, err, nil)
			return
		}
		switch status {
		case 0, http.StatusOK:
			writeJSON(ctx, w, resp)
		case http.StatusNoContent:
			w.WriteHeader(status)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			writeJSON(ctx, w, resp)
		}
	})
}

// bindRequest fills the tagged fields of *dst (a struct) from r.
func bindRequest(r *http.Request, dst any) error {
	v := reflect.ValueOf(dst).Elem()
	// VisibleFields includes fields promoted from embedded param structs
	// (e.g. struct{ poolParam; Body X `body:""` }).
	for _, f := range reflect.VisibleFields(v.Type()) {
		if f.Anonymous {
			continue
		}
		fv := v.FieldByIndex(f.Index)
		if tag, ok := f.Tag.Lookup("body"); ok {
			err := decodeJSON(r, fv.Addr().Interface())
			if tag == "optional" && errors.Is(err, io.EOF) {
				continue // body:"optional" — an empty body leaves the zero value
			}
			if err != nil {
				return fmt.Errorf("invalid request body: %w", err)
			}
			continue
		}
		var name, raw string
		if name = f.Tag.Get("path"); name != "" {
			raw = r.PathValue(name)
		} else if name = f.Tag.Get("query"); name != "" {
			raw = r.URL.Query().Get(name)
		} else {
			continue
		}
		if err := setParam(fv, name, raw); err != nil {
			return err
		}
	}
	return nil
}

// setParam parses raw into fv. An absent (empty) value leaves the zero
// value; handlers that require a parameter check it themselves.
func setParam(fv reflect.Value, name, raw string) error {
	switch fv.Kind() {
	case reflect.String:
		fv.SetString(raw)
		return nil
	}
	if raw == "" {
		return nil
	}
	switch fv.Kind() {
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("%s must be true or false", name)
		}
		fv.SetBool(b)
	case reflect.Int, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("%s must be an integer", name)
		}
		fv.SetInt(n)
	case reflect.Uint, reflect.Uint64:
		// 63 bits: always fits an int, so handlers can convert freely.
		n, err := strconv.ParseUint(raw, 10, 63)
		if err != nil {
			return fmt.Errorf("%s must be a non-negative integer", name)
		}
		fv.SetUint(n)
	default:
		panic(fmt.Sprintf("bindRequest: unsupported parameter type %s for %s", fv.Type(), name))
	}
	return nil
}
