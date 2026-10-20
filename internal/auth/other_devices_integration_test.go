//go:build integration

package auth_test

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/auth"
	"github.com/koopa0/goen/internal/auth/google"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/user"
)

func TestSigningOutOtherDevicesKeepsTheCurrentSessionAndOtherUsers(t *testing.T) {
	for _, googleOnly := range []bool{false, true} {
		for _, locale := range i18n.Locales() {
			name := "password/" + locale.Tag()
			if googleOnly {
				name = "google-only/" + locale.Tag()
			}
			t.Run(name, func(t *testing.T) {
				ctx := i18n.WithLocale(t.Context(), locale)
				appPool := accountStorePool(t, "sign-out-other-devices")
				s := auth.NewStore(appPool)
				var u user.User
				if googleOnly {
					var err error
					u, err = s.SignInWithGoogle(ctx, google.Identity{
						Subject: "other-devices-" + uuid.NewString(), Email: "other-devices-" + uuid.NewString() + "@example.com", EmailVerified: true,
					})
					if err != nil {
						t.Fatal(err)
					}
				} else {
					u = registerProved(t, s, "other-devices-"+uuid.NewString()+"@example.com")
				}
				foreign := registerProved(t, s, "foreign-devices-"+uuid.NewString()+"@example.com")
				tokens := make([]string, 0, 4)
				for _, id := range []string{u.ID, u.ID, foreign.ID, foreign.ID} {
					token, err := s.StartSession(ctx, id, "other-device session", "127.0.0.1")
					if err != nil {
						t.Fatal(err)
					}
					tokens = append(tokens, token)
				}
				cookieResponse := httptest.NewRecorder()
				auth.SetSessionCookie(cookieResponse, tokens[0], false)
				cookie := sessionCookie(t, cookieResponse)
				h := auth.NewHandler(s, nil, nil, slog.New(slog.DiscardHandler), false, nil)
				type counts struct{ Own, Foreign int64 }
				assertCounts := func(want counts) {
					t.Helper()
					var got counts
					if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE user_id = $1), count(*) FILTER (WHERE user_id = $2) FROM sessions`, uuid.MustParse(u.ID), uuid.MustParse(foreign.ID)).Scan(&got.Own, &got.Foreign); err != nil {
						t.Fatal(err)
					}
					if diff := cmp.Diff(want, got); diff != "" {
						t.Errorf("remaining sessions (-want +got):\n%s", diff)
					}
				}
				failedPool := accountStorePool(t, "other-device-write-failure")
				failedHandler := auth.NewHandler(auth.NewStore(failedPool), nil, nil, slog.New(slog.DiscardHandler), false, nil)
				failedPool.Close()
				failedRequest := httptest.NewRequestWithContext(ctx, http.MethodPost, "/account/sessions/others/end", nil)
				failedRequest.AddCookie(cookie)
				failedResponse := httptest.NewRecorder()
				h.Authenticate(http.HandlerFunc(failedHandler.SignOutOtherDevices)).ServeHTTP(failedResponse, failedRequest)
				if failedResponse.Code != http.StatusInternalServerError || failedResponse.Header().Get("Location") != "" {
					t.Errorf("failed sign-out write = %d %q, want 500 without a success redirect", failedResponse.Code, failedResponse.Header().Get("Location"))
				}
				assertCounts(counts{Own: 2, Foreign: 2})
				mux := http.NewServeMux()
				mux.HandleFunc("POST /account/sessions/others/end", h.RequireUser(h.SignOutOtherDevices))
				mux.HandleFunc("GET /account", h.RequireUser(accountPage(appPool).Overview))
				app := h.Authenticate(mux)
				for attempt := range 2 {
					form := url.Values{"user_id": {foreign.ID}, "token": {tokens[2]}}
					request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/account/sessions/others/end", strings.NewReader(form.Encode()))
					request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					request.AddCookie(cookie)
					response := httptest.NewRecorder()
					app.ServeHTTP(response, request)
					if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/account?other_devices=signed_out" {
						t.Fatalf("sign-out attempt %d = %d %q, want 303 account confirmation", attempt, response.Code, response.Header().Get("Location"))
					}
					if response.Header().Get("Set-Cookie") != "" {
						t.Error("signing out other devices changes the current browser's cookie")
					}
					assertCounts(counts{Own: 1, Foreign: 2})
					for index, wantID := range []string{u.ID, "", foreign.ID, foreign.ID} {
						signedIn, err := s.SessionUser(ctx, tokens[index])
						if wantID == "" {
							if !errors.Is(err, auth.ErrNotFound) || signedIn.ID != "" {
								t.Errorf("revoked session user = %q, %v; want no user and ErrNotFound", signedIn.ID, err)
							}
							continue
						}
						if err != nil || signedIn.ID != wantID {
							t.Errorf("session %d user = %q, %v; want %q", index, signedIn.ID, err, wantID)
						}
					}
				}
				request := httptest.NewRequestWithContext(ctx, http.MethodGet, "/account?other_devices=signed_out", nil)
				request.AddCookie(cookie)
				response := httptest.NewRecorder()
				app.ServeHTTP(response, request)
				want := "Signed out of your other devices."
				if locale == i18n.ZhHant {
					want = "\u5df2\u767b\u51fa\u5176\u4ed6\u88dd\u7f6e\u3002"
				}
				if response.Code != http.StatusOK || otherDevicesNoticeRole(t, response.Body.String(), want) != "status" {
					t.Errorf("current session cannot read its account confirmation: status %d", response.Code)
				}
			})
		}
	}
}

func otherDevicesNoticeRole(t *testing.T, body, want string) string {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for n := range doc.Descendants() {
		if n.Type != html.ElementNode || n.Data != "p" {
			continue
		}
		var text strings.Builder
		for child := range n.Descendants() {
			if child.Type == html.TextNode {
				text.WriteString(child.Data)
			}
		}
		if strings.TrimSpace(text.String()) != want {
			continue
		}
		for _, attr := range n.Attr {
			if attr.Key == "role" {
				return attr.Val
			}
		}
	}
	return ""
}
