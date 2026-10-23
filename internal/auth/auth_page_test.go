package auth

import (
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestSignInEmailFieldGoogleError: the sign-in email field keeps its email rule
// attributes, and a Google sign-in failure is not attributed to it (no
// aria-invalid, no description link).
func TestSignInEmailFieldGoogleError(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	body := renderComponent(t, ctx, SignIn(SignInMeta(ctx), View{Errors: map[string]string{"oauth": "Google sign-in failed"}}))
	input := regexp.MustCompile(`<input[^>]*id="email"[^>]*>`).FindString(body)
	for _, want := range []string{`type="email"`, `data-rule="email"`, `autocomplete="email"`, `required`} {
		if !strings.Contains(input, want) {
			t.Errorf("sign-in email field omits %q: %s", want, input)
		}
	}
	if strings.Contains(input, `aria-invalid="true"`) || strings.Contains(input, `aria-describedby="signin-google-error"`) {
		t.Errorf("Google failure was attributed to the email field: %s", input)
	}
}

// TestRegisterSentPage: after registering, the page names the address and
// offers a resend instead of the registration form, and a page without the sent
// state still shows the form. An empty form with no way to ask again strands
// whoever lost the message; the registration form must not return under the
// confirmation.
func TestRegisterSentPage(t *testing.T) {
	t.Parallel()

	sent := renderToString(t, Register(frame.Page{Title: "註冊"}, View{
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
	anon := renderToString(t, Register(frame.Page{Title: "註冊"}, View{OffersResend: true, Next: "/account"}))
	if !strings.Contains(anon, `action="/register/resend"`) || !strings.Contains(anon, `type="email"`) {
		t.Error("the sent page without an address does not ask for one")
	}

	fresh := renderToString(t, Register(frame.Page{Title: "註冊"}, View{Next: "/account"}))
	if strings.Contains(fresh, `action="/register/resend"`) {
		t.Error("the registration page offers a resend before anything was sent")
	}
	if !strings.Contains(fresh, `action="/register"`) {
		t.Error("the registration page lost its form")
	}
}

// TestRegisterResendCorrection: the resend page offers the address-correction
// link only once a confirmation has been sent, in both locales.
func TestRegisterResendCorrection(t *testing.T) {
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
					page := renderComponent(t, ctx, Register(frame.Page{Title: "Registration"}, View{
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

// TestRegisterCompleteWhy: the page opened from the mailed registration link
// says why it asks for a password. The password after the mailed link is
// deliberate, and a person who just clicked the link reads the request as a bug
// unless the page says so.
func TestRegisterCompleteWhy(t *testing.T) {
	t.Parallel()

	page := renderToString(t, RegisterComplete(frame.Page{Title: "完成註冊"}, RegisterCompleteView{Token: "t"}))
	if want := i18n.T(i18n.WithLocale(t.Context(), i18n.ZhHant), i18n.KeyRegisterCompleteWhy); !strings.Contains(page, want) {
		t.Errorf("the page does not say why it asks for the password: want %q", want)
	}
}

func TestRegisterSentHeading(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			sent := renderComponent(t, ctx, Register(RegisterMeta(ctx), View{OffersResend: true, Email: "ada@example.com", Notice: "ada@example.com"}))
			heading := "Check your inbox"
			if locale == i18n.ZhHant {
				heading = "\u8acb\u5230\u4fe1\u7bb1\u6536\u4fe1"
			}
			if !strings.Contains(sent, `<h1 class="goen-auth__title">`+heading+`</h1>`) || !strings.Contains(sent, `href="/register"`) || !strings.Contains(sent, "ada@example.com") {
				t.Error("Register() sent state lost the inbox heading, destination or re-entry")
			}
			fresh := renderComponent(t, ctx, Register(RegisterMeta(ctx), View{}))
			if strings.Contains(fresh, `<h1 class="goen-auth__title">`+heading+`</h1>`) {
				t.Error("Register() asks a new visitor to check their inbox")
			}
		})
	}
}
