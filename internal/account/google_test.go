package account

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

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
	// The challenge must be the SHA-256 of the verifier, or PKCE is decoration.
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

	// next travels in the STATE: in the URL it is an open redirect Google echoes back.
	if strings.Contains(target, "/account/orders") {
		t.Error("the authorisation URL carries the return path; it belongs in the state cookie")
	}
	if state.Next != "/account/orders" {
		t.Errorf("the state carries %q, want the path the visitor was headed for", state.Next)
	}
}

// TestARefusedCodeExchangeLogsOnlyItsStatusAndErrorCode drives a callback
// whose code Google refuses, and reads what the handler logs. A refusal's body
// is free text chosen by whoever answered, so only its status and its OAuth
// error code may reach the log.
func TestARefusedCodeExchangeLogsOnlyItsStatusAndErrorCode(t *testing.T) {
	const kept = "invalid_grant"
	for name, tt := range map[string]struct {
		body     string
		wantCode string
	}{
		"an OAuth refusal": {
			body:     `{"error":"invalid_grant","error_description":"description text DROPME","extra":"DROPME"}`,
			wantCode: kept,
		},
		"a code that is not one": {
			body:     `{"error":"invalid grant DROPME"}`,
			wantCode: "unrecognised",
		},
		// 41 bytes of the alphabet codes use: the length alone refuses it.
		"a code longer than any OAuth defines": {
			body:     `{"error":"invalid_invalid_invalid_invalid_invalid_x"}`,
			wantCode: "unrecognised",
		},
		"not JSON": {
			body:     `<html>proxy page DROPME</html>`,
			wantCode: "unrecognised",
		},
	} {
		t.Run(name, func(t *testing.T) {
			g, err := NewGoogle("client-id", "client-secret", "https://goen.example")
			if err != nil {
				t.Fatalf("NewGoogle: %v", err)
			}
			g.http = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusBadRequest, Header: make(http.Header),
					Body: io.NopCloser(strings.NewReader(tt.body)), Request: r,
				}, nil
			})}
			var logs bytes.Buffer
			h := NewHandler(deadAccountStore(t), nil, slog.New(slog.NewJSONHandler(&logs, nil)), false, g)

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
			if loc := res.Header().Get("Location"); loc != "/signin?oauth=failed" {
				t.Fatalf("a refused exchange lands at %q, want /signin?oauth=failed", loc)
			}

			line := logs.String()
			if !strings.Contains(line, "exchange the google code") {
				t.Fatalf("the refusal was not logged: %s", line)
			}
			if !strings.Contains(line, "400 "+tt.wantCode) {
				t.Errorf("the log does not carry the status and %q: %s", tt.wantCode, line)
			}
			if strings.Contains(line, "DROPME") || strings.Contains(line, "the-code") {
				t.Errorf("the log carries the refusal's body or the code: %s", line)
			}
		})
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

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

	// Half a configuration does not start: it would fail at the exchange, after consent.
	if _, err := NewGoogle("client-id", "", "https://goen.example"); err == nil {
		t.Error("a client id with no secret was accepted")
	}
	if _, err := NewGoogle("", "secret", "https://goen.example"); err == nil {
		t.Error("a secret with no client id was accepted")
	}
}

func TestGoogleRedirectUsesTheSiteOriginGrammar(t *testing.T) {
	if _, err := NewGoogle("client-id", "client-secret", "httpx://evil.example"); err == nil {
		t.Fatal("NewGoogle accepted an http-looking non-origin")
	}

	g, err := NewGoogle("client-id", "client-secret", "https://goen.example/")
	if err != nil {
		t.Fatalf("NewGoogle refused a canonicalisable origin: %v", err)
	}
	if g.redirectURL != "https://goen.example/auth/google/callback" {
		t.Errorf("redirect URL = %q, want callback built from the canonical origin", g.redirectURL)
	}
}

// Starting a Google sign-in is bounded per client. An IPv6 client chooses the
// low 64 bits of its address freely, so a limit keyed on the whole address is
// no limit.
func TestTheGoogleSignInLimitCoversAWholeIPv6Slash64(t *testing.T) {
	g, err := NewGoogle("client-id", "client-secret", "https://goen.example")
	if err != nil {
		t.Fatalf("NewGoogle: %v", err)
	}
	h := &Handler{
		log: slog.New(slog.DiscardHandler), google: g,
		signinLimit: accountTestLimiter(), resetLimit: accountTestLimiter(),
	}

	start := func(remoteAddr string) int {
		t.Helper()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/google", http.NoBody)
		req.RemoteAddr = remoteAddr
		res := httptest.NewRecorder()
		h.GoogleSignIn(res, req)
		return res.Code
	}

	if got := start("[2001:db8:1:2::1]:1000"); got != http.StatusSeeOther {
		t.Fatalf("the first sign-in answered %d, want 303 to Google", got)
	}
	if got := start("[2001:db8:1:2:aaaa:bbbb:cccc:dddd]:1001"); got != http.StatusTooManyRequests {
		t.Errorf("another address in the same /64 answered %d, want 429; "+
			"rotating the low bits bought a fresh allowance", got)
	}
	if got := start("[2001:db8:1:3::1]:1002"); got != http.StatusSeeOther {
		t.Errorf("an address in another /64 answered %d, want 303; it shared a bucket", got)
	}
}

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

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/google/callback", http.NoBody)
	r.AddCookie(c)
	got, ok := readOAuthState(r, true)
	if !ok {
		t.Fatal("the state written could not be read back")
	}
	if got.Value != "state-value" || got.Verifier != "verifier-value" {
		t.Errorf("read back %+v, want the state and verifier that were written", got)
	}

	// An off-site Next is refused on the way OUT, not merely on the way in.
	evil := httptest.NewRecorder()
	writeOAuthState(evil, OAuthState{Value: "s", Verifier: "v", Next: "//evil.example/"}, true)
	er := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	er.AddCookie(evil.Result().Cookies()[0])
	back, _ := readOAuthState(er, true)
	if back.Next != "/account" {
		t.Errorf("an off-site return path survived the cookie: %q", back.Next)
	}
}

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
