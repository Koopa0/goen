//go:build integration

package payment_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	stripe "github.com/stripe/stripe-go/v86"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/shoptime"
)

func TestPaymentWindowPageAndPostOfferRecovery(t *testing.T) {
	gateway, err := payment.NewGateway("sk_test_notreal", testWebhookSecret, "https://goen.example")
	if err != nil {
		t.Fatal(err)
	}
	h := payment.NewHandler(payment.NewStore(pool), gateway, alwaysPlacedHere{}, slog.New(slog.DiscardHandler), false)
	for _, remaining := range []time.Duration{60 * time.Minute, 30 * time.Minute, 0} {
		t.Run(remaining.String(), func(t *testing.T) {
			number, id := order(t, 100000)
			var expiry time.Time
			if remaining > 0 {
				expiry = hold(t, id, 0, remaining, "window:"+number)
			}
			for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
				ctx := i18n.WithLocale(t.Context(), locale)
				r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+number+"/pay?cancelled=1", http.NoBody)
				r.SetPathValue("number", number)
				out := httptest.NewRecorder()
				h.Page(out, r)
				if out.Code != http.StatusOK {
					t.Fatalf("Page = %d: %s", out.Code, out.Body.String())
				}
				body := out.Body.String()
				payAction := `action="/orders/` + number + `/pay"`
				if remaining == 60*time.Minute {
					deadline := fmt.Sprintf(i18n.T(ctx, i18n.KeyPayStartBy), shoptime.Minute(expiry.Add(-31*time.Minute)))
					if !strings.Contains(body, payAction) || !strings.Contains(body, deadline) {
						t.Errorf("open window omitted payment form or deadline %q", deadline)
					}
					continue
				}
				assertClosedPaymentWindow(t, body, number, ctx)
				if strings.Contains(body, i18n.T(ctx, i18n.KeyPayCancelled)) || strings.Contains(body, i18n.T(ctx, i18n.KeyPayBody)) {
					t.Error("closed window still promises payment retry or reserved stock")
				}
				post := httptest.NewRequestWithContext(ctx, http.MethodPost, "/orders/"+number+"/pay", http.NoBody)
				post.SetPathValue("number", number)
				refused := httptest.NewRecorder()
				h.Start(refused, post)
				if refused.Code != http.StatusConflict {
					t.Fatalf("expired-window POST = %d, want 409: %s", refused.Code, refused.Body.String())
				}
				assertClosedPaymentWindow(t, refused.Body.String(), number, ctx)
			}
		})
	}
}

func TestExistingPaymentSessionResumesAfterStartWindowCloses(t *testing.T) {
	number, id := order(t, 100000)
	hold(t, id, 0, 60*time.Minute, "resume-window:"+number)
	s := payment.NewStore(pool)
	sessionID := "cs_window_" + number
	if err := s.OpenPayment(t.Context(), number, sessionID, 100000); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE inventory_reservations SET expires_at = now() + interval '20 minutes' WHERE order_id = $1`, id); err != nil {
		t.Fatal(err)
	}
	const destination = "https://checkout.stripe.com/c/pay/cs_window"
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/checkout/sessions/"+sessionID {
			t.Errorf("unexpected provider call: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":%q,"object":"checkout.session","status":"open","url":%q}`, sessionID, destination)
	}))
	t.Cleanup(provider.Close)
	original := stripe.GetBackend(stripe.APIBackend)
	stripe.SetBackend(stripe.APIBackend, stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{
		URL: stripe.String(provider.URL), MaxNetworkRetries: stripe.Int64(0),
	}))
	gateway, err := payment.NewGateway("sk_test_notreal", testWebhookSecret, "https://goen.example")
	stripe.SetBackend(stripe.APIBackend, original)
	if err != nil {
		t.Fatal(err)
	}
	h := payment.NewHandler(s, gateway, alwaysPlacedHere{}, slog.New(slog.DiscardHandler), false)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		r := httptest.NewRequestWithContext(t.Context(), method, "/orders/"+number+"/pay", http.NoBody)
		r.SetPathValue("number", number)
		out := httptest.NewRecorder()
		if method == http.MethodGet {
			h.Page(out, r)
			if out.Code != http.StatusOK || !strings.Contains(out.Body.String(), `action="/orders/`+number+`/pay"`) {
				t.Fatalf("existing session lost payment form: %d %s", out.Code, out.Body.String())
			}
		} else {
			h.Start(out, r)
			if out.Code != http.StatusSeeOther || out.Header().Get("Location") != destination {
				t.Fatalf("existing session resume = %d %q", out.Code, out.Header().Get("Location"))
			}
		}
	}
}

func assertClosedPaymentWindow(t *testing.T, body, number string, ctx context.Context) {
	t.Helper()
	if strings.Contains(body, `action="/orders/`+number+`/pay"`) {
		t.Error("closed window still offers a payment form")
	}
	for _, want := range []string{
		i18n.T(ctx, i18n.KeyPayWindowClosedBody),
		`href="/orders/` + number + `"`,
		`action="/orders/` + number + `/reorder"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("closed window omitted recovery %q", want)
		}
	}
}
