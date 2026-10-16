//go:build integration

package auth_test

import (
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
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
