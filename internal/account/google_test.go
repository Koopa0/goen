package account

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestTheAuthorizationURLCarriesEverySecurityParameter reads the URL a customer
// is actually sent to.
//
// Each of these is a distinct attack it closes, and each is invisible once the
// browser has left: a missing state is login CSRF, a missing PKCE challenge
// makes a leaked code redeemable, and a wrong redirect_uri is a code delivered
// somewhere else.
func TestTheAuthorizationURLCarriesEverySecurityParameter(t *testing.T) {
	g, err := NewGoogle("client-id", "client-secret", "https://goen.example")
	if err != nil {
		t.Fatalf("NewGoogle: %v", err)
	}

	target, state, err := g.AuthorizeURL("/account/orders")
	if err != nil {
		t.Fatalf("AuthorizeURL: %v", err)
	}
	u, err := url.Parse(target)
	if err != nil {
		t.Fatalf("the authorisation URL does not parse: %v", err)
	}
	if u.Scheme != "https" || u.Host != "accounts.google.com" {
		t.Errorf("the browser is sent to %s://%s, want https://accounts.google.com",
			u.Scheme, u.Host)
	}
	q := u.Query()

	if q.Get("state") != state.Value || state.Value == "" {
		t.Errorf("state in the URL is %q and the browser is told to remember %q",
			q.Get("state"), state.Value)
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method = %q, want S256 — 'plain' is no protection at all",
			q.Get("code_challenge_method"))
	}
	// The challenge must be the SHA-256 of the verifier the browser keeps, or
	// PKCE is decoration: Google compares them and the exchange would fail.
	sum := sha256.Sum256([]byte(state.Verifier))
	if want := base64.RawURLEncoding.EncodeToString(sum[:]); q.Get("code_challenge") != want {
		t.Errorf("code_challenge is %q, want the SHA-256 of the verifier", q.Get("code_challenge"))
	}
	if q.Get("redirect_uri") != "https://goen.example/auth/google/callback" {
		t.Errorf("redirect_uri = %q, want the registered callback", q.Get("redirect_uri"))
	}
	if q.Get("response_type") != "code" {
		t.Errorf("response_type = %q, want code — the implicit flow puts a token in "+
			"the address bar", q.Get("response_type"))
	}

	// `next` travels in the STATE, never in the URL. In the URL it would be an
	// open redirect Google itself would echo back.
	if strings.Contains(target, "/account/orders") {
		t.Error("the authorisation URL carries the return path; it belongs in the state cookie")
	}
	if state.Next != "/account/orders" {
		t.Errorf("the state carries %q, want the path the visitor was headed for", state.Next)
	}
}

// TestTwoSignInsDoNotShareAState proves the state and verifier are per attempt.
//
// A fixed state is no CSRF protection at all — an attacker who learns it once
// can forge every callback after.
func TestTwoSignInsDoNotShareAState(t *testing.T) {
	g, _ := NewGoogle("client-id", "client-secret", "https://goen.example")
	_, first, err := g.AuthorizeURL("/")
	if err != nil {
		t.Fatalf("AuthorizeURL: %v", err)
	}
	_, second, err := g.AuthorizeURL("/")
	if err != nil {
		t.Fatalf("AuthorizeURL: %v", err)
	}
	if first.Value == second.Value {
		t.Error("two sign-ins share a state")
	}
	if first.Verifier == second.Verifier {
		t.Error("two sign-ins share a PKCE verifier")
	}
}

// TestAnUnconfiguredClientOffersNothing holds the off-switch: password
// authentication is complete on its own, so an absent Google is an ordinary
// deployment rather than a broken one.
func TestAnUnconfiguredClientOffersNothing(t *testing.T) {
	g, err := NewGoogle("", "", "https://goen.example")
	if err != nil {
		t.Fatalf("an empty configuration must be legal: %v", err)
	}
	if g.Enabled() {
		t.Error("a client with no credentials reports itself enabled")
	}
	if _, _, err := g.AuthorizeURL("/"); err == nil {
		t.Error("an unconfigured client built an authorisation URL")
	}

	// And HALF a configuration does not start. A client id without its secret
	// fails at the EXCHANGE — after the customer has been to Google and
	// consented — which reads as goen losing their account.
	if _, err := NewGoogle("client-id", "", "https://goen.example"); err == nil {
		t.Error("a client id with no secret was accepted")
	}
	if _, err := NewGoogle("", "secret", "https://goen.example"); err == nil {
		t.Error("a secret with no client id was accepted")
	}
}

// TestTheStateCookieSurvivesTheRedirectAndCarriesNoOpenRedirect holds the two
// properties the callback depends on.
//
// SameSite=Lax rather than Strict, which is the one that would break everything:
// Strict drops the cookie on a cross-site navigation, so every sign-in would come
// back and fail its own state check. And HttpOnly, because the verifier is the
// secret half of PKCE and a script that could read it could redeem a stolen code.
func TestTheStateCookieSurvivesTheRedirectAndCarriesNoOpenRedirect(t *testing.T) {
	w := httptest.NewRecorder()
	writeOAuthState(w, OAuthState{
		Value: "state-value", Verifier: "verifier-value", Next: "/account",
	}, true)

	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("%d cookies written, want 1", len(cookies))
	}
	c := cookies[0]
	if c.Name != oauthStateCookie {
		t.Errorf("cookie name is %q, want the __Host- one — a name without that "+
			"prefix can be set by a sibling subdomain, which is the login CSRF this "+
			"whole comparison exists to stop", c.Name)
	}
	if !c.HttpOnly {
		t.Error("the state cookie is readable by script; it holds the PKCE verifier")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite is %v, want Lax — Strict is dropped on the way back from "+
			"Google and every sign-in would fail its own state check", c.SameSite)
	}

	// Read back through a request, which is the only path that matters.
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/google/callback", http.NoBody)
	r.AddCookie(c)
	got, ok := readOAuthState(r, true)
	if !ok {
		t.Fatal("the state written could not be read back")
	}
	if got.Value != "state-value" || got.Verifier != "verifier-value" {
		t.Errorf("read back %+v, want the state and verifier that were written", got)
	}

	// An off-site Next is refused on the way OUT, not merely on the way in: a
	// cookie is something the browser holds, and an open redirect at the end of
	// a real sign-in is exactly what a phishing page wants to borrow.
	evil := httptest.NewRecorder()
	writeOAuthState(evil, OAuthState{Value: "s", Verifier: "v", Next: "//evil.example/"}, true)
	er := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	er.AddCookie(evil.Result().Cookies()[0])
	back, _ := readOAuthState(er, true)
	if back.Next != "/account" {
		t.Errorf("an off-site return path survived the cookie: %q", back.Next)
	}
}

// TestAMissingOrTamperedCookieIsRefused proves readOAuthState says no rather
// than returning a half-read state the callback would then compare against.
func TestAMissingOrTamperedCookieIsRefused(t *testing.T) {
	tests := []struct {
		name  string
		value string
		set   bool
	}{
		{name: "absent", set: false},
		{name: "empty", value: "", set: true},
		{name: "not base64", value: "!!!not base64!!!", set: true},
		{name: "too few fields", value: base64.RawURLEncoding.EncodeToString([]byte("only|two")), set: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
			if tt.set {
				//nolint:gosec // G124: a request-side cookie in a test, not one this
				// code sets — the attributes are asserted on the WRITE above.
				r.AddCookie(&http.Cookie{Name: oauthStateCookie, Value: tt.value})
			}
			if _, ok := readOAuthState(r, true); ok {
				t.Error("a cookie that carries no usable state was accepted")
			}
		})
	}
}
