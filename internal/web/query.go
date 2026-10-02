package web

import (
	"net/http"
	"net/url"
	"slices"
)

// DropEmptyParams answers a request whose query string carries a parameter with
// no value, which is what an untouched form field submits, so a shared address
// never reads min_price=&sort=. A browser navigation is redirected to the address
// without them and DropEmptyParams reports true; the caller stops there. An htmx
// request keeps its response, and the address htmx pushes is replaced by the
// clean one. A parameter named in keep stays when empty: a search box submitted
// blank is still a search.
func DropEmptyParams(w http.ResponseWriter, r *http.Request, keep ...string) (redirected bool) {
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
	if IsHTMX(r) {
		w.Header().Set("HX-Push-Url", target.RequestURI())
		return false
	}
	http.Redirect(w, r, target.RequestURI(), http.StatusSeeOther)
	return true
}
