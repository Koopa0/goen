package web

import (
	"net/url"
	"strings"
	"unicode"
)

// SitePath reports whether raw is a path on this site, as a BROWSER will
// resolve it, and returns it cleaned.
//
// The rule is stated here rather than delegated to net/url, and that is the
// point. net/url follows RFC 3986, where an authority is exactly two slashes;
// browsers follow WHATWG, whose special-authority-ignore-slashes state skips
// ANY run of "/" or "\" and then reads a host. So url.Parse reports Host=""
// for "///evil.example/x" while every browser resolves it off-site.
func SitePath(raw string) (string, bool) {
	// (1) WHATWG strips U+0009, U+000A and U+000D from anywhere in a URL
	// before parsing, so "/<TAB>/evil.example" is "//evil.example" to a
	// browser; it also trims leading C0-and-space. Refuse every control
	// character outright: none belongs in a link, and CR/LF in a Location
	// header is response splitting. net/url currently rejects some of these
	// too, but an inherited parser error is not this rule.
	if raw == "" || hasControl(raw) {
		return "", false
	}
	// (2) Rooted, tested on the RAW string. u.Path is percent-decoded, so
	// "/%2f%2fevil.example" arrives there as "///evil.example" although a
	// browser keeps it same-origin: the decoded path answers a different
	// question from the one asked.
	if raw[0] != '/' {
		return "", false
	}
	// (3) A second "/" or "\" is where a browser starts reading a host.
	// Position 1 alone covers "//", "///", "/\" and every longer run,
	// because the ignore-slashes state consumes the rest of the run for us.
	if len(raw) > 1 && (raw[1] == '/' || raw[1] == '\\') {
		return "", false
	}
	// (4) A backslash anywhere: a browser normalises it to a slash in a
	// special-scheme URL, so a later one can move a segment boundary. No route
	// in this shop contains one.
	if strings.ContainsRune(raw, '\\') {
		return "", false
	}
	// (5) A scheme, an authority or userinfo means it was never a path. This is
	// a backstop after (2)-(4), not the rule.
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Opaque != "" || u.Host != "" || u.User != nil {
		return "", false
	}
	return u.String(), true
}

// SiteOrigin reports whether raw is an origin goen can build absolute links
// from, and returns it canonicalised with no trailing slash. HasPrefix(raw,
// "http") cannot do it: "httpx://" passes a five-character string test but
// is not one of the web schemes goen supports, and "https://ok@evil.example"
// carries userinfo a mail client renders as the destination.
func SiteOrigin(raw string) (origin, scheme string, ok bool) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") ||
		u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", "", false
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host, "/"), u.Scheme, true
}

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// SitePathOr is SitePath with a fallback, for a caller that must redirect
// somewhere whatever the input was.
func SitePathOr(raw, fallback string) string {
	if p, ok := SitePath(raw); ok {
		return p
	}
	return fallback
}
