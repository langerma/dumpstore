package api

import "context"

type reqIDKey struct{}

// WithReqID returns a new context carrying the given request ID.
func WithReqID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, reqIDKey{}, id)
}

// ReqIDFromContext returns the request ID stored in ctx, or "" if none.
func ReqIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(reqIDKey{}).(string)
	return id
}
