// Package accesstest is the check every back-office feature runs on its own
// routes: that each one is behind the access guard, which a route table moved
// out of server.go no longer shows in one place.
package accesstest

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/admin/access"
)

// RefuseOutsiders registers a feature's routes and asks each as somebody signed
// out and as a signed-in customer. Both must get the 404 every outsider gets: a
// route registered without its guard would run the handler, which here has no
// database behind it.
func RefuseOutsiders(t *testing.T, register func(*http.ServeMux, *access.Control), routes ...string) {
	t.Helper()
	mux := http.NewServeMux()
	register(mux, access.New(slog.New(slog.DiscardHandler), nil))
	for _, route := range routes {
		method, pattern, _ := strings.Cut(route, " ")
		path := pattern
		for strings.Contains(path, "{") {
			start := strings.Index(path, "{")
			path = path[:start] + "x" + path[strings.Index(path, "}")+1:]
		}
		for name, as := range map[string]func(context.Context) context.Context{
			"signed out": func(ctx context.Context) context.Context { return ctx },
			"a customer": func(ctx context.Context) context.Context {
				return account.WithUser(ctx, account.User{ID: uuid.NewString(), Role: account.RoleCustomer})
			},
		} {
			t.Run(route+" "+name, func(t *testing.T) {
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, httptest.NewRequestWithContext(as(t.Context()), method, path, nil))
				if w.Code != http.StatusNotFound {
					t.Errorf("%s answered %d to %s, want the 404 an outsider gets", route, w.Code, name)
				}
			})
		}
	}
}
