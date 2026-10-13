//go:build integration

package account_test

import (
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/i18n"
)

func TestForgotConfirmationThroughPlainPostKeepsAccountExistencePrivate(t *testing.T) {
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), locale)
			s := account.NewStore(accountStorePool(t, "sent-forgot"))
			known := registerProved(t, s, "target-known-"+uuid.NewString()+"@example.com").Email
			unknown := "target-unknown-" + uuid.NewString() + "@example.com"
			h := account.NewHandler(s, nil, slog.New(slog.DiscardHandler), false, nil)
			mux := http.NewServeMux()
			mux.HandleFunc("POST /forgot", h.Forgot)
			mux.HandleFunc("GET /forgot", h.ForgotPage)
			var previous string
			for _, address := range []string{known, unknown} {
				posted := httptest.NewRecorder()
				mux.ServeHTTP(posted, cartForm(ctx, "/forgot", url.Values{"email": {address}}))
				const want = "/forgot?address=t%2A%2A%2A%40example.com&sent=1"
				if posted.Code != http.StatusSeeOther || posted.Header().Get("Location") != want {
					t.Fatalf("POST /forgot = %d to %q, want 303 to %q", posted.Code, posted.Header().Get("Location"), want)
				}
				if len(posted.Result().Cookies()) != 0 {
					t.Error("POST /forgot introduced a confirmation cookie")
				}
				shown := httptest.NewRecorder()
				mux.ServeHTTP(shown, httptest.NewRequestWithContext(ctx, http.MethodGet, want, http.NoBody))
				body := shown.Body.String()
				if shown.Code != http.StatusOK {
					t.Fatalf("GET confirmation = %d, want 200", shown.Code)
				}
				for _, value := range []string{"t***@example.com", `href="/forgot"`, `id="forgot-resend"`} {
					if !strings.Contains(body, value) {
						t.Errorf("forgot confirmation omits %q", value)
					}
				}
				if strings.Contains(body, address) {
					t.Error("forgot confirmation reveals the complete submitted address")
				}
				if previous != "" && previous != body {
					t.Error("forgot confirmations distinguish known and unknown accounts with the same mask")
				}
				previous = body
				retry := httptest.NewRecorder()
				mux.ServeHTTP(retry, httptest.NewRequestWithContext(ctx, http.MethodGet, "/forgot", http.NoBody))
				if !strings.Contains(retry.Body.String(), `action="/forgot"`) || strings.Contains(retry.Body.String(), `id="forgot-resend"`) {
					t.Error("re-entry does not restore the uncollapsed initial form")
				}
			}
		})
	}
}

func TestEmailChangeConfirmationNamesRequestedTargetBeforeItsWorkerRuns(t *testing.T) {
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), locale)
			s := account.NewStore(accountStorePool(t, "sent-email"))
			owner := registerProved(t, s, "old-"+uuid.NewString()+"@example.com")
			taken := registerProved(t, s, "target-taken-"+uuid.NewString()+"@example.com").Email
			free := "target-free-" + uuid.NewString() + "@example.com"
			h := account.NewHandler(s, nil, slog.New(slog.DiscardHandler), false, nil)
			b := changeBrowser{t: t, h: h}
			cookie := b.signIn(owner.Email)
			mux := http.NewServeMux()
			mux.HandleFunc("POST /account/email", h.RequireUser(h.ChangeEmail))
			mux.HandleFunc("GET /account", h.RequireUser(h.Overview))
			serve := h.Authenticate(mux)
			var previous string
			for _, address := range []string{taken, free} {
				req := cartForm(ctx, "/account/email", url.Values{"email": {address}, "current": {"a sufficiently long password"}})
				req.AddCookie(cookie)
				posted := httptest.NewRecorder()
				serve.ServeHTTP(posted, req)
				const want = "/account?address=t%2A%2A%2A%40example.com&email=sent"
				if posted.Code != http.StatusSeeOther || posted.Header().Get("Location") != want {
					t.Fatalf("email change = %d to %q, want 303 to %q", posted.Code, posted.Header().Get("Location"), want)
				}
				get := httptest.NewRequestWithContext(ctx, http.MethodGet, want, http.NoBody)
				get.AddCookie(cookie)
				shown := httptest.NewRecorder()
				serve.ServeHTTP(shown, get)
				body := shown.Body.String()
				if shown.Code != http.StatusOK {
					t.Fatalf("email confirmation GET = %d, want 200", shown.Code)
				}
				expected := "If t***@example.com can be used for your account"
				if locale == i18n.ZhHant {
					expected = "\u5982\u679c t***@example.com \u53ef\u4ee5\u7528\u65bc\u4f60\u7684\u5e33\u865f"
				}
				for _, value := range []string{html.EscapeString(expected), `href="/account?email=reenter#account-email-form"`, owner.Email} {
					if !strings.Contains(body, value) {
						t.Errorf("email confirmation omits %q", value)
					}
				}
				if strings.Contains(body, address) {
					t.Error("email confirmation reveals the full requested target")
				}
				if previous != "" && previous != body {
					t.Error("email confirmations distinguish taken and free targets before worker processing")
				}
				previous = body
				reenter := httptest.NewRequestWithContext(ctx, http.MethodGet, "/account?email=reenter", http.NoBody)
				reenter.AddCookie(cookie)
				opened := httptest.NewRecorder()
				serve.ServeHTTP(opened, reenter)
				if opened.Code != http.StatusOK || !strings.Contains(opened.Body.String(), `id="account-email-form" open`) || !strings.Contains(opened.Body.String(), `action="/account/email"`) {
					t.Error("email re-entry did not open the existing change form")
				}
			}
		})
	}
}
