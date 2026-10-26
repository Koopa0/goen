package reviews

import (
	"log/slog"
	"net/http"
	"testing"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/admin/routetest"
)

// TestHandler_RoutesRefuseOutsiders: every route refuses an outsider.
func TestHandler_RoutesRefuseOutsiders(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.DiscardHandler)
	routetest.RefuseOutsiders(t, func(mux *http.ServeMux, ac *admin.Control) {
		NewHandler(&Store{}, log).Routes(mux, ac)
	}, "GET /admin/reviews", "POST /admin/reviews/hide", "POST /admin/reviews/show")
}
