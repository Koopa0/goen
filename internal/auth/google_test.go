package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/auth/google"
)

// TestHandler_GoogleSignInStateCookie: GoogleSignIn keeps the return path in
// the state cookie and not in the authorisation URL, and the cookie's state and
// verifier are the ones the URL's state and PKCE challenge were made from. In
// the authorisation URL the return path would be an open redirect Google echoes
// back.
func TestHandler_GoogleSignInStateCookie(t *testing.T) {
	g, err := google.New("client-id", "client-secret", "https://goen.example")
	if err != nil {
		t.Fatalf("google.New: %v", err)
	}
	h := &Handler{log: slog.New(slog.DiscardHandler), google: g, signinLimit: accountTestLimiter()}

	start := httptest.NewRecorder()
	h.GoogleSignIn(start, httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/auth/google?next=/account/orders", http.NoBody))
	if start.Code != http.StatusSeeOther {
		t.Fatalf("starting a sign-in answered %d, want 303 to Google", start.Code)
	}
	target := start.Header().Get("Location")
	if strings.Contains(target, "/account/orders") {
		t.Error("the authorisation URL carries the return path; it belongs in the state cookie")
	}
	u, err := url.Parse(target)
	if err != nil {
		t.Fatalf("the authorisation URL does not parse: %v", err)
	}

	back := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/google/callback", http.NoBody)
	for _, c := range start.Result().Cookies() {
		back.AddCookie(c)
	}
	state, ok := readOAuthState(back, false)
	if !ok {
		t.Fatal("the state cookie written could not be read back")
	}
	if state.Next != "/account/orders" {
		t.Errorf("the state carries %q, want the path the visitor was headed for", state.Next)
	}
	if q := u.Query().Get("state"); q != state.Value || q == "" {
		t.Errorf("state in the URL is %q and the browser is told to remember %q", q, state.Value)
	}
	sum := sha256.Sum256([]byte(state.Verifier))
	if want := base64.RawURLEncoding.EncodeToString(sum[:]); u.Query().Get("code_challenge") != want {
		t.Errorf("code_challenge is %q, want the SHA-256 of the verifier the cookie keeps",
			u.Query().Get("code_challenge"))
	}
}

// TestHandler_GoogleSignInIPv6Limit: starting a Google sign-in is bounded per
// client network, and an IPv6 client's whole /64 shares one bucket while
// another /64 has its own. An IPv6 client chooses the low 64 bits of its
// address freely, so a limit keyed on the whole address is no limit.
func TestHandler_GoogleSignInIPv6Limit(t *testing.T) {
	g, err := google.New("client-id", "client-secret", "https://goen.example")
	if err != nil {
		t.Fatalf("google.New: %v", err)
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

// TestWriteOAuthState: writeOAuthState sets one __Host- prefixed, HttpOnly,
// SameSite=Lax cookie that readOAuthState reads back with the state and
// verifier written, and an off-site return path does not survive it, on the way
// out as well as on the way in.
func TestWriteOAuthState(t *testing.T) {
	w := httptest.NewRecorder()
	writeOAuthState(w, oauthState{
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
	writeOAuthState(evil, oauthState{Value: "s", Verifier: "v", Next: "//evil.example/"}, true)
	er := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	er.AddCookie(evil.Result().Cookies()[0])
	back, _ := readOAuthState(er, true)
	if back.Next != "/account" {
		t.Errorf("an off-site return path survived the cookie: %q", back.Next)
	}
}

// TestReadOAuthState: readOAuthState refuses a missing cookie and one that
// carries no usable state.
func TestReadOAuthState(t *testing.T) {
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
