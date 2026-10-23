//go:build integration

package auth_test

import (
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/auth"
	"github.com/koopa0/goen/internal/i18n"
)

// TestHandler_ResendVerification follows a resend to the account's own
// address through its confirmation link: the acknowledgement is the generic one
// and the address stays as it was.
func TestHandler_ResendVerification(t *testing.T) {
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), locale)
			store := auth.NewStore(accountStorePool(t, "same-address-verification"))
			person := registerProved(t, store, "verify-again-"+uuid.NewString()+"@example.com")
			browser := changeBrowser{t: t, h: auth.NewHandler(store, nil, nil, slog.New(slog.DiscardHandler), false, nil)}
			session := browser.signIn(person.Email)
			request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/account/email/resend", nil)
			response := browser.serve(browser.h.ResendVerification, request, session)
			if response.Code != http.StatusSeeOther {
				t.Fatalf("resend verification status=%d, want 303", response.Code)
			}
			token, _ := queuedLink(t, person.Email)
			confirm := cartForm(ctx, "/verify", url.Values{"token": {token}})
			confirmed := browser.serve(browser.h.Verify, confirm, session)
			if confirmed.Code != http.StatusSeeOther || confirmed.Header().Get("Location") != "/verify?done=1" {
				t.Fatalf("same-address verification status/location=%d/%q, want 303 safe acknowledgement", confirmed.Code, confirmed.Header().Get("Location"))
			}
			page := browser.serve(browser.h.VerifyPage, httptest.NewRequestWithContext(ctx, http.MethodGet, confirmed.Header().Get("Location"), nil), nil)
			want := html.EscapeString(i18n.T(ctx, i18n.KeyVerifyDoneBody))
			if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), want) {
				t.Errorf("same-address acknowledgement status=%d, want 200 with %q", page.Code, want)
			}
			if got := emailOf(t, person.ID); got != person.Email {
				t.Errorf("resent verification address=%q, want unchanged %q", got, person.Email)
			}
		})
	}
}

// TestHandler_ResendVerificationBound: one account's resend past its allowance
// is refused with 429 and the account's own Retry-After.
func TestHandler_ResendVerificationBound(t *testing.T) {
	store := auth.NewStore(pool)
	browser := changeBrowser{t: t, h: auth.NewHandler(store, nil, nil, slog.New(slog.DiscardHandler), false, nil)}
	session := browser.signIn(registerProved(t, store, "verify-bound-"+uuid.NewString()+"@example.com").Email)
	resend := func() *httptest.ResponseRecorder {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/account/email/resend", nil)
		return browser.serve(browser.h.ResendVerification, request, session)
	}

	for i := range accountMailBudget {
		if code := resend().Code; code != http.StatusSeeOther {
			t.Fatalf("resend %d answered %d, want 303", i+1, code)
		}
	}
	refused := resend()
	if refused.Code != http.StatusTooManyRequests {
		t.Errorf("the resend past the account's allowance answered %d, want 429", refused.Code)
	} else if wait, err := strconv.Atoi(refused.Header().Get("Retry-After")); err != nil || wait <= 19*60 {
		t.Errorf("a refused account may ask again in %q s; want the twenty-minute pace",
			refused.Header().Get("Retry-After"))
	}
}
