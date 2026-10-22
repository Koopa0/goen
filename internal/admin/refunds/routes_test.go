package refunds

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

func TestEveryRouteRefusesAnOutsider(t *testing.T) {
	t.Parallel()
	accesstest.RefuseOutsiders(t, func(mux *http.ServeMux, ac *admin.Control) {
		NewHandler(&Store{}, nil, slog.New(slog.DiscardHandler)).Routes(mux, ac)
	}, "POST /admin/orders/{number}/refund", "POST /admin/orders/{number}/refund-unsent")
}

// A money door waits for the session's second factor: staff who have not
// proved it are sent to prove it, and nothing behind the door runs.
func TestEveryRouteWaitsForTheSecondFactor(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	notVerified := func(*http.Request) (bool, error) { return false, nil }
	NewHandler(&Store{}, nil, slog.New(slog.DiscardHandler)).Routes(mux, admin.NewControl(slog.New(slog.DiscardHandler), notVerified))
	// An order number the handlers refuse: one that got past the guard would
	// answer 404 rather than reach a store this test does not have.
	for _, path := range []string{"/admin/orders/x/refund", "/admin/orders/x/refund-unsent"} {
		ctx := user.NewContext(t.Context(), user.User{ID: uuid.NewString(), Role: user.RoleStaff})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodPost, path, http.NoBody))
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/verify" {
			t.Errorf("POST %s without the second factor = %d to %q, want 303 to /admin/verify",
				path, rec.Code, rec.Header().Get("Location"))
		}
	}
}
