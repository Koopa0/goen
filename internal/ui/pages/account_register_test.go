package pages

import (
	"html"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestRegistrationWelcomeKeepsTheOriginalActionAndCartRecovery(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			welcome := "Your account is created. Welcome."
			if locale == i18n.ZhHant {
				welcome = "\u5e33\u865f\u5df2\u5efa\u7acb\uff0c\u6b61\u8fce\u52a0\u5165\u3002"
			}
			body := renderComponent(t, ctx, Account(AccountMeta(ctx), &AccountView{
				Notice: i18n.T(ctx, i18n.KeyAccountWelcome), ReturnAfterWelcome: "/cart?qty=adjusted&next=%2Fcheckout", CartAdjusted: true,
			}))
			for _, want := range []string{welcome, html.EscapeString(i18n.T(ctx, i18n.KeyCartQuantityAdjusted)), `href="/cart?qty=adjusted&amp;next=%2Fcheckout"`} {
				if !strings.Contains(body, want) {
					t.Errorf("registration welcome omits %q", want)
				}
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

// TestTheSentPageNamesTheAddressAndOffersAResendInsteadOfTheForm: after
// registering, an empty form with no way to ask again strands whoever lost the
// message; the registration form must not return under the confirmation.
func TestTheSentPageNamesTheAddressAndOffersAResendInsteadOfTheForm(t *testing.T) {
	t.Parallel()

	sent := renderToString(t, Register(layouts.Page{Title: "註冊"}, AuthView{
		OffersResend: true, Email: "ada@example.com", Next: "/account",
		Notice: "確認信已寄到 ada@example.com",
	}))
	for _, want := range []string{
		`action="/register/resend"`,
		`name="email" value="ada@example.com"`,
		"ada@example.com",
	} {
		if !strings.Contains(sent, want) {
			t.Errorf("the sent page lacks %q", want)
		}
	}
	if strings.Contains(sent, `action="/register"`) {
		t.Error("the sent page still carries the registration form")
	}

	// Without the cookie the page asks for the address instead of naming it.
	anon := renderToString(t, Register(layouts.Page{Title: "註冊"}, AuthView{OffersResend: true, Next: "/account"}))
	if !strings.Contains(anon, `action="/register/resend"`) || !strings.Contains(anon, `type="email"`) {
		t.Error("the sent page without an address does not ask for one")
	}

	fresh := renderToString(t, Register(layouts.Page{Title: "註冊"}, AuthView{Next: "/account"}))
	if strings.Contains(fresh, `action="/register/resend"`) {
		t.Error("the registration page offers a resend before anything was sent")
	}
	if !strings.Contains(fresh, `action="/register"`) {
		t.Error("the registration page lost its form")
	}
}

func TestRegistrationResendOnlyOffersAddressCorrectionAfterSending(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(string(locale), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			correction := "信箱打錯了？換一個重新註冊"
			if locale == i18n.En {
				correction = "Wrong address? Register again"
			}
			for _, tt := range []struct {
				name   string
				notice string
				sent   bool
			}{
				{name: "resend entry"},
				{name: "sent", notice: i18n.T(ctx, i18n.KeyRegisterSent), sent: true},
			} {
				t.Run(tt.name, func(t *testing.T) {
					page := renderComponent(t, ctx, Register(layouts.Page{Title: "Registration"}, AuthView{
						OffersResend: true, Next: "/account", Notice: tt.notice,
					}))
					if !strings.Contains(page, `action="/register/resend"`) || !strings.Contains(page, `type="email"`) {
						t.Error("the resend page without a cookie does not offer its email form")
					}
					if got := strings.Contains(page, `id="register-sent"`); got != tt.sent {
						t.Errorf("the sent notice is present = %v, want %v", got, tt.sent)
					}
					if got := strings.Contains(page, correction); got != tt.sent {
						t.Errorf("address correction %q is present = %v, want %v", correction, got, tt.sent)
					}
				})
			}
		})
	}
}

// TestTheRegistrationLinkPageSaysWhyItAsksForAPassword: the password after the
// mailed link is deliberate, and a person who just clicked the link reads the
// request as a bug unless the page says so.
func TestTheRegistrationLinkPageSaysWhyItAsksForAPassword(t *testing.T) {
	t.Parallel()

	page := renderToString(t, RegisterComplete(layouts.Page{Title: "完成註冊"}, RegisterCompleteView{Token: "t"}))
	if want := i18n.T(i18n.WithLocale(t.Context(), i18n.ZhHant), i18n.KeyRegisterCompleteWhy); !strings.Contains(page, want) {
		t.Errorf("the page does not say why it asks for the password: want %q", want)
	}
}
