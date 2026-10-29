//go:build integration

package auth_test

import (
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/auth"
	"github.com/koopa0/goen/internal/auth/google"
)

// TestHandler_GoogleCallbackReload: a Google sign-in that succeeds answers 200
// with a page whose refresh and link load the destination chosen before leaving
// for Google, carries the new session cookie, and is not stored. A 303 would
// stay inside the navigation that began at Google, where the SameSite=Strict
// session cookie is withheld and the landing page sends the visitor back to
// /signin. A sign-in Google or goen refuses still redirects there.
func TestHandler_GoogleCallbackReload(t *testing.T) {
	for _, next := range []string{"/account", "/account/orders", "/p/headphones?variant=blue#buy"} {
		t.Run("a success headed for "+next, func(t *testing.T) {
			res := googleCallback(t, next, true)
			if res.Code != http.StatusOK {
				t.Fatalf("GoogleCallback = %d, want 200 for the page that loads %s", res.Code, next)
			}
			if loc := res.Header().Get("Location"); loc != "" {
				t.Errorf("the success also redirects to %q; a redirect is the navigation that loses the cookie", loc)
			}
			if got := res.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
				t.Errorf("Content-Type = %q, want text/html", got)
			}
			if got := res.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store: the answer sets a session", got)
			}
			if got := reloadTarget(t, res); got != next {
				t.Errorf("the refresh loads %q, want %q", got, next)
			}
			if want := `href="` + html.EscapeString(next) + `"`; !strings.Contains(res.Body.String(), want) {
				t.Errorf("the page has no %s for a browser that does not refresh:\n%s", want, res.Body.String())
			}
			sessionCookie(t, res)
		})
	}

	t.Run("an unverified address", func(t *testing.T) {
		res := googleCallback(t, "/account", false)
		if res.Code != http.StatusSeeOther {
			t.Fatalf("GoogleCallback = %d, want 303 to /signin", res.Code)
		}
		if loc := res.Header().Get("Location"); loc != "/signin?oauth=unverified" {
			t.Errorf("Location = %q, want /signin?oauth=unverified", loc)
		}
		for _, c := range res.Result().Cookies() {
			if strings.HasSuffix(c.Name, "goen_session") {
				t.Errorf("a refused sign-in set the session cookie %q", c.Name)
			}
		}
	})
}

// googleCallback starts a Google sign-in headed for next and returns the
// callback's answer, with Google standing in as an account whose address is
// verified or not.
func googleCallback(t *testing.T, next string, verified bool) *httptest.ResponseRecorder {
	t.Helper()
	suffix := uuid.NewString()
	g, err := google.New("client-id", "client-secret", "https://goen.example")
	if err != nil {
		t.Fatalf("google.New: %v", err)
	}
	google.SetHTTPClient(g, &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"access_token":"fixture"}`
		if r.URL.Path == "/v1/userinfo" {
			body = fmt.Sprintf(`{"sub":%q,"email":%q,"email_verified":%t,"name":"Fixture"}`,
				suffix, "reload-"+suffix+"@example.com", verified)
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(body)), Request: r,
		}, nil
	})})
	h := auth.NewHandler(auth.NewStore(accountStorePool(t, "google-callback-reload")), nil, nil,
		slog.New(slog.DiscardHandler), false, g)

	start := httptest.NewRecorder()
	h.GoogleSignIn(start, httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/auth/google?next="+url.QueryEscape(next), http.NoBody))
	authorise, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse the authorisation redirect: %v", err)
	}
	callback := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/auth/google/callback?code=fixture&state="+url.QueryEscape(authorise.Query().Get("state")), http.NoBody)
	for _, c := range start.Result().Cookies() {
		callback.AddCookie(c)
	}
	res := httptest.NewRecorder()
	h.GoogleCallback(res, callback)
	return res
}

// reloadTarget is where the page's refresh sends the browser, as the browser
// reads it.
func reloadTarget(t *testing.T, res *httptest.ResponseRecorder) string {
	t.Helper()
	const marker = `<meta http-equiv="refresh" content="0; url=`
	_, rest, ok := strings.Cut(res.Body.String(), marker)
	if !ok {
		t.Fatalf("the answer has no refresh to follow (status %d):\n%s", res.Code, res.Body.String())
	}
	target, _, _ := strings.Cut(rest, `">`)
	return html.UnescapeString(target)
}
