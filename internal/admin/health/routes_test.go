package health

import (
	"log/slog"
	"net/http"
	"testing"

	"github.com/koopa0/goen/internal/outbox"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/admin/accesstest"
)

func TestEveryRouteRefusesAnOutsider(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.DiscardHandler)
	accesstest.RefuseOutsiders(t, func(mux *http.ServeMux, ac *admin.Control) {
		NewHandler(&Store{}, &outbox.Store{}, nil, nil, log).Routes(mux, ac)
	}, "GET /admin/health", "POST /admin/health/reconcile")
}
