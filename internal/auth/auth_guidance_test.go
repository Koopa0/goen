package auth_test

import (
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	xhtml "golang.org/x/net/html"

	"github.com/koopa0/goen/internal/auth"
	"github.com/koopa0/goen/internal/i18n"
)

// authGuidanceRun runs fn once per locale, in parallel, with a handler that
// never reaches its store: these pages render, or refuse a form, before any read.
func authGuidanceRun(t *testing.T, fn func(c *authGuidanceCase)) {
	t.Helper()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			fn(&authGuidanceCase{
				t:      t,
				locale: locale,
				h:      auth.NewHandler(&auth.Store{}, nil, nil, slog.New(slog.DiscardHandler), false, nil),
			})
		})
	}
}

// authGuidanceCase is one locale's subtest, handler and requests.
type authGuidanceCase struct {
	t      *testing.T
	locale i18n.Locale
	h      *auth.Handler
}

func (c *authGuidanceCase) text(key i18n.Key) string {
	return i18n.T(i18n.WithLocale(c.t.Context(), c.locale), key)
}

func (c *authGuidanceCase) get(serve http.HandlerFunc, target string) *httptest.ResponseRecorder {
	ctx := i18n.WithLocale(c.t.Context(), c.locale)
	res := httptest.NewRecorder()
	serve(res, httptest.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody))
	return res
}

// mismatch posts two different passwords to serve and wants the 422 refusal.
func (c *authGuidanceCase) mismatch(serve http.HandlerFunc, target string) *xhtml.Node {
	ctx := i18n.WithLocale(c.t.Context(), c.locale)
	form := url.Values{"email": {"ada@example.com"}, "password": {"long-password"}, "confirm": {"other-password"}, "token": {"unchecked"}}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()
	serve(res, req)
	if res.Code != http.StatusUnprocessableEntity {
		c.t.Fatalf("POST %s with mismatched passwords = %d, want 422", target, res.Code)
	}
	return authGuidanceDOM(c.t, res.Body.String())
}

func TestHandler_RegisterPage(t *testing.T) {
	t.Parallel()
	authGuidanceRun(t, func(c *authGuidanceCase) {
		res := c.get(c.h.RegisterPage, "/register")
		root := authGuidanceDOM(c.t, res.Body.String())
		for _, destination := range []string{"/terms", "/privacy"} {
			if !authGuidanceInForm(root, "a", "href", destination) {
				c.t.Errorf("register form lacks policy link %s", destination)
			}
		}
		if want := html.EscapeString(c.text(i18n.KeyRegisterEmailFollows)); !strings.Contains(res.Body.String(), want) {
			c.t.Errorf("register form omits the confirmation-email notice %q", want)
		}
		authGuidanceWantHint(c.t, root)
	})
}

func TestHandler_ResetPage(t *testing.T) {
	t.Parallel()
	authGuidanceRun(t, func(c *authGuidanceCase) {
		res := c.get(c.h.ResetPage, "/reset?token=unchecked")
		if want := html.EscapeString(c.text(i18n.KeyPasswordHint)); !strings.Contains(res.Body.String(), want) {
			c.t.Errorf("reset form omits the password rule %q", want)
		}
		root := authGuidanceDOM(c.t, res.Body.String())
		authGuidanceWantHint(c.t, root)
		authGuidanceWantFocus(c.t, root, "password")
	})
}

func TestHandler_ResetPageNoToken(t *testing.T) {
	t.Parallel()
	authGuidanceRun(t, func(c *authGuidanceCase) {
		root := authGuidanceDOM(c.t, c.get(c.h.ResetPage, "/reset").Body.String())
		want := c.text(i18n.KeyBackToShop)
		for n := range root.Descendants() {
			if n.Data == "a" && authGuidanceAttr(n, "href") == "/" && authGuidanceAttr(n, "class") == "notice__back" {
				if got := strings.TrimSpace(n.FirstChild.Data); got != want {
					c.t.Errorf("back link text = %q, want %q", got, want)
				}
				return
			}
		}
		c.t.Error("email-link page lacks the plain back-to-shop link")
	})
}

func TestHandler_CompleteRegistrationPage(t *testing.T) {
	t.Parallel()
	authGuidanceRun(t, func(c *authGuidanceCase) {
		body := c.get(c.h.CompleteRegistrationPage, "/register/complete?token=unchecked").Body.String()
		for _, key := range []i18n.Key{i18n.KeyRegisterCompleteWhy, i18n.KeyRegisterCompleteNotYou, i18n.KeyResetPasswordLink} {
			if want := html.EscapeString(c.text(key)); !strings.Contains(body, want) {
				c.t.Errorf("register-complete page omits %q", want)
			}
		}
	})
}

func TestHandler_RegisterMismatch(t *testing.T) {
	t.Parallel()
	authGuidanceRun(t, func(c *authGuidanceCase) {
		authGuidanceWantConfirmRefusal(c.t, c.mismatch(c.h.Register, "/register"))
	})
}

func TestHandler_ResetMismatch(t *testing.T) {
	t.Parallel()
	authGuidanceRun(t, func(c *authGuidanceCase) {
		root := c.mismatch(c.h.Reset, "/reset")
		authGuidanceWantFocus(c.t, root, "confirm")
		authGuidanceWantConfirmRefusal(c.t, root)
	})
}

// authGuidanceWantConfirmRefusal wants the mismatch refusal attached to the
// confirm field alone: the first password is not marked invalid.
func authGuidanceWantConfirmRefusal(t *testing.T, root *xhtml.Node) {
	t.Helper()
	var confirm, refusal *xhtml.Node
	for n := range root.Descendants() {
		if n.Data == "input" && authGuidanceAttr(n, "name") == "password" && authGuidanceAttr(n, "aria-invalid") == "true" {
			t.Error("mismatch marks the first password invalid")
		}
		if n.Data == "input" && authGuidanceAttr(n, "name") == "confirm" {
			confirm = n
		}
		if id := authGuidanceAttr(n, "id"); id == "confirm-error" || id == "reset-error" {
			refusal = n
		}
	}
	if confirm == nil || refusal == nil || confirm.Parent != refusal.Parent ||
		authGuidanceAttr(confirm, "aria-invalid") != "true" ||
		!strings.Contains(authGuidanceAttr(confirm, "aria-describedby"), authGuidanceAttr(refusal, "id")) {
		t.Error("mismatch is not associated with the confirm field alone")
	}
}

// authGuidanceInForm reports whether a tag with the attribute value sits inside a form.
func authGuidanceInForm(root *xhtml.Node, tag, attr, value string) bool {
	for n := range root.Descendants() {
		if n.Data != tag || authGuidanceAttr(n, attr) != value {
			continue
		}
		for p := n.Parent; p != nil; p = p.Parent {
			if p.Data == "form" {
				return true
			}
		}
	}
	return false
}

// authGuidanceWantHint wants the password input to point at a hint element that exists.
func authGuidanceWantHint(t *testing.T, root *xhtml.Node) {
	t.Helper()
	var describedby string
	ids := map[string]bool{}
	for n := range root.Descendants() {
		if n.Data == "input" && authGuidanceAttr(n, "name") == "password" {
			describedby = authGuidanceAttr(n, "aria-describedby")
		}
		if id := authGuidanceAttr(n, "id"); id != "" {
			ids[id] = true
		}
	}
	if describedby != "password-hint" || !ids["password-hint"] {
		t.Errorf("password aria-describedby = %q, hint element present = %v, want password-hint present", describedby, ids["password-hint"])
	}
}

// authGuidanceWantFocus wants autofocus on the named input and on no other.
func authGuidanceWantFocus(t *testing.T, root *xhtml.Node, want string) {
	t.Helper()
	var focused []string
	for n := range root.Descendants() {
		if n.Data == "input" {
			for _, a := range n.Attr {
				if a.Key == "autofocus" {
					focused = append(focused, authGuidanceAttr(n, "name"))
				}
			}
		}
	}
	if len(focused) != 1 || focused[0] != want {
		t.Errorf("autofocus on %v, want only %s", focused, want)
	}
}

func authGuidanceDOM(t *testing.T, body string) *xhtml.Node {
	t.Helper()
	root, err := xhtml.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func authGuidanceAttr(n *xhtml.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
