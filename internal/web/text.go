package web

import (
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"github.com/koopa0/goen/internal/i18n"
)

// RefuseUnstorableText answers 400 to a request whose path or query carries
// text PostgreSQL refuses to hold: bytes that are not UTF-8, or a NUL. No
// browser sends either, and path values and query values reach queries as they
// arrived, where the refusal becomes a 500. It belongs before routing, so
// nothing behind it reads one. A query pair that does not decode is left alone,
// as every reader of the query drops it.
func RefuseUnstorableText(next http.Handler, secure bool, refuse http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !storableText(r.URL.Path) || !queryStoresAsText(r.URL.RawQuery) {
			ctx := i18n.WithLocale(r.Context(), i18n.Detect(r, secure))
			refuse.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// queryStoresAsText reports whether every name and value in a raw query that
// decodes at all decodes to text PostgreSQL stores.
func queryStoresAsText(raw string) bool {
	for pair := range strings.SplitSeq(raw, "&") {
		for part := range strings.SplitSeq(pair, "=") {
			decoded, err := url.QueryUnescape(part)
			if err != nil {
				continue
			}
			if !storableText(decoded) {
				return false
			}
		}
	}
	return true
}

// HasControlChars reports whether s carries a control character.
// unicode.IsControl covers C1 (0x80–0x9F) as well as C0, which an ASCII-only
// check lets through.
func HasControlChars(s string) bool {
	return strings.ContainsFunc(s, unicode.IsControl)
}
