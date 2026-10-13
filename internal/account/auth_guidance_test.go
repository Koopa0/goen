package account

import (
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	xhtml "golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

func TestRegistrationAndResetGiveGuidanceBesideTheForm(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			h := NewHandler(&Store{}, nil, slog.New(slog.DiscardHandler), false, nil)
			res := httptest.NewRecorder()
			h.RegisterPage(res, httptest.NewRequestWithContext(ctx, http.MethodGet, "/register", http.NoBody))
			root := authGuidanceDOM(t, res.Body.String())
			for _, destination := range []string{"/terms", "/privacy"} {
				found := false
				for n := range root.Descendants() {
					if n.Data == "a" && authGuidanceAttr(n, "href") == destination {
						for p := n.Parent; p != nil; p = p.Parent {
							found = found || p.Data == "form"
						}
					}
				}
				if !found {
					t.Errorf("register form lacks policy link %s", destination)
				}
			}
			if !strings.Contains(res.Body.String(), i18n.T(ctx, i18n.KeyRegisterEmailFollows)) {
				t.Error("register form omits the confirmation-email notice")
			}
			authGuidanceWantHint(t, "register", root)
			res = httptest.NewRecorder()
			h.ResetPage(res, httptest.NewRequestWithContext(ctx, http.MethodGet, "/reset?token=unchecked", http.NoBody))
			if !strings.Contains(res.Body.String(), i18n.T(ctx, i18n.KeyPasswordHint)) {
				t.Error("reset form omits the password rule")
			}
			authGuidanceWantHint(t, "reset", authGuidanceDOM(t, res.Body.String()))
			authGuidanceWantFocus(t, "reset", authGuidanceDOM(t, res.Body.String()), "password")

			res = httptest.NewRecorder()
			h.CompleteRegistrationPage(res, httptest.NewRequestWithContext(ctx, http.MethodGet, "/register/complete?token=unchecked", http.NoBody))
			body := res.Body.String()
			if !strings.Contains(body, html.EscapeString(i18n.T(ctx, i18n.KeyRegisterCompleteWhy))) ||
				!strings.Contains(body, html.EscapeString(i18n.T(ctx, i18n.KeyRegisterCompleteNotYou))) ||
				!strings.Contains(body, html.EscapeString(i18n.T(ctx, i18n.KeyResetPasswordLink))) {
				t.Error("register-complete page omits its reason or reset link copy")
			}

			res = httptest.NewRecorder()
			h.ResetPage(res, httptest.NewRequestWithContext(ctx, http.MethodGet, "/reset", http.NoBody))
			back := false
			for n := range authGuidanceDOM(t, res.Body.String()).Descendants() {
				if n.Data == "a" && authGuidanceAttr(n, "href") == "/" && authGuidanceAttr(n, "class") == "notice__back" {
					back = strings.TrimSpace(n.FirstChild.Data) == i18n.T(ctx, i18n.KeyBackToShop)
				}
			}
			if !back {
				t.Error("email-link page lacks the plain back-to-shop link")
			}
			for _, path := range []string{"/register", "/reset"} {
				form := url.Values{"email": {"ada@example.com"}, "password": {"long-password"}, "confirm": {"other-password"}, "token": {"unchecked"}}
				req := httptest.NewRequestWithContext(ctx, http.MethodPost, path, strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				res = httptest.NewRecorder()
				if path == "/register" {
					h.Register(res, req)
				} else {
					h.Reset(res, req)
				}
				if res.Code != http.StatusUnprocessableEntity {
					t.Fatalf("%s mismatch = %d, want 422", path, res.Code)
				}
				root = authGuidanceDOM(t, res.Body.String())
				if path == "/reset" {
					authGuidanceWantFocus(t, "reset mismatch", root, "confirm")
				}
				var confirm, refusal *xhtml.Node
				for n := range root.Descendants() {
					if n.Data == "input" && authGuidanceAttr(n, "name") == "password" && authGuidanceAttr(n, "aria-invalid") == "true" {
						t.Errorf("%s mismatch marks the first password", path)
					}
					if n.Data == "input" && authGuidanceAttr(n, "name") == "confirm" {
						confirm = n
					}
					if authGuidanceAttr(n, "id") == "confirm-error" || authGuidanceAttr(n, "id") == "reset-error" {
						refusal = n
					}
				}
				if confirm == nil || refusal == nil || confirm.Parent != refusal.Parent || authGuidanceAttr(confirm, "aria-invalid") != "true" || !strings.Contains(authGuidanceAttr(confirm, "aria-describedby"), authGuidanceAttr(refusal, "id")) {
					t.Errorf("%s mismatch is not associated with the confirm field alone", path)
				}
			}
		})
	}
}

// authGuidanceWantHint wants the password input to point at a hint element that exists.
func authGuidanceWantHint(t *testing.T, page string, root *xhtml.Node) {
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
		t.Errorf("%s password aria-describedby = %q, hint element present = %v", page, describedby, ids["password-hint"])
	}
}

// authGuidanceWantFocus wants autofocus on the named input and on no other.
func authGuidanceWantFocus(t *testing.T, page string, root *xhtml.Node, want string) {
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
		t.Errorf("%s autofocus on %v, want only %s", page, focused, want)
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
