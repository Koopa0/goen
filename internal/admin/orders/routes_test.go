package orders

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
	routetest.RefuseOutsiders(t, func(mux *http.ServeMux, ac *admin.Control) {
		NewHandler(&Store{}, nil, slog.New(slog.DiscardHandler)).Routes(mux, ac)
	},
		"GET /admin/orders", "GET /admin/orders/{number}", "GET /admin/orders/picking/slips", "POST /admin/orders/picking",
		"POST /admin/orders/{number}/status", "POST /admin/orders/{number}/ship",
		"POST /admin/orders/{number}/note", "POST /admin/orders/{number}/delivery",
		"POST /admin/orders/{number}/shipments/{shipment}/delivered",
		"POST /admin/orders/{number}/invoice", "POST /admin/orders/{number}/invoice/void",
		"POST /admin/orders/{number}/invoice/allowance")
}
