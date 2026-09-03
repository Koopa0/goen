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
	// Rootedness is tested on the RAW string. u.Path is percent-decoded, so
	// "/%2f%2fevil.example" arrives there as "///evil.example" although a
	// browser keeps it same-origin: the decoded path answers a different
	// question from the one asked.
	if !hasSitePathShape(raw) {
		return "", false
	}
	// A scheme, an authority or userinfo means it was never a path. This is a
	// backstop after the browser-shape rule, not the rule itself.
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Opaque != "" || u.Host != "" || u.User != nil {
		return "", false
	}
	clean := u.String()
	// URL.String canonicalises raw characters. When a valid escaped slash sits
	// beside a raw space, net/url cannot retain RawPath and used to turn
	// "/%2f " into "//%20". Reapply the browser rule to what the caller will
	// actually put in a Location header.
	if !hasSitePathShape(clean) {
		return "", false
	}
	return clean, true
}

func hasSitePathShape(raw string) bool {
	// WHATWG strips U+0009, U+000A and U+000D from anywhere in a URL before
	// parsing, so "/<TAB>/evil.example" is "//evil.example" to a browser; it
	// also trims leading C0-and-space. Refuse every control character outright:
	// none belongs in a link, and CR/LF in a Location header is response
	// splitting. net/url rejecting some of these is not this browser rule.
	if raw == "" || hasControl(raw) || raw[0] != '/' {
		return false
	}
	// A second "/" or "\" is where a browser starts reading a host. Position
	// one covers every longer run because the ignore-slashes state consumes it.
	if len(raw) > 1 && (raw[1] == '/' || raw[1] == '\\') {
		return false
	}
	// A browser normalises any later backslash to a slash in a special-scheme
	// URL, which can move a segment boundary. No route in this shop contains one.
	return !strings.ContainsRune(raw, '\\')
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
	return strings.ContainsFunc(s, unicode.IsControl)
}

// SitePathOr is SitePath with a fallback, for a caller that must redirect
// somewhere whatever the input was.
func SitePathOr(raw, fallback string) string {
	if p, ok := SitePath(raw); ok {
		return p
	}
	return fallback
}
