package orderaccess

import (
	"log/slog"
	"net/http"

	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

// ReloadSameSite answers a refused cross-site navigation with a page that loads
// the same URL again, and reports whether it did. The member session cookie is
// SameSite=Strict, so a signed-in owner coming back from Stripe Checkout
// arrives without it; the second request is started by goen's own page, so it
// is same-site and carries the cookie.
//
// A page and not a 303: a redirect stays inside the cross-site navigation and
// the browser still withholds the cookie. Only an exact "cross-site": every
// other value is a request that carried the cookie already, the second request
// among them, and a browser that sends no Sec-Fetch-Site would be sent round
// forever. Only a GET or HEAD, because the refresh re-requests with GET.
//
// Call it only after refusing, and before reading the order: the answer is
// the same whether the order exists or not.
func ReloadSameSite(w http.ResponseWriter, r *http.Request, log *slog.Logger) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if r.Header.Get("Sec-Fetch-Site") != "cross-site" {
		return false
	}
	target := r.URL.EscapedPath()
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Add("Vary", "Sec-Fetch-Site")
	h.Set("X-Robots-Tag", "noindex")
	h.Set("Refresh", "0; url="+target)
	web.Render(w, r, log, http.StatusOK, pages.OrderReload(target))
	return true
}
