//go:build integration

package auth

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/auth/google"
	"github.com/koopa0/goen/internal/i18n"
)

// These callbacks run through the real Google client against a stand-in for
// Google's two endpoints. The client's transport is set only by
// google.SetHTTPClient, which exists in integration builds alone, so they live
// here rather than beside the handler's unit tests.

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
			g, err := google.New("client-id", "client-secret", "https://goen.example")
			if err != nil {
				t.Fatalf("google.New: %v", err)
			}
			google.SetHTTPClient(g, &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusBadRequest, Header: make(http.Header),
					Body: io.NopCloser(strings.NewReader(tt.body)), Request: r,
				}, nil
			})})
			var logs bytes.Buffer
			h := NewHandler(deadAccountStore(t), nil, nil, slog.New(slog.NewJSONHandler(&logs, nil)), false, g)

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

// TestGoogleIsNeverLinkedToTheDemoAccount: a Google sign-in at an account's
// address links Google to that account, and whoever holds a Google account at
// the demo address would then sign in with Google alone.
func TestGoogleIsNeverLinkedToTheDemoAccount(t *testing.T) {
	g, err := google.New("client-id", "client-secret", "https://goen.example")
	if err != nil {
		t.Fatalf("google.New: %v", err)
	}
	google.SetHTTPClient(g, &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		body := `{"access_token":"a-token"}`
		if r.Method == http.MethodGet {
			body = `{"sub":"google-subject","email":"Demo@goen.example","email_verified":true}`
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(body)), Request: r,
		}, nil
	})})
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
