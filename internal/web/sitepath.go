package web

import (
	"net/url"
	"strings"
)

// SitePath reports whether raw is a path on this site, and returns it cleaned.
//
// This is the one owner of that rule. It existed twice — once for the
// wishlist's `return` field and once for the language switch's — and a
// security-relevant rule written twice is a rule that drifts. A third caller
// (the back office's hero links) is what made keeping three copies indefensible.
//
// # What it refuses, and why each one matters
//
// A redirect target or link href taken from a form is an open redirect unless
// something says otherwise, and the "something" cannot be a prefix check:
//
//   - "//evil.example/x" begins with "/" and is a protocol-relative URL. This
//     is the one that catches people, because strings.HasPrefix(raw, "/") is
//     true for it. url.Parse puts evil.example in Host, which is refused.
//   - "https://evil.example" — an absolute URL, refused on Scheme.
//   - "javascript:alert(1)" — refused on Scheme too, and separately by templ's
//     own URL sanitiser, but a value that never lands is better than one that
//     is neutralised at render time.
//   - "https://ok@evil.example/" — userinfo, which is how an allowlist written
//     against a prefix gets fooled. Refused on User.
//   - `/\evil.example` — url.String escapes the backslash to %5C, which IS
//     safe, but that safety rests on a detail of Go's escaping meeting a detail
//     of URL parsing. No legitimate path on this site contains one; refusing it
//     leaves nothing to reason about.
//
// The returned string is u.String(), so the query and fragment survive — a
// return to "/search?q=abc" should land back on the search results.
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
//
// A refused target sends the visitor to fallback rather than erroring: a 400 on
// a language switch is a dead end for somebody who cannot read the page they
// are on, and the worst case of the fallback is a redirect they did not expect.
func SitePathOr(raw, fallback string) string {
	if p, ok := SitePath(raw); ok {
		return p
	}
	return fallback
}
