package account

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/user"
)

const (
	demoAddress  = "demo@goen.example"
	demoPassword = "a password every visitor reads"
)

func TestADemoAccountIsBothSettingsOrNeither(t *testing.T) {
	none, err := NewDemoAccount("", "")
	if err != nil || none.Enabled() {
		t.Fatalf("no settings = (%+v, %v), want no demo account and no error", none, err)
	}
	for name, tt := range map[string]struct{ addr, password string }{
		"an address alone":    {addr: demoAddress},
		"a password alone":    {password: demoPassword},
		"an unusable address": {addr: "not an address", password: demoPassword},
		"a short password":    {addr: demoAddress, password: "too short"},
	} {
		t.Run(name, func(t *testing.T) {
			_, refusal := NewDemoAccount(tt.addr, tt.password)
			if refusal == nil {
				t.Fatal("accepted")
			}
			if half := tt.addr == "" || tt.password == ""; half && !strings.Contains(refusal.Error(), "both") {
				t.Errorf("half a configuration is refused as %q, which does not say both are needed", refusal)
			}
			if tt.password != "" && strings.Contains(refusal.Error(), tt.password) {
				t.Errorf("the refusal carries the password: %v", refusal)
			}
		})
	}
	d, err := NewDemoAccount(" Demo@Goen.example ", demoPassword)
	if err != nil {
		t.Fatalf("NewDemoAccount: %v", err)
	}
	if !d.Enabled() || !d.holds("DEMO@goen.example") || d.holds("other@goen.example") {
		t.Errorf("%+v does not hold its own address in any case, or holds another", d)
	}
}

func demoHandler(t *testing.T) *Handler {
	t.Helper()
	d, err := NewDemoAccount(demoAddress, demoPassword)
	if err != nil {
		t.Fatalf("NewDemoAccount: %v", err)
	}
	h := rateLimitedAccountHandler(deadAccountStore(t))
	h.mailLimit = accountTestLimiter()
	h.OfferDemoAccount(d)
	return h
}

func TestTheSignInPageShowsTheDemoAccountOnlyWhenOffered(t *testing.T) {
	signInPage := func(h *Handler) string {
		t.Helper()
		res := httptest.NewRecorder()
		h.SignInPage(res, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/signin", http.NoBody))
		return res.Body.String()
	}
	shared := i18n.T(t.Context(), i18n.KeyDemoAccountShared)

	offered := demoHandler(t)
	refused := httptest.NewRecorder()
	offered.signInFailed(refused, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/signin", http.NoBody),
		"someone@goen.example", "/account")
	for page, body := range map[string]string{"the sign-in page": signInPage(offered), "a refused sign-in": refused.Body.String()} {
		for _, want := range []string{demoAddress, demoPassword, shared} {
			if !strings.Contains(body, want) {
				t.Errorf("%s with a demo account lacks %q", page, want)
			}
		}
		// goen.js reveals the button; without it the button would sign in nobody.
		if button := regexp.MustCompile(`<button[^>]*data-demo-fill[^>]*>`).FindString(body); !strings.Contains(button, " hidden") {
			t.Errorf("%s carries the demo button %q, want it hidden until script reveals it", page, button)
		}
	}

	plain := signInPage(rateLimitedAccountHandler(deadAccountStore(t)))
	for _, absent := range []string{shared, `data-demo-fill`, `goen-auth__demo`} {
		if strings.Contains(plain, absent) {
			t.Errorf("the sign-in page without a demo account carries %q", absent)
		}
	}
}

// TestTheDemoAccountIsRefusedEveryChangeThatWouldShutOutTheNextVisitor: every
// visitor holds this account's password, so none may change how the next one
// signs in, end the account, or send mail on its behalf. Each refusal comes
// before the store is asked anything, which the dead store here would fail.
func TestTheDemoAccountIsRefusedEveryChangeThatWouldShutOutTheNextVisitor(t *testing.T) {
	h := demoHandler(t)
	form := url.Values{
		"current": {demoPassword}, "password": {"a new long password"}, "email": {"elsewhere@goen.example"},
	}
	for name, serve := range map[string]http.HandlerFunc{
		"change the address":     h.ChangeEmail,
		"resend an address link": h.ResendVerification,
		"change the password":    h.ChangePassword,
		"delete the account":     h.Erase,
	} {
		for who, addr := range map[string]string{"the demo account": "Demo@Goen.example", "another account": "own@goen.example"} {
			t.Run(name+" as "+who, func(t *testing.T) {
				r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/account",
					strings.NewReader(form.Encode()))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				r = r.WithContext(user.NewContext(r.Context(), user.User{ID: "00000000-0000-0000-0000-000000000001", Email: addr, Role: user.RoleCustomer}))
				res := httptest.NewRecorder()
				serve(res, r)
				refused := res.Code == http.StatusSeeOther && res.Header().Get("Location") == "/account?demo=fixed"
				if wantRefused := who == "the demo account"; refused != wantRefused {
					t.Errorf("answered %d to %q; refused as the demo account = %v, want %v",
						res.Code, res.Header().Get("Location"), refused, wantRefused)
				}
			})
		}
	}

	forgot := postAccountForm(t, "/forgot", url.Values{"email": {" DEMO@goen.example"}}, h.Forgot)
	if loc := forgot.Header().Get("Location"); forgot.Code != http.StatusSeeOther || loc != "/forgot?demo=fixed" {
		t.Errorf("a reset link for the demo address answered %d to %q, want 303 to /forgot?demo=fixed", forgot.Code, loc)
	}
	page := httptest.NewRecorder()
	h.ForgotPage(page, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/forgot?demo=fixed", http.NoBody))
	if want := i18n.T(t.Context(), i18n.KeyDemoPasswordNoReset); !strings.Contains(page.Body.String(), want) {
		t.Errorf("the forgot page after a refusal does not say %q", want)
	}
}

// TestGoogleIsNeverLinkedToTheDemoAccount: a Google sign-in at an account's
// address links Google to that account, and whoever holds a Google account at
// the demo address would then sign in with Google alone.
func TestGoogleIsNeverLinkedToTheDemoAccount(t *testing.T) {
	g, err := NewGoogle("client-id", "client-secret", "https://goen.example")
	if err != nil {
		t.Fatalf("NewGoogle: %v", err)
	}
	g.http = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		body := `{"access_token":"a-token"}`
		if r.Method == http.MethodGet {
			body = `{"sub":"google-subject","email":"Demo@goen.example","email_verified":true}`
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(body)), Request: r,
		}, nil
	})}
	h := demoHandler(t)
	h.google = g

	start := httptest.NewRecorder()
	h.GoogleSignIn(start, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/google", http.NoBody))
	target, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse the authorisation redirect: %v", err)
	}
	callback := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/auth/google/callback?code=the-code&state="+url.QueryEscape(target.Query().Get("state")), http.NoBody)
	for _, c := range start.Result().Cookies() {
		callback.AddCookie(c)
	}
	res := httptest.NewRecorder()
	h.GoogleCallback(res, callback)
	if loc := res.Header().Get("Location"); loc != "/signin?oauth=demo" {
		t.Fatalf("a Google sign-in at the demo address lands at %q, want /signin?oauth=demo", loc)
	}
	if got, want := oauthOutcome(t.Context(), "demo")["oauth"], i18n.T(t.Context(), i18n.KeyDemoSignInPassword); got != want {
		t.Errorf("the sign-in page says %q, want %q", got, want)
	}
}

// TestNoVisitorCanThrottleTheDemoAccountForEveryOther: its password is public,
// so a bound per account protects nothing and a stream of wrong attempts would
// turn every other visitor away. Any other address keeps its bound.
func TestNoVisitorCanThrottleTheDemoAccountForEveryOther(t *testing.T) {
	h := demoHandler(t)
	h.log = slog.New(slog.DiscardHandler)
	signIn := func(addr string) int {
		return postAccountForm(t, "/signin", url.Values{"email": {addr}, "password": {"wrong"}}, h.SignIn).Code
	}
	for i := range 3 {
		if code := signIn("Demo@goen.example"); code == http.StatusTooManyRequests {
			t.Fatalf("sign-in %d at the demo address was throttled", i+1)
		}
	}
	signIn("own@goen.example")
	if code := signIn("own@goen.example"); code != http.StatusTooManyRequests {
		t.Errorf("a second sign-in at another address answered %d, want 429: its bound is gone", code)
	}
}
