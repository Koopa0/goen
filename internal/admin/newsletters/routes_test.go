package newsletters

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
		NewHandler(&Store{}, slog.New(slog.DiscardHandler)).Routes(mux, ac)
	},
		"GET /admin/newsletters", "POST /admin/newsletters", "POST /admin/newsletters/{id}/send")
}
