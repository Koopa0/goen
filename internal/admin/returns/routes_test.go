package returns

import (
	"log/slog"
	"net/http"
	"testing"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/admin/accesstest"
)

// TestHandler_RoutesRefuseOutsiders: every route refuses an outsider.
func TestHandler_RoutesRefuseOutsiders(t *testing.T) {
	t.Parallel()
	accesstest.RefuseOutsiders(t, func(mux *http.ServeMux, ac *admin.Control) {
		NewHandler(&Store{}, slog.New(slog.DiscardHandler)).Routes(mux, ac)
	},
		"GET /admin/returns", "POST /admin/returns/{id}/decide", "POST /admin/returns/{id}/assess",
		"POST /admin/returns/{id}/inspect", "POST /admin/returns/{id}/complete")
}
