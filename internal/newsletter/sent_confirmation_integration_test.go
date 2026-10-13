//go:build integration

package newsletter_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestNewsletterConfirmationMatchesWhetherItQueuesMailOrAlreadyHasASubscriber(t *testing.T) {
	for _, locale := range i18n.Locales() {
		for _, htmx := range []bool{false, true} {
			t.Run(locale.Tag()+"/"+map[bool]string{false: "plain", true: "htmx"}[htmx], func(t *testing.T) {
				ctx := i18n.WithLocale(t.Context(), locale)
				h := recoveryNewsletterHandler(t)
				s := store(t)
				fresh, active := addr(t), addr(t)
				if _, err := s.Request(ctx, active); err != nil {
					t.Fatal(err)
				}
				if _, err := s.Confirm(ctx, tokenFor(t, "newsletter.confirm", active, "token")); err != nil {
					t.Fatal(err)
				}
				mux := http.NewServeMux()
				mux.HandleFunc("POST /newsletter", h.Submit)
				mux.HandleFunc("GET /newsletter/thanks", h.Thanks)
				var previous string
				for _, address := range []string{fresh, active} {
					before := enqueued(t, "newsletter.confirm", address)
					req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/newsletter", strings.NewReader(url.Values{"email": {address}}.Encode()))
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					req.RemoteAddr = "203.0.113.20:1234"
					if htmx {
						req.Header.Set("HX-Request", "true")
					}
					res := httptest.NewRecorder()
					mux.ServeHTTP(res, req)
					body := res.Body.String()
					if htmx && res.Code != http.StatusOK {
						t.Fatalf("HTMX newsletter = %d, want 200", res.Code)
					}
					if !htmx {
						body = plainThanksBody(ctx, t, mux, res)
					}
					for _, want := range []string{"n***@goen.invalid", `href="/#newsletter-form"`} {
						if !strings.Contains(body, want) {
							t.Errorf("newsletter confirmation omits %q", want)
						}
					}
					if strings.Contains(body, address) {
						t.Error("newsletter confirmation reveals the complete mailbox")
					}
					if previous != "" && previous != body {
						t.Error("newsletter confirmations distinguish pending requests and existing subscribers")
					}
					previous = body
					wantDelta := 1
					if address == active {
						wantDelta = 0
					}
					if got := enqueued(t, "newsletter.confirm", address) - before; got != wantDelta {
						t.Errorf("confirmation mail delta = %d, want %d", got, wantDelta)
					}
					if len(res.Result().Cookies()) != 0 {
						t.Error("newsletter introduced a confirmation cookie")
					}
				}
			})
		}
	}
}

// plainThanksBody checks the plain POST redirect and returns the page it names.
func plainThanksBody(ctx context.Context, t *testing.T, mux *http.ServeMux, res *httptest.ResponseRecorder) string {
	t.Helper()
	const want = "/newsletter/thanks?address=n%2A%2A%2A%40goen.invalid"
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != want {
		t.Fatalf("plain newsletter = %d to %q, want 303 to %q", res.Code, res.Header().Get("Location"), want)
	}
	shown := httptest.NewRecorder()
	mux.ServeHTTP(shown, httptest.NewRequestWithContext(ctx, http.MethodGet, want, http.NoBody))
	if shown.Code != http.StatusOK {
		t.Fatalf("newsletter thanks = %d, want 200", shown.Code)
	}
	return shown.Body.String()
}
