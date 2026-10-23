package admin_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/user"
)

// TestControl_RequireAdminRoles: of the roles that reach the back office, only
// an admin reaches a handler guarded by RequireAdmin; signed out, a customer and
// a staff member get the same 404 and the handler does not run.
func TestControl_RequireAdminRoles(t *testing.T) {
	t.Parallel()

	guarded := admin.NewControl(slog.New(slog.DiscardHandler), nil).RequireAdmin(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("who works here"))
	})

	for _, tt := range []struct {
		name     string
		signedIn bool
		user     user.User
		want     int
	}{
		{name: "signed out", want: http.StatusNotFound},
		{name: "a customer", signedIn: true, user: user.User{ID: uuid.NewString(), Role: user.RoleCustomer}, want: http.StatusNotFound},
		{name: "a staff member", signedIn: true, user: user.User{ID: uuid.NewString(), Role: user.RoleStaff}, want: http.StatusNotFound},
		{name: "an admin", signedIn: true, user: user.User{ID: uuid.NewString(), Role: user.RoleAdmin}, want: http.StatusOK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/staff", nil)
			if tt.signedIn {
				req = req.WithContext(user.NewContext(req.Context(), tt.user))
			}
			w := httptest.NewRecorder()
			guarded(w, req)

			if w.Code != tt.want {
				t.Errorf("status is %d, want %d", w.Code, tt.want)
			}
			ran := strings.Contains(w.Body.String(), "who works here")
			if want := tt.want == http.StatusOK; ran != want {
				t.Errorf("the handler ran = %v, want %v", ran, want)
			}
		})
	}
}
