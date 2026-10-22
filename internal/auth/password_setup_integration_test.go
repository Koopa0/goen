//go:build integration

package auth_test

import (
	"encoding/json"
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/auth"
	"github.com/koopa0/goen/internal/auth/google"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/outbox"
)

func TestGoogleOnlyPasswordSetupUsesTheCurrentMailboxAndExistingReset(t *testing.T) {
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), locale)
			appPool := accountStorePool(t, "google-password-setup")
			s := auth.NewStore(appPool)
			u, err := s.SignInWithGoogle(ctx, google.Identity{
				Subject: "password-setup-" + uuid.NewString(), Email: "password-setup-" + uuid.NewString() + "@example.com", EmailVerified: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			foreign := registerProved(t, s, "setup-foreign-"+uuid.NewString()+"@example.com")
			token, err := s.StartSession(ctx, u.ID, "password setup", "127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			cookieResponse := httptest.NewRecorder()
			auth.SetSessionCookie(cookieResponse, token, false)
			cookie := sessionCookie(t, cookieResponse)
			h := auth.NewHandler(s, nil, nil, slog.New(slog.DiscardHandler), false, nil)
			mux := http.NewServeMux()
			mux.HandleFunc("POST /account/password/setup", h.RequestPasswordSetup)
			mux.HandleFunc("GET /account", h.RequireUser(accountPage(appPool).Overview))
			app := h.Authenticate(mux)
			forged := httptest.NewRequestWithContext(ctx, http.MethodGet, "/account?password_setup=sent", nil)
			forged.AddCookie(cookie)
			forgedResponse := httptest.NewRecorder()
			app.ServeHTTP(forgedResponse, forged)
			if forgedResponse.Code != http.StatusOK || strings.Contains(forgedResponse.Body.String(), "password-setup-sent") {
				t.Errorf("query-only confirmation status = %d, want 200 without a sent notice", forgedResponse.Code)
			}
			form := url.Values{"email": {foreign.Email}, "user_id": {foreign.ID}, "password": {"a forged new password"}}
			request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/account/password/setup", strings.NewReader(form.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.AddCookie(cookie)
			response := httptest.NewRecorder()
			app.ServeHTTP(response, request)
			if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/account?password_setup=sent#password-heading" {
				t.Fatalf("setup mail request = %d %q, want 303 password-section confirmation", response.Code, response.Header().Get("Location"))
			}
			var key, recipient, queuedLocale string
			if queryErr := pool.QueryRow(ctx, `SELECT dedupe_key, payload->>'user_id', payload->>'locale' FROM outbox_messages WHERE topic = $1 AND payload->>'user_id' = $2`, outbox.TopicPasswordResetRequest.Name(), u.ID).Scan(&key, &recipient, &queuedLocale); queryErr != nil {
				t.Fatal(queryErr)
			}
			if recipient != u.ID || queuedLocale != locale.Tag() {
				t.Errorf("queued setup request recipient/locale = %q/%q, want %q/%q", recipient, queuedLocale, u.ID, locale.Tag())
			}
			var foreignRequests int
			if countErr := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_messages WHERE topic = $1 AND payload->>'user_id' = $2`, outbox.TopicPasswordResetRequest.Name(), foreign.ID).Scan(&foreignRequests); countErr != nil {
				t.Fatal(countErr)
			}
			if foreignRequests != 0 {
				t.Errorf("forged email queued %d reset requests for another account", foreignRequests)
			}
			view, err := account.NewStore(appPool).Overview(ctx, u, "")
			if err != nil {
				t.Fatal(err)
			}
			if view.CanUnlinkGoogle {
				t.Error("requesting a setup email added a password without mailbox proof")
			}
			location, err := url.Parse(response.Header().Get("Location"))
			if err != nil {
				t.Fatal(err)
			}
			location.Fragment = ""
			get := httptest.NewRequestWithContext(ctx, http.MethodGet, location.String(), nil)
			for _, noticeCookie := range response.Result().Cookies() {
				get.AddCookie(noticeCookie)
			}
			get.AddCookie(cookie)
			confirmation := httptest.NewRecorder()
			app.ServeHTTP(confirmation, get)
			want := "A link to set your password was sent to " + u.Email + "."
			if locale == i18n.ZhHant {
				want = "\u8a2d\u5b9a\u5bc6\u78bc\u7684\u9023\u7d50\u5df2\u5bc4\u5230 " + u.Email + "\u3002"
			}
			if confirmation.Code != http.StatusOK || !strings.Contains(html.UnescapeString(confirmation.Body.String()), want) {
				t.Errorf("account confirmation status = %d or lacks the current mailbox notice", confirmation.Code)
			}
			repeated := httptest.NewRequestWithContext(ctx, http.MethodGet, location.String(), nil)
			repeated.AddCookie(cookie)
			repeatedResponse := httptest.NewRecorder()
			app.ServeHTTP(repeatedResponse, repeated)
			if strings.Contains(repeatedResponse.Body.String(), "password-setup-sent") {
				t.Error("consumed browser receipt still shows the sent notice")
			}
			if issueErr := auth.IssueQueuedReset(ctx, s, key); issueErr != nil {
				t.Fatal(issueErr)
			}
			var payload []byte
			if payloadErr := pool.QueryRow(ctx, `SELECT payload FROM outbox_messages WHERE topic = $1 AND payload->>'email' = $2`, outbox.TopicPasswordReset.Name(), u.Email).Scan(&payload); payloadErr != nil {
				t.Fatal(payloadErr)
			}
			var mail outbox.PasswordReset
			if decodeErr := json.Unmarshal(payload, &mail); decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if mail.Email != u.Email || mail.Locale != locale.Tag() || mail.Token == "" {
				t.Error("existing reset worker did not prepare a link to the current mailbox")
			}
			if _, resetErr := s.CompleteReset(ctx, mail.Token, "a mailbox-proved password"); resetErr != nil {
				t.Fatal(resetErr)
			}
			view, err = account.NewStore(appPool).Overview(ctx, u, "")
			if err != nil {
				t.Fatal(err)
			}
			if !view.CanUnlinkGoogle {
				t.Error("mailbox-proved password still cannot unlink Google")
			}
			signedIn, err := s.Authenticate(ctx, u.Email, "a mailbox-proved password")
			if err != nil {
				t.Fatal(err)
			}
			freshToken, err := s.StartSession(ctx, signedIn.ID, "after mailbox proof", "127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			freshCookies := httptest.NewRecorder()
			auth.SetSessionCookie(freshCookies, freshToken, false)
			accountRequest := httptest.NewRequestWithContext(ctx, http.MethodGet, "/account", nil)
			accountRequest.AddCookie(sessionCookie(t, freshCookies))
			accountResponse := httptest.NewRecorder()
			app.ServeHTTP(accountResponse, accountRequest)
			if accountResponse.Code != http.StatusOK || !strings.Contains(accountResponse.Body.String(), `action="/account/google/unlink"`) || strings.Contains(accountResponse.Body.String(), `action="/account/password/setup#password-heading"`) {
				t.Errorf("mailbox-proved account does not offer unlinking instead of setup: status %d", accountResponse.Code)
			}
		})
	}
}

func TestPasswordSetupSharesTheForgotPasswordMailboxBudget(t *testing.T) {
	ctx := t.Context()
	s := auth.NewStore(accountStorePool(t, "password-setup-mailbox-limit"))
	u, err := s.SignInWithGoogle(ctx, google.Identity{
		Subject: "setup-bound-" + uuid.NewString(), Email: "setup-bound-" + uuid.NewString() + "@example.com", EmailVerified: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.StartSession(ctx, u.ID, "mailbox budget", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	cookies := httptest.NewRecorder()
	auth.SetSessionCookie(cookies, token, false)
	h := auth.NewHandler(s, nil, nil, slog.New(slog.DiscardHandler), false, nil)
	for range 3 {
		response := httptest.NewRecorder()
		h.Forgot(response, cartForm(ctx, "/forgot", url.Values{"email": {u.Email}}))
		if response.Code != http.StatusSeeOther {
			t.Fatalf("initial forgotten-password request = %d, want 303", response.Code)
		}
	}
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/account/password/setup", nil)
	request.AddCookie(sessionCookie(t, cookies))
	response := httptest.NewRecorder()
	h.Authenticate(http.HandlerFunc(h.RequestPasswordSetup)).ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/account?password_setup=sent#password-heading" {
		t.Errorf("setup after spent forgot budget = %d %q, want 303 to the sent notice", response.Code, response.Header().Get("Location"))
	}
	var requests int
	if countErr := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_messages WHERE topic = $1 AND payload->>'user_id' = $2`, outbox.TopicPasswordResetRequest.Name(), u.ID).Scan(&requests); countErr != nil {
		t.Fatal(countErr)
	}
	if requests != 3 {
		t.Errorf("spent mailbox budget queued %d requests, want only the original 3", requests)
	}
}
