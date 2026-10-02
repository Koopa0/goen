package shipping

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
		"GET /admin/shipping", "POST /admin/shipping/version", "POST /admin/shipping/surcharge",
		"POST /admin/shipping/method", "POST /admin/shipping/method/{id}/active",
		"POST /admin/shipping/zone", "POST /admin/shipping/zone/{id}/prefixes",
		"POST /admin/shipping/zone/{id}/delete")
}
