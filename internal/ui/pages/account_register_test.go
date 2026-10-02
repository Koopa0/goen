package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// TestTheSentPageNamesTheAddressAndOffersAResendInsteadOfTheForm: after
// registering, an empty form with no way to ask again strands whoever lost the
// message; the registration form must not return under the confirmation.
func TestTheSentPageNamesTheAddressAndOffersAResendInsteadOfTheForm(t *testing.T) {
	t.Parallel()

	sent := renderToString(t, Register(layouts.Page{Title: "註冊"}, AuthView{
		Sent: true, Email: "ada@example.com", Next: "/account",
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
	anon := renderToString(t, Register(layouts.Page{Title: "註冊"}, AuthView{Sent: true, Next: "/account"}))
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
