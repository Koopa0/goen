// Package comparison owns the shopper's temporary comparison selection.
package comparison

import (
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

const (
	Min        = 2
	Max        = 4
	CookieName = "goen_compare"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Normalize bounds untrusted URL and cookie values and reports a visible overflow.
func Normalize(raw []string) ([]string, bool) {
	out := make([]string, 0, Max)
	for _, slug := range raw {
		if len(slug) > 120 || !slugPattern.MatchString(slug) || slices.Contains(out, slug) {
			continue
		}
		if len(out) == Max {
			return out, true
		}
		out = append(out, slug)
	}
	return out, false
}

// Read does not create a cookie; only an explicit selection action persists one.
func Read(r *http.Request) []string {
	cookie, err := r.Cookie(CookieName)
	if err != nil || len(cookie.Value) > Max*121 {
		return nil
	}
	selected, _ := Normalize(strings.Split(cookie.Value, ","))
	return selected
}

func Write(w http.ResponseWriter, slugs []string, secure bool) {
	selected, _ := Normalize(slugs)
	//nolint:gosec // G124: Secure follows the deployment flag; plain HTTP requires the explicit development opt-out.
	cookie := &http.Cookie{Name: CookieName, Value: strings.Join(selected, ","), Path: "/", Secure: secure, HttpOnly: true, SameSite: http.SameSiteLaxMode}
	if len(selected) == 0 {
		cookie.MaxAge = -1
	}
	http.SetCookie(w, cookie)
}

func Href(slugs []string) string {
	q := url.Values{}
	for _, slug := range slugs {
		q.Add("p", slug)
	}
	if len(slugs) == 0 {
		q.Set("p", "")
	}
	return "/compare?" + q.Encode()
}
