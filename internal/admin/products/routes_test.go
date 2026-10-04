package products

import (
	"log/slog"
	"net/http"
	"testing"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/admin/access/accesstest"
	"github.com/koopa0/goen/internal/media"
)

func TestEveryRouteRefusesAnOutsider(t *testing.T) {
	t.Parallel()
	accesstest.RefuseOutsiders(t, func(mux *http.ServeMux, ac *access.Control) {
		NewHandler(&Store{}, &media.Handler{}, slog.New(slog.DiscardHandler)).Routes(mux, ac)
	},
		"GET /admin/products", "POST /admin/products", "GET /admin/products/new",
		"GET /admin/products/{slug}", "POST /admin/products/{slug}", "POST /admin/products/{slug}/status",
		"POST /admin/products/{slug}/variants", "POST /admin/products/{slug}/options",
		"POST /admin/products/{slug}/options/values", "POST /admin/products/{slug}/specs",
		"POST /admin/products/{slug}/specs/remove", "POST /admin/products/{slug}/images",
		"POST /admin/products/{slug}/images/reuse", "POST /admin/products/{slug}/images/remove",
		"POST /admin/products/{slug}/images/option", "POST /admin/products/{slug}/images/move")
}
