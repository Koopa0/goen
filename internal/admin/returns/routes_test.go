package returns

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
		NewHandler(&Store{}, slog.New(slog.DiscardHandler)).Routes(mux, ac)
	},
		"GET /admin/returns", "POST /admin/returns/{id}/decide", "POST /admin/returns/{id}/assess",
		"POST /admin/returns/{id}/inspect", "POST /admin/returns/{id}/complete")
}
