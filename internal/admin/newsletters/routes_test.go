package newsletters

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
		"GET /admin/newsletters", "POST /admin/newsletters", "POST /admin/newsletters/{id}/send")
}
