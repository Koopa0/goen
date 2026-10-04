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

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/user"
)

// RefuseOutsiders registers a feature's routes and asks each as somebody signed
// out and as a signed-in customer. Both must get the page the guard answers: a
// route registered without its guard would run the handler, which here has no
// database behind it, or which may 404 by itself on a path value it refuses.
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
				return user.NewContext(ctx, user.User{ID: uuid.NewString(), Role: user.RoleCustomer})
			},
		} {
			t.Run(route+" "+name, func(t *testing.T) {
				w := httptest.NewRecorder()
				req := httptest.NewRequestWithContext(as(t.Context()), method, path, nil)
				mux.ServeHTTP(w, req)
				if w.Code != http.StatusNotFound ||
					!strings.Contains(w.Body.String(), i18n.T(req.Context(), i18n.KeyAdminNotFoundHead)) {
					t.Errorf("%s answered %d to %s, want the guard's 404 page", route, w.Code, name)
				}
			})
		}
	}
}
