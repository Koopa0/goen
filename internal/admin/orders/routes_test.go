package orders

import (
	"log/slog"
	"net/http"
	"testing"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/admin/accesstest"
)

func TestEveryRouteRefusesAnOutsider(t *testing.T) {
	t.Parallel()
	accesstest.RefuseOutsiders(t, func(mux *http.ServeMux, ac *admin.Control) {
		NewHandler(&Store{}, nil, slog.New(slog.DiscardHandler)).Routes(mux, ac)
	},
		"GET /admin/orders", "GET /admin/orders/{number}",
		"POST /admin/orders/{number}/status", "POST /admin/orders/{number}/ship",
		"POST /admin/orders/{number}/note", "POST /admin/orders/{number}/delivery",
		"POST /admin/orders/{number}/shipments/{shipment}/delivered",
		"POST /admin/orders/{number}/invoice", "POST /admin/orders/{number}/invoice/void",
		"POST /admin/orders/{number}/invoice/allowance")
}
