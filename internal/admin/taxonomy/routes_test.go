package taxonomy

import (
	"log/slog"
	"net/http"
	"testing"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/admin/accesstest"
	"github.com/koopa0/goen/internal/media"
)

// TestHandler_RoutesRefuseOutsiders: every route refuses an outsider.
func TestHandler_RoutesRefuseOutsiders(t *testing.T) {
	t.Parallel()
	accesstest.RefuseOutsiders(t, func(mux *http.ServeMux, ac *admin.Control) {
		NewHandler(&Store{}, &media.Handler{}, slog.New(slog.DiscardHandler)).Routes(mux, ac)
	},
		"GET /admin/taxonomy", "POST /admin/taxonomy/{kind}", "POST /admin/taxonomy/{kind}/{slug}",
		"GET /admin/categories/{slug}", "POST /admin/categories/{slug}/image",
		"POST /admin/categories/{slug}/image/remove")
}
