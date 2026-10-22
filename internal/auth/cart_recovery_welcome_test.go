package auth

import (
	"html"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

// TestCartRecoveryWelcome: after registration the cart-recovery page keeps the
// welcome sentence, the retry action, the original destination and the
// merge-failed notice. The account page's half of this test is
// internal/account's TestAccountWelcomeKeepsTheOriginalAction.
func TestCartRecoveryWelcome(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			welcome := "Your account is created. Welcome."
			if locale == i18n.ZhHant {
				welcome = "\u5e33\u865f\u5df2\u5efa\u7acb\uff0c\u6b61\u8fce\u52a0\u5165\u3002"
			}
			recovery := renderComponent(t, ctx, CartRecovery(CartRecoveryMeta(ctx), CartRecoveryView{
				Welcome: true, Next: "/account?welcome=1", Notice: i18n.T(ctx, i18n.KeyCartMergeFailed), Retry: i18n.T(ctx, i18n.KeyCartMergeRetry),
			}))
			for _, want := range []string{welcome, `action="/account/cart/retry"`, `name="next" value="/account?welcome=1"`, html.EscapeString(i18n.T(ctx, i18n.KeyCartMergeFailed))} {
				if !strings.Contains(recovery, want) {
					t.Errorf("registration cart recovery omits %q", want)
				}
			}
		})
	}
}
