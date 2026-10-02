package content

import (
	"log/slog"
	"net/http"
	"testing"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/admin/access/accesstest"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/newsletter"
)

func TestEveryRouteRefusesAnOutsider(t *testing.T) {
	t.Parallel()
	accesstest.RefuseOutsiders(t, func(mux *http.ServeMux, ac *access.Control) {
		NewHandler(&Store{}, &media.Handler{}, &newsletter.Store{}, slog.New(slog.DiscardHandler)).Routes(mux, ac)
	},
		"GET /admin/faq", "POST /admin/faq", "POST /admin/faq/{id}",
		"GET /admin/newsletter", "POST /admin/newsletter", "POST /admin/newsletter/{id}/send",
		"GET /admin/home", "POST /admin/home", "POST /admin/home/banner",
		"POST /admin/home/banner/{id}/active", "POST /admin/home/{id}/active",
		"POST /admin/home/{id}/promote")
}
