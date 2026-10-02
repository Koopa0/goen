package web

import (
	"net/http"

	"github.com/koopa0/goen/internal/i18n"
)

// Notice is the sentence a redirect asks a page to show: the message for the
// first parameter of the request that is set to 1.
func Notice(r *http.Request, messages map[string]i18n.Key) string {
	q := r.URL.Query()
	for name, k := range messages {
		if q.Get(name) == "1" {
			return i18n.T(r.Context(), k)
		}
	}
	return ""
}
