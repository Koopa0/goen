package auth

import (
	"strings"
	"testing"
)

func TestTheCartRecoveryPageReturnsToTheCart(t *testing.T) {
	t.Parallel()
	html := renderToString(t, CartRecovery(CartRecoveryMeta(t.Context()), CartRecoveryView{Next: "/account", Notice: "n", Retry: "r"}))
	back := tagCarrying(t, html, "notice__back")
	if !strings.Contains(back, `href="/cart"`) {
		t.Errorf("cart recovery does not return to the cart: %s", back)
	}
}

// tagCarrying is the opening tag of the element that carries needle.
func tagCarrying(t *testing.T, html, needle string) string {
	t.Helper()

	at := strings.Index(html, needle)
	if at < 0 {
		t.Fatalf("the rendered checkout carries no %s", needle)
	}
	start := strings.LastIndex(html[:at], "<")
	end := strings.Index(html[at:], ">")
	if start < 0 || end < 0 {
		t.Fatalf("%s is not inside a tag", needle)
	}
	return html[start : at+end+1]
}
