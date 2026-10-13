//go:build integration

package account_test

import (
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

func TestResentVerificationAcknowledgesTheSameSignInAddress(t *testing.T) {
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), locale)
			store := account.NewStore(accountStorePool(t, "same-address-verification"))
			person := registerProved(t, store, "verify-again-"+uuid.NewString()+"@example.com")
			browser := changeBrowser{t: t, h: account.NewHandler(store, nil, slog.New(slog.DiscardHandler), false, nil)}
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
			want := "Your email address is confirmed. Use this address to sign in; notices go there too."
			if locale == i18n.ZhHant {
				want = "\u96fb\u5b50\u90f5\u4ef6\u5df2\u78ba\u8a8d\uff0c\u4e4b\u5f8c\u8acb\u7528\u9019\u500b\u4fe1\u7bb1\u767b\u5165\uff0c\u901a\u77e5\u4fe1\u4e5f\u6703\u5bc4\u5230\u9019\u88e1\u3002"
			}
			if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), want) {
				t.Errorf("same-address acknowledgement status=%d, want 200 with %q", page.Code, want)
			}
			if got := emailOf(t, person.ID); got != person.Email {
				t.Errorf("resent verification address=%q, want unchanged %q", got, person.Email)
			}
		})
	}
}
