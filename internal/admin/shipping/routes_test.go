package shipping

import (
	"log/slog"
	"net/http"
	"testing"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/admin/accesstest"
)

// TestHandler_RoutesRefuseOutsiders: every route refuses an outsider. Each
// route answers a signed-out visitor and a signed-in customer with the guard's
// page.
func TestHandler_RoutesRefuseOutsiders(t *testing.T) {
	t.Parallel()
	accesstest.RefuseOutsiders(t, func(mux *http.ServeMux, ac *admin.Control) {
		NewHandler(&Store{}, false, slog.New(slog.DiscardHandler)).Routes(mux, ac)
	},
		"GET /admin/shipping", "POST /admin/shipping/version", "POST /admin/shipping/surcharge",
		"POST /admin/shipping/method", "POST /admin/shipping/method/{id}/active",
		"POST /admin/shipping/zone", "POST /admin/shipping/zone/{id}/prefixes",
		"POST /admin/shipping/zone/{id}/delete")
}
