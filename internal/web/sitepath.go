package web

import (
	"net/url"
	"strings"
)

// SitePath reports whether raw is a path on this site, and returns it cleaned.
// A prefix check cannot do it: "//evil.example/x" begins with "/", and
// "https://ok@evil.example/" fools an allowlist through its userinfo.
func SitePath(raw string) (string, bool) {
	if raw == "" {
		return "", false
	}
	if strings.ContainsAny(raw, `\`) {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return "", false
	}
	if !strings.HasPrefix(u.Path, "/") {
		return "", false
	}
	return u.String(), true
}

// SitePathOr is SitePath with a fallback, for a caller that must redirect
// somewhere whatever the input was.
func SitePathOr(raw, fallback string) string {
	if p, ok := SitePath(raw); ok {
		return p
	}
	return fallback
}
