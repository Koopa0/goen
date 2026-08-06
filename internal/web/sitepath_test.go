package web

import "testing"

// TestSitePathRefusesAnythingButAPathOnThisSite proves each real bypass is
// refused.
//
// This rule guards three doors — the wishlist's return field, the language
// switch's, and the hero links the back office writes. It lived in two copies
// before the third arrived; one owner is what stops the copies drifting, and
// this is the one test that has to be right.
//
// Each refusal below is a real bypass of the naive check (strings.HasPrefix
// "/"), not a hypothetical.
func TestSitePathRefusesAnythingButAPathOnThisSite(t *testing.T) {
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

		{"empty", "", ""},
		// The one that catches people: it begins with "/" and is not a path.
		{"protocol-relative", "//evil.example/x", ""},
		{"protocol-relative with a path after", "//evil.example", ""},
		{"backslash", `/\evil.example`, ""},
		{"backslash pair", `\\evil.example`, ""},
		{"absolute https", "https://evil.example", ""},
		{"absolute http", "http://evil.example/deals", ""},
		{"javascript", "javascript:alert(1)", ""},
		{"data", "data:text/html,<script>alert(1)</script>", ""},
		// Userinfo is how a prefix-based allowlist gets fooled.
		{"userinfo", "https://goen.example@evil.example/", ""},
		{"userinfo without a scheme", "//goen.example@evil.example/", ""},
		{"relative", "deals", ""},
		{"parent", "../etc", ""},
		{"a control character", "/deals\n/evil", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
		})
	}
}

// TestSitePathOrFallsBackRatherThanErroring proves a refused target is not a
// dead end.
//
// A refused target must not be a dead end. Somebody switching language cannot
// read the error page they would land on.
func TestSitePathOrFallsBackRatherThanErroring(t *testing.T) {
	if got := SitePathOr("//evil.example", "/"); got != "/" {
		t.Errorf("a refused target gave %q, want the fallback", got)
	}
	if got := SitePathOr("/deals", "/"); got != "/deals" {
		t.Errorf("a good target gave %q", got)
	}
	// The fallback is returned verbatim: it is the CALLER's constant, not user
	// input, so validating it would be validating our own source code.
	if got := SitePathOr("", "/account/wishlist"); got != "/account/wishlist" {
		t.Errorf("empty gave %q", got)
	}
}
