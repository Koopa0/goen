package web

import (
	"net/url"
	"strings"
	"testing"
)

// Every refusal below is a real bypass of a prefix check or of RFC 3986 parsing.
func TestSitePathRefusesAnythingButAPathOnThisSite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want string // "" means refused
	}{
		{"a page", "/deals", "/deals"},
		{"a page with a query", "/search?q=abc", "/search?q=abc"},
		{"a fragment survives", "/faq#shipping", "/faq#shipping"},
		{"the root", "/", "/"},
		{"an escaped path", "/p/%E6%89%8B%E6%A9%9F", "/p/%E6%89%8B%E6%A9%9F"},
		{"account", "/account", "/account"},
		{"account orders", "/account/orders", "/account/orders"},
		{"cart query", "/cart?x=1", "/cart?x=1"},
		{"slashes in a query", "/a?b=//c", "/a?b=//c"},
		{"slashes in a fragment", "/a#//b", "/a#//b"},
		{"encoded slashes stay encoded", "/%2f%2fevil.example", "/%2f%2fevil.example"},
		{"encoded backslashes stay encoded", "/%5C%5Cevil.example", "/%5C%5Cevil.example"},
		{"a path segment containing a space", "/ /evil.example", "/%20/evil.example"},

		{"empty", "", ""},
		{"protocol-relative", "//evil.example", ""},
		{"protocol-relative with a path", "//evil.example/x", ""},
		{"triple slash", "///evil.example/x", ""},
		{"quadruple slash", "////evil.example/x", ""},
		{"five slashes", "/////x", ""},
		{"bare triple slash", "///", ""},
		{"slash backslash", `/\evil.example`, ""},
		{"slash backslash slash", `/\/evil.example`, ""},
		{"two slashes then backslash", `//\evil.example`, ""},
		{"backslash pair", `\\evil.example`, ""},
		{"later backslash", `/a\b`, ""},
		{"tab stripped into an authority", "/\t/evil.example", ""},
		{"line feed stripped into an authority", "/\n/evil.example", ""},
		{"carriage return stripped into an authority", "/\r/evil.example", ""},
		{"leading space is trimmed", " //evil.example", ""},
		{"leading tab is stripped", "\t//evil.example", ""},
		{"triple slash userinfo", "///goen.example@evil.example/", ""},
		{"absolute https", "https://evil.example", ""},
		{"absolute http", "http://evil.example/deals", ""},
		{"userinfo without a scheme", "//goen.example@evil.example/", ""},
		{"userinfo", "https://goen.example@evil.example/", ""},
		{"javascript", "javascript:alert(1)", ""},
		{"data", "data:text/html,<script>alert(1)</script>", ""},
		{"relative", "deals", ""},
		{"parent", "../etc", ""},
		{"encoded slash is not raw rootedness", "%2Fdeals", ""},
		{"canonical escaped slash cannot become an authority", "/%2f ", ""},
		{"a control character", "/deals\n/evil", ""},
		{"header injection", "/x\r\nSet-Cookie: a=b", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := SitePath(tt.raw)
			if tt.want == "" {
				if ok {
					t.Errorf("SitePath(%q) accepted it as %q", tt.raw, got)
				}
				return
			}
			if !ok {
				t.Fatalf("SitePath(%q) refused a legitimate path", tt.raw)
			}
			if got != tt.want {
				t.Errorf("SitePath(%q) = %q, want %q", tt.raw, got, tt.want)
			}
			if origin := whatwgOrigin(tt.raw); origin != "goen.example" {
				t.Errorf("accepted %q, which a browser resolves at %q", tt.raw, origin)
			}
			if origin := whatwgOrigin(got); origin != "goen.example" {
				t.Errorf("returned %q, which a browser resolves at %q", got, origin)
			}
			if gotAgain, okAgain := SitePath(got); !okAgain || gotAgain != got {
				t.Errorf("output %q is not itself accepted unchanged: got %q, ok=%v",
					got, gotAgain, okAgain)
			}
		})
	}
}

// whatwgOrigin resolves ref against https://goen.example/checkout the way a
// browser does, not the way RFC 3986 does. url.ResolveReference is precisely
// the standard the code under test must not be measured by. This implements
// only the states that decide origin: strip tab/LF/CR, trim leading and trailing
// C0-and-space, and treat any leading run of slash or backslash after the first
// two as a special-scheme authority.
//
// The oracle is pinned to these measured Node new URL(ref, base) results:
//
//	///evil.example/x   -> https://evil.example/x
//	////evil.example/x  -> https://evil.example/x
//	/\/evil.example/x   -> https://evil.example/x
//	/<TAB>/evil.example -> https://evil.example/
//	/%2f%2fevil.example -> https://goen.example/%2f%2fevil.example
//	/ /evil.example     -> https://goen.example/%20/evil.example
//
// Percent-encoding is deliberately not decoded before origin is decided.
func whatwgOrigin(ref string) string {
	ref = strings.NewReplacer("\t", "", "\n", "", "\r", "").Replace(ref)
	ref = strings.TrimFunc(ref, func(r rune) bool { return r <= ' ' })
	if len(ref) >= 2 && isSlash(ref[0]) && isSlash(ref[1]) {
		i := 2
		for i < len(ref) && isSlash(ref[i]) {
			i++
		}
		end := strings.IndexAny(ref[i:], `/\?#`)
		if end < 0 {
			return ref[i:]
		}
		return ref[i : i+end]
	}
	if u, err := url.Parse(ref); err == nil && u.IsAbs() && u.Hostname() != "" {
		return u.Hostname()
	}
	return "goen.example"
}

func isSlash(b byte) bool { return b == '/' || b == '\\' }

// TestSitePathOrFallsBackRatherThanErroring: somebody switching language cannot
// read the error page they would otherwise land on.
func TestSitePathOrFallsBackRatherThanErroring(t *testing.T) {
	if got := SitePathOr("//evil.example", "/"); got != "/" {
		t.Errorf("a refused target gave %q, want the fallback", got)
	}
	if got := SitePathOr("/deals", "/"); got != "/deals" {
		t.Errorf("a good target gave %q", got)
	}
	// The fallback is returned verbatim: it is the caller's constant.
	if got := SitePathOr("", "/account/wishlist"); got != "/account/wishlist" {
		t.Errorf("empty gave %q", got)
	}
}

func TestSiteOriginRefusesWhatIsNotAnOrigin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		raw        string
		wantOrigin string
		wantScheme string
	}{
		{"an https origin", "https://shop.example", "https://shop.example", "https"},
		{"a trailing slash is canonicalised", "https://shop.example/", "https://shop.example", "https"},
		{"the development origin", "http://127.0.0.1:9700", "http://127.0.0.1:9700", "http"},

		{"an http-looking scheme", "httpx://evil.example", "", ""},
		{"an https-looking scheme", "httpsss://evil.example", "", ""},
		{"no scheme", "shop.example", "", ""},
		{"userinfo", "https://ok@evil.example", "", ""},
		{"a path", "https://shop.example/deep/path", "", ""},
		{"a query", "https://shop.example?q=1", "", ""},
		{"a fragment", "https://shop.example#part", "", ""},
		{"an empty host", "https://", "", ""},
		{"empty", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			origin, scheme, ok := SiteOrigin(tt.raw)
			if tt.wantOrigin == "" {
				if ok {
					t.Errorf("SiteOrigin(%q) accepted it as %q with scheme %q",
						tt.raw, origin, scheme)
				}
				if origin != "" || scheme != "" {
					t.Errorf("SiteOrigin(%q) refused with outputs %q, %q; want empty outputs",
						tt.raw, origin, scheme)
				}
				return
			}
			if !ok {
				t.Fatalf("SiteOrigin(%q) refused a legitimate origin", tt.raw)
			}
			if origin != tt.wantOrigin || scheme != tt.wantScheme {
				t.Errorf("SiteOrigin(%q) = (%q, %q), want (%q, %q)",
					tt.raw, origin, scheme, tt.wantOrigin, tt.wantScheme)
			}
		})
	}
}

// FuzzSitePath holds the same-site parser to properties that remain true for
// every input, including malformed UTF-8: refusal has no residual output, and
// every accepted canonical path is safe to feed back unchanged.
func FuzzSitePath(f *testing.F) {
	for _, seed := range []string{
		"", "/", "/deals", "/search?q=abc#part", "//evil.example",
		"///evil.example/x", `/\evil.example`, "/%2f%2fevil.example", "/%2f ",
		"/\t/evil.example", "https://evil.example", string([]byte{'/', 0xff}),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		got, ok := SitePath(raw)
		if !ok {
			if got != "" {
				t.Errorf("SitePath(%q) refused with output %q, want empty", raw, got)
			}
			return
		}
		if got == "" || got[0] != '/' {
			t.Fatalf("SitePath(%q) accepted non-rooted output %q", raw, got)
		}
		if hasControl(got) || strings.ContainsRune(got, '\\') {
			t.Errorf("SitePath(%q) returned unsafe path %q", raw, got)
		}
		if len(got) > 1 && (got[1] == '/' || got[1] == '\\') {
			t.Errorf("SitePath(%q) returned authority-shaped path %q", raw, got)
		}
		if origin := whatwgOrigin(got); origin != "goen.example" {
			t.Errorf("SitePath(%q) returned %q, which resolves at %q", raw, got, origin)
		}
		gotAgain, okAgain := SitePath(got)
		if !okAgain || gotAgain != got {
			t.Errorf("SitePath(%q) canonical output %q reparsed as %q, ok=%v",
				raw, got, gotAgain, okAgain)
		}
	})
}

// FuzzSiteOrigin pins canonical origins as a closed grammar: successful output
// reparses to the same value and scheme, while refusal never returns a partial
// origin that a caller could accidentally use.
func FuzzSiteOrigin(f *testing.F) {
	for _, seed := range []string{
		"", "https://shop.example", "https://shop.example/",
		"http://127.0.0.1:9700", "httpx://evil.example",
		"https://ok@evil.example", "https://shop.example/path?q=1#part",
		"https://[::1]:9700", string([]byte("https://shop.example/\xff")),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		origin, scheme, ok := SiteOrigin(raw)
		if !ok {
			if origin != "" || scheme != "" {
				t.Errorf("SiteOrigin(%q) refused with outputs %q, %q", raw, origin, scheme)
			}
			return
		}
		if scheme != "http" && scheme != "https" {
			t.Fatalf("SiteOrigin(%q) returned unsupported scheme %q", raw, scheme)
		}
		u, err := url.Parse(origin)
		if err != nil {
			t.Fatalf("SiteOrigin(%q) returned unparsable origin %q: %v", raw, origin, err)
		}
		if u.Scheme != scheme || u.Host == "" || u.User != nil ||
			u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			t.Errorf("SiteOrigin(%q) returned non-origin %q with scheme %q", raw, origin, scheme)
		}
		again, againScheme, againOK := SiteOrigin(origin)
		if !againOK || again != origin || againScheme != scheme {
			t.Errorf("SiteOrigin(%q) canonical output (%q, %q) reparsed as (%q, %q), ok=%v",
				raw, origin, scheme, again, againScheme, againOK)
		}
	})
}
