package pages

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
