package catalog

import (
	"net/http"
	"net/url"
	"slices"

	"github.com/koopa0/goen/internal/web/respond"
)

// dropEmptyParams answers a request whose query string carries a parameter with
// no value, which is what an untouched form field submits, so a shared address
// never reads min_price=&sort=. A browser navigation is redirected to the address
// without them and dropEmptyParams reports true; the caller stops there. An htmx
// request keeps its response, and the address htmx pushes is replaced by the
// clean one. A parameter named in keep stays when empty: a search box submitted
// blank is still a search.
func dropEmptyParams(w http.ResponseWriter, r *http.Request, keep ...string) (redirected bool) {
	clean := url.Values{}
	dropped := false
	for k, vs := range r.URL.Query() {
		for _, v := range vs {
			if v == "" && !slices.Contains(keep, k) {
				dropped = true
				continue
			}
			clean.Add(k, v)
		}
	}
	if !dropped {
		return false
	}
	target := *r.URL
	target.RawQuery = clean.Encode()
	if respond.IsHTMX(r) {
		w.Header().Set("HX-Push-Url", target.RequestURI())
		return false
	}
	// The target keeps this request's own path; only its query changes.
	http.Redirect(w, r, target.RequestURI(), http.StatusSeeOther) //nolint:gosec // G710: relative to this request, same origin
	return true
}
