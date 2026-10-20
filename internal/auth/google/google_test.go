package google

import (
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"testing"
)

func TestTheAuthorizationURLCarriesEverySecurityParameter(t *testing.T) {
	g, err := New("client-id", "client-secret", "https://goen.example")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	target, state, err := g.AuthorizeURL()
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
}

func TestTwoSignInsDoNotShareAState(t *testing.T) {
	g, _ := New("client-id", "client-secret", "https://goen.example")
	_, first, err := g.AuthorizeURL()
	if err != nil {
		t.Fatalf("AuthorizeURL: %v", err)
	}
	_, second, err := g.AuthorizeURL()
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
	g, err := New("", "", "https://goen.example")
	if err != nil {
		t.Fatalf("an empty configuration must be legal: %v", err)
	}
	if g.Enabled() {
		t.Error("a client with no credentials reports itself enabled")
	}
	if _, _, err := g.AuthorizeURL(); err == nil {
		t.Error("an unconfigured client built an authorisation URL")
	}

	// Half a configuration does not start: it would fail at the exchange, after consent.
	if _, err := New("client-id", "", "https://goen.example"); err == nil {
		t.Error("a client id with no secret was accepted")
	}
	if _, err := New("", "secret", "https://goen.example"); err == nil {
		t.Error("a secret with no client id was accepted")
	}
}

func TestGoogleRedirectUsesTheSiteOriginGrammar(t *testing.T) {
	if _, err := New("client-id", "client-secret", "httpx://evil.example"); err == nil {
		t.Fatal("New accepted an http-looking non-origin")
	}

	g, err := New("client-id", "client-secret", "https://goen.example/")
	if err != nil {
		t.Fatalf("New refused a canonicalisable origin: %v", err)
	}
	if g.redirectURL != "https://goen.example/auth/google/callback" {
		t.Errorf("redirect URL = %q, want callback built from the canonical origin", g.redirectURL)
	}
}
