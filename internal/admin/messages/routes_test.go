package messages

import (
	"log/slog"
	"net/http"
	"testing"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/admin/accesstest"
)

func TestEveryRouteRefusesAnOutsider(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.DiscardHandler)
	accesstest.RefuseOutsiders(t, func(mux *http.ServeMux, ac *admin.Control) {
		NewHandler(&Store{}, log).Routes(mux, ac)
	}, "GET /admin/messages", "POST /admin/messages/handle", "POST /admin/messages/reopen")
}
