package keyset

import (
	"encoding/json"
	"net/url"
	"testing"
)

// FuzzReadKeysetOnlyAcceptsItsOwnScope: a token minted for one scope is worth
// nothing under another, whatever either scope or the position contains.
func FuzzReadKeysetOnlyAcceptsItsOwnScope(f *testing.F) {
	f.Add("/admin/orders", "/admin/products", "x")
	f.Add("/a?b=c", "/a", "")
	f.Add("/a", "/a\n", "1")
	f.Add("", "/", "{}")
	f.Add("/a\n/b", "/a", "\n")

	f.Fuzz(func(t *testing.T, scopeA, scopeB, position string) {
		body, err := json.Marshal(position)
		if err != nil {
			t.Fatal(err)
		}
		next, ok := NextURL(scopeA, string(body))
		if !ok {
			return
		}
		u, err := url.Parse(next)
		if err != nil {
			t.Fatalf("NextURL made %q: %v", next, err)
		}
		token := u.Query().Get(Param)

		if _, ok := Read[string](scopeA, token); !ok && len(token) <= maxKeysetToken {
			t.Fatalf("a token minted for %q is refused under its own scope", scopeA)
		}
		if scopeA != scopeB {
			if _, ok := Read[string](scopeB, token); ok {
				t.Fatalf("a token minted for %q was accepted under %q", scopeA, scopeB)
			}
		}
	})
}
