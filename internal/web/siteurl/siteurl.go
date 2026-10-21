// Package siteurl is the shape of a URL on goen's own site; it holds the
// same-site path check, the configured origin and the slug shape; it does not
// hold any page, which is package site, or the response, which is package
// respond.
package siteurl

import (
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

// Path reports whether raw is a path on this site, as a BROWSER will
// resolve it, and returns it cleaned.
//
// The rule is stated here rather than delegated to net/url, and that is the
// point. net/url follows RFC 3986, where an authority is exactly two slashes;
// browsers follow WHATWG, whose special-authority-ignore-slashes state skips
// ANY run of "/" or "\" and then reads a host. So url.Parse reports Host=""
// for "///evil.example/x" while every browser resolves it off-site.
func Path(raw string) (string, bool) {
	// Rootedness is tested on the RAW string. u.Path is percent-decoded, so
	// "/%2f%2fevil.example" arrives there as "///evil.example" although a
	// browser keeps it same-origin: the decoded path answers a different
	// question from the one asked.
	if !hasPathShape(raw) {
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
	// beside a raw space, net/url cannot retain RawPath and turns "/%2f " into
	// "//%20". Reapply the browser rule to what the caller will actually put in
	// a Location header.
	if !hasPathShape(clean) {
		return "", false
	}
	return clean, true
}

func hasPathShape(raw string) bool {
	// WHATWG strips U+0009, U+000A and U+000D from anywhere in a URL before
	// parsing, so "/<TAB>/evil.example" is "//evil.example" to a browser; it
	// also trims leading C0-and-space. Refuse every control character outright:
	// none belongs in a link, and CR/LF in a Location header is response
	// splitting. net/url rejecting some of these is not this browser rule.
	if raw == "" || hasControlChars(raw) || raw[0] != '/' {
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

// Origin reports whether raw is an origin goen can build absolute links
// from, and returns it canonicalised with no trailing slash. HasPrefix(raw,
// "http") cannot do it: "httpx://" passes a five-character string test but
// is not one of the web schemes goen supports, and "https://ok@evil.example"
// carries userinfo a mail client renders as the destination.
func Origin(raw string) (origin, scheme string, ok bool) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") ||
		!linkableHost(u.Hostname()) || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", "", false
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host, "/"), u.Scheme, true
}

// linkableHost refuses what a listen address allows and a link cannot use.
// u.Host is ":9701" for "http://:9701", so only the hostname shows it is
// empty, and 0.0.0.0 or :: means every interface to a listener but no machine
// to a customer following a Stripe return URL or an emailed link, whether it
// is spelled IPv4-mapped or with a zone.
func linkableHost(host string) bool {
	if host == "" {
		return false
	}
	addr, err := netip.ParseAddr(host)
	return err != nil || !addr.Unmap().WithZone("").IsUnspecified()
}

// PathOr is Path with the caller's own answer for a raw that is not a path on
// this site.
func PathOr(raw, fallback string) string {
	if p, ok := Path(raw); ok {
		return p
	}
	return fallback
}

// hasControlChars reports whether s carries a control character.
// unicode.IsControl covers C1 (0x80–0x9F) as well as C0, which an ASCII-only
// check lets through.
func hasControlChars(s string) bool {
	return strings.ContainsFunc(s, unicode.IsControl)
}

var slugShape = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidSlug is the shape of an address segment goen mints: lower-case words
// joined by single hyphens.
func ValidSlug(s string) bool { return slugShape.MatchString(s) }
