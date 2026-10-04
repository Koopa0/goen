package orders

import (
	"log/slog"
	"net/http"
	"testing"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/admin/access/accesstest"
)

func TestEveryRouteRefusesAnOutsider(t *testing.T) {
	t.Parallel()
	accesstest.RefuseOutsiders(t, func(mux *http.ServeMux, ac *access.Control) {
		NewHandler(&Store{}, nil, slog.New(slog.DiscardHandler)).Routes(mux, ac)
	},
		"GET /admin", "GET /admin/orders", "GET /admin/orders/{number}",
		"POST /admin/orders/{number}/status", "POST /admin/orders/{number}/ship",
		"POST /admin/orders/{number}/note", "POST /admin/orders/{number}/delivery")
}
