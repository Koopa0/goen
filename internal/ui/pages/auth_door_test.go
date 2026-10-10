package pages

import (
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/ui/layouts"
)

var authPhoto = regexp.MustCompile(`<img[^>]*department-books-stationery[^>]*alt=""`)

// TestSignInAndRegisterOpenWithAPhotoAfterTheForm: the photo is decoration, so
// it follows the form in the document, and the pages that carry no
// photo (a re-sign-in, forgot) stay a single column.
func TestSignInAndRegisterOpenWithAPhotoAfterTheForm(t *testing.T) {
	t.Parallel()
	page := layouts.Page{Title: "x"}
	tests := []struct {
		name  string
		body  string
		photo bool
	}{
		{"sign in", renderToString(t, SignIn(page, AuthView{GoogleSignIn: true})), true},
		{"register", renderToString(t, Register(page, AuthView{})), true},
		{"register sent", renderToString(t, Register(page, AuthView{OffersResend: true, Email: "a@example.com", Notice: "sent"})), true},
		{"re-sign-in", renderToString(t, SignIn(page, AuthView{HideRegister: true})), false},
		{"forgot", renderToString(t, Forgot(page, ForgotView{})), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := strings.Contains(tt.body, "goen-auth--door"); got != tt.photo {
				t.Errorf("goen-auth--door = %t, want %t", got, tt.photo)
			}
			loc := authPhoto.FindStringIndex(tt.body)
			if (loc != nil) != tt.photo {
				t.Fatalf("photo present = %t, want %t", loc != nil, tt.photo)
			}
			if tt.photo && loc[0] < strings.LastIndex(tt.body, `class="goen-auth__form"`) {
				t.Error("the photo comes before the form")
			}
			if strings.Contains(tt.body, "goen-auth__divider") {
				t.Error("the Google divider rule is back")
			}
		})
	}
}

func TestTheForgotLinkFollowsThePasswordField(t *testing.T) {
	t.Parallel()
	body := renderToString(t, SignIn(layouts.Page{Title: "x"}, AuthView{}))
	field := strings.Index(body, `id="password"`)
	link := strings.Index(body, `href="/forgot"`)
	if field < 0 || link < field {
		t.Errorf("forgot link at %d, password field at %d; the link must come after the field", link, field)
	}
}

func TestTheDemoAccountTakesTheFocusOffTheEmailField(t *testing.T) {
	t.Parallel()
	page := layouts.Page{Title: "x"}
	focus := func(v AuthView) bool {
		body := renderToString(t, SignIn(page, v))
		id := strings.Index(body, `id="email"`)
		if id < 0 {
			t.Fatal(`no id="email" in the sign-in page`)
		}
		start := strings.LastIndex(body[:id], "<input")
		if start < 0 {
			t.Fatal(`id="email" is not on an <input>`)
		}
		end := id + strings.Index(body[id:], ">")
		return strings.Contains(body[start:end], "autofocus")
	}
	if !focus(AuthView{}) {
		t.Error("the email field lost its focus with no demo account")
	}
	if focus(AuthView{DemoEmail: "demo@example.com", DemoPassword: "x"}) {
		t.Error("the email field takes the focus above a demo account")
	}
}

func TestTheReturnSentenceIsALede(t *testing.T) {
	t.Parallel()
	body := renderToString(t, SignIn(layouts.Page{Title: "x"}, AuthView{ReturnMessage: "Sign in to go back to your cart."}))
	if !strings.Contains(body, `<p class="goen-auth__lede">Sign in to go back to your cart.</p>`) {
		t.Error("the return sentence is not in an element with class goen-auth__lede")
	}
}
