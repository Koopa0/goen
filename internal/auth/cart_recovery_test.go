package auth_test

import (
	"context"
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/auth"
	"github.com/koopa0/goen/internal/cart/cartitem"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/user"
)

// reportedCarts reports fixed items, so the page is checked against exactly
// what the cart said.
type reportedCarts struct{ beforeSignIn, account []cartitem.Item }

func (reportedCarts) AdoptRequestCart(context.Context, *http.Request, string) (bool, error) {
	return false, nil
}

func (c reportedCarts) RecoveryContents(context.Context, *http.Request) (beforeSignIn, account []cartitem.Item, err error) {
	return c.beforeSignIn, c.account, nil
}

func (reportedCarts) ForgetCart(http.ResponseWriter, *http.Request) {}

// TestHandler_CartRecoveryPageLists: CartRecoveryPage renders what the cart
// reports in two sections: the before-sign-in basket with its photo, srcset,
// label and quantity, and the account basket with a placeholder, without mixing
// their items.
func TestHandler_CartRecoveryPageLists(t *testing.T) {
	t.Parallel()
	carts := reportedCarts{
		beforeSignIn: []cartitem.Item{{
			Name: "Guest tote", Label: "Red · Small", Quantity: 2,
			ImageURL: "/guest.webp", ImageSrcset: "/guest-400.webp 400w", ImageAlt: "Guest photo",
		}},
		account: []cartitem.Item{{Name: "Account mug", Quantity: 3}},
	}
	h := auth.NewHandler(&auth.Store{}, carts, nil, slog.New(slog.DiscardHandler), false, nil)
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(user.NewContext(t.Context(), user.User{ID: "11111111-1111-4111-8111-111111111111"}), locale)
			res := httptest.NewRecorder()
			h.CartRecoveryPage(res, httptest.NewRequestWithContext(ctx, http.MethodGet, "/account/cart-recovery", http.NoBody))
			if res.Code != http.StatusOK {
				t.Fatalf("recovery = %d, want 200", res.Code)
			}
			body := html.UnescapeString(res.Body.String())
			quantity := i18n.T(ctx, i18n.KeyQuantity)
			for _, tc := range []struct {
				section   string
				want, not []string
			}{
				{"cart-recovery-before", []string{
					"Guest tote", "Red · Small", quantity + " 2",
					`src="/guest.webp"`, `srcset="/guest-400.webp 400w"`, `alt="Guest photo"`,
				}, []string{"Account mug"}},
				{"cart-recovery-account", []string{"Account mug", quantity + " 3", "goen-line__ph"}, []string{"Guest tote", "<img"}},
			} {
				_, rest, found := strings.Cut(body, `aria-labelledby="`+tc.section+`"`)
				section, _, _ := strings.Cut(rest, "</section>")
				if !found {
					t.Fatalf("no %s section", tc.section)
				}
				for _, w := range tc.want {
					if !strings.Contains(section, w) {
						t.Errorf("%s lacks %q", tc.section, w)
					}
				}
				for _, n := range tc.not {
					if strings.Contains(section, n) {
						t.Errorf("%s shows %q", tc.section, n)
					}
				}
			}
		})
	}
}
