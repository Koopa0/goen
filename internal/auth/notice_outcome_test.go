package auth

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/user"
)

// TestHandler_CartRecoveryPageNotice: the sentence that says the carts were not
// combined is drawn as a failure, in the danger style and announced as an
// alert, and not as information.
func TestHandler_CartRecoveryPageNotice(t *testing.T) {
	t.Parallel()
	h := NewHandler(&Store{}, nil, nil, slog.New(slog.DiscardHandler), false, nil)
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(user.NewContext(t.Context(), user.User{ID: "11111111-1111-4111-8111-111111111111"}), locale)
			res := httptest.NewRecorder()
			h.CartRecoveryPage(res, httptest.NewRequestWithContext(ctx, http.MethodGet, "/account/cart-recovery", http.NoBody))
			if res.Code != http.StatusOK {
				t.Fatalf("CartRecoveryPage = %d, want 200", res.Code)
			}
			doc, err := html.Parse(strings.NewReader(res.Body.String()))
			if err != nil {
				t.Fatal(err)
			}
			n := findDescendant(doc, func(n *html.Node) bool {
				return n.Type == html.ElementNode && attrValue(n, "id") == "cart-recovery"
			})
			if n == nil {
				t.Fatal("CartRecoveryPage renders no cart-recovery notice")
			}
			if want := i18n.T(ctx, i18n.KeyCartMergeFailed); nodeText(n) != want {
				t.Errorf("cart-recovery notice says %q, want %q", nodeText(n), want)
			}
			assertDangerNotice(t, n)
		})
	}
}

// TestHandler_ForgotPageDemoNotice: the refusal to reset the demo account's
// password is drawn like the account page's refusal for the same account, in
// the danger style and announced as an alert, and not as a warning.
func TestHandler_ForgotPageDemoNotice(t *testing.T) {
	t.Parallel()
	h := NewHandler(&Store{}, nil, nil, slog.New(slog.DiscardHandler), false, nil)
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			res := httptest.NewRecorder()
			h.ForgotPage(res, httptest.NewRequestWithContext(ctx, http.MethodGet, "/forgot?demo=fixed", http.NoBody))
			if res.Code != http.StatusOK {
				t.Fatalf("ForgotPage = %d, want 200", res.Code)
			}
			doc, err := html.Parse(strings.NewReader(res.Body.String()))
			if err != nil {
				t.Fatal(err)
			}
			want := i18n.T(ctx, i18n.KeyDemoPasswordNoReset)
			n := findDescendant(doc, func(n *html.Node) bool {
				return n.Type == html.ElementNode && hasClass(n, "goen-notice") && nodeText(n) == want
			})
			if n == nil {
				t.Fatalf("ForgotPage?demo=fixed renders no notice saying %q", want)
			}
			assertDangerNotice(t, n)
		})
	}
}

func assertDangerNotice(t *testing.T, n *html.Node) {
	t.Helper()
	if !hasClass(n, "goen-notice--danger") || attrValue(n, "role") != "alert" {
		t.Errorf("notice has class %q and role %q, want goen-notice--danger and alert", className(n), attrValue(n, "role"))
	}
	for _, class := range []string{"goen-notice--accent", "goen-notice--warn", "ui-alert--info"} {
		if hasClass(n, class) {
			t.Errorf("refusal notice also carries %s: %q", class, className(n))
		}
	}
}
