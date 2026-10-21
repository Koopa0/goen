// Package requestid is the identifier every log line of one request carries;
// it holds minting, validating and carrying it in the context; it does not
// hold the audit event it stamps, which is package admin/audit.
package requestid

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

type requestIDKey struct{}

// With records the identifier the log lines for this request carry.
func With(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// From is that identifier, or "" outside a request. The audit trail stamps
// it onto every back-office row, so a row and its log lines can be put together.
func From(ctx context.Context) string {
	id, ok := ctx.Value(requestIDKey{}).(string)
	if !ok {
		return ""
	}
	return id
}

// Handler gives every request an identifier and echoes it back. A
// client-supplied X-Request-Id is honoured only when it looks like an id: an
// arbitrary one reaches the logs, where a newline could forge a log line.
func Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if !valid(id) {
			id = uuid.NewString()
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(With(r.Context(), id)))
	})
}

func valid(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, c := range s {
		alphanumeric := (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
		if !alphanumeric && c != '-' {
			return false
		}
	}
	return true
}
