package faq

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
		"GET /admin/faq", "POST /admin/faq", "POST /admin/faq/{id}",
		"POST /admin/faq/categories", "POST /admin/faq/categories/{id}")
}
