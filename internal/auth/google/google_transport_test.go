package google

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// googleStandIn answers Google's two endpoints from memory and records where
// each request was sent and what the token request carried.
type googleStandIn struct {
	padToken, padProfile int
	hosts                []string
	tokenForm            url.Values
}

func (s *googleStandIn) RoundTrip(r *http.Request) (*http.Response, error) {
	s.hosts = append(s.hosts, r.URL.Scheme+"://"+r.URL.Host+r.URL.Path)
	body := `{"sub":"109876543210","email":"someone@example.com","email_verified":true,"name":"某人"}`
	pad := s.padProfile
	if r.URL.Path == "/token" {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		if s.tokenForm, err = url.ParseQuery(string(raw)); err != nil {
			return nil, err
		}
		body, pad = `{"access_token":"ya29.stand-in"}`, s.padToken
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(strings.Repeat(" ", pad) + body)),
		Request:    r,
	}, nil
}

// TestClient_ExchangeEndpointsBound: Exchange sends both requests to Google's
// fixed endpoints only, names the configured callback as redirect_uri whatever
// the callback's code says, and refuses a reply past the size bound instead of
// reading it whole. This holds the OAuth client's outbound half; each padded
// reply is otherwise valid, so only the bound can refuse it.
func TestClient_ExchangeEndpointsBound(t *testing.T) {
	tests := []struct {
		name       string
		standIn    googleStandIn
		wantSignIn bool
	}{
		{name: "ordinary replies", wantSignIn: true},
		{name: "a token reply past the bound", standIn: googleStandIn{padToken: 1 << 20}},
		{name: "a profile reply past the bound", standIn: googleStandIn{padProfile: 1 << 20}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, err := New("client-id", "client-secret", "https://goen.example")
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			standIn := tt.standIn
			g.http.Transport = &standIn

			identity, err := g.Exchange(t.Context(), "https://attacker.example/@evil.example", "verifier")
			if tt.wantSignIn {
				if err != nil || identity.Subject != "109876543210" {
					t.Fatalf("Exchange() = %+v, %v; want the stand-in identity", identity, err)
				}
				want := []string{
					"https://oauth2.googleapis.com/token",
					"https://openidconnect.googleapis.com/v1/userinfo",
				}
				if strings.Join(standIn.hosts, " ") != strings.Join(want, " ") {
					t.Errorf("requests went to %v, want only %v", standIn.hosts, want)
				}
				if got := standIn.tokenForm.Get("redirect_uri"); got != "https://goen.example/auth/google/callback" {
					t.Errorf("the token request names redirect_uri %q, want the configured callback", got)
				}
				return
			}
			if err == nil {
				t.Fatalf("Exchange() read a reply past the bound whole and signed in %+v", identity)
			}
		})
	}
}

// TestNewHTTPClient: the HTTP client New builds has a finite timeout and the
// default transport, whose certificate verification is on. It is the client
// that carries the client secret.
func TestNewHTTPClient(t *testing.T) {
	g, err := New("client-id", "client-secret", "https://goen.example")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if g.http.Timeout <= 0 {
		t.Errorf("the Google client has timeout %v, want a finite one", g.http.Timeout)
	}
	if g.http.Transport != nil {
		t.Errorf("the Google client carries its own transport %T; certificate verification "+
			"is only known to be on in http.DefaultTransport", g.http.Transport)
	}
}
