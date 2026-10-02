package customers

import (
	"log/slog"
	"net/http"
	"testing"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/admin/access/accesstest"
)

func TestEveryRouteRefusesAnOutsider(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.DiscardHandler)
	accesstest.RefuseOutsiders(t, func(mux *http.ServeMux, ac *access.Control) {
		NewHandler(&Store{}, log).Routes(mux, ac)
	}, "GET /admin/customers", "GET /admin/customers/{id}", "GET /admin/warranty")
}
