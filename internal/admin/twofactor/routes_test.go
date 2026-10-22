package twofactor

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/admin/accesstest"
	"github.com/koopa0/goen/internal/user"
)

// TestHandler_RoutesRefuseOutsiders: every route refuses an outsider. Each
// route answers a signed-out visitor and a signed-in customer with the guard's
// page.
func TestHandler_RoutesRefuseOutsiders(t *testing.T) {
	t.Parallel()
	h := keylessHandler(t)
	accesstest.RefuseOutsiders(t, func(mux *http.ServeMux, ac *admin.Control) {
		h.Routes(mux, ac)
	}, "GET /admin/verify", "POST /admin/verify", "POST /admin/verify/enrol", "POST /admin/verify/confirm")
}

// TestHandler_RoutesOpenBeforeSecondFactor: every route opens before the second
// factor. These pages are where every other back-office route sends staff who
// have not passed the second factor, so they must open to them: behind the
// step-up they would send the same staff member back to themselves forever.
func TestHandler_RoutesOpenBeforeSecondFactor(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	notVerified := func(*http.Request) (bool, error) { return false, nil }
	keylessHandler(t).Routes(mux, admin.NewControl(slog.New(slog.DiscardHandler), notVerified))
	for _, tt := range []struct {
		method, path string
		code         int
		location     string
	}{
		{http.MethodGet, "/admin/verify", http.StatusOK, ""},
		{http.MethodPost, "/admin/verify", http.StatusSeeOther, "/admin/verify?disabled=1"},
		{http.MethodPost, "/admin/verify/enrol", http.StatusSeeOther, "/admin/verify?disabled=1"},
		{http.MethodPost, "/admin/verify/confirm", http.StatusSeeOther, "/admin/verify?disabled=1"},
	} {
		ctx := user.NewContext(t.Context(), user.User{ID: uuid.NewString(), Role: user.RoleStaff})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, tt.method, tt.path, http.NoBody))
		if rec.Code != tt.code || rec.Header().Get("Location") != tt.location {
			t.Errorf("%s %s before the second factor = %d to %q, want %d to %q from the page itself",
				tt.method, tt.path, rec.Code, rec.Header().Get("Location"), tt.code, tt.location)
		}
	}
}
