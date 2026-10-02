package campaigns

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
		"GET /admin/campaigns", "POST /admin/campaigns", "GET /admin/campaigns/{slug}",
		"POST /admin/campaigns/{slug}/products", "POST /admin/campaigns/{slug}/tone",
		"POST /admin/campaigns/{slug}/image", "POST /admin/campaigns/{slug}/image/remove",
		"POST /admin/campaigns/{slug}/active", "POST /admin/campaigns/{slug}/window")
}
