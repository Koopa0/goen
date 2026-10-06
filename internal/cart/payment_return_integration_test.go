//go:build integration

package cart_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/orderaccess"
)

// paymentsOn makes the handler a shop that takes payment, which is what shows
// the pay link; it closes nothing.
type paymentsOn struct{}

func (paymentsOn) ExpireSession(context.Context, string) error { return nil }

func TestPaymentReturnChecksEndWithoutInvitingAnotherPayment(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(locale.Tag(), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), locale)
			s := cart.NewStore(pool)
			h := cart.NewHandler(s, orderaccess.NewStore(pool, false), slog.New(slog.DiscardHandler), false, testLimiter(), paymentsOn{}, nil)
			number := placeUnpaidOrderFor(t, s, "return@example.com")
			cookie := placedCookie(t, number)
			path := "/orders/" + number
			get := func(target string, authorized bool) *httptest.ResponseRecorder {
				r := httptest.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
				r.SetPathValue("number", number)
				if authorized {
					r.AddCookie(cookie)
				}
				w := httptest.NewRecorder()
				h.OrderPage(w, r)
				return w
			}
			stranger := get(path+"?paid=1", false)
			if stranger.Code != http.StatusNotFound || stranger.Header().Get("Refresh") != "" {
				t.Fatalf("unauthorized return: status=%d refresh=%q", stranger.Code, stranger.Header().Get("Refresh"))
			}
			target := path + "?paid=1"
			for check := range 3 {
				w := get(target, true)
				if w.Code != http.StatusOK {
					t.Fatalf("check %d status=%d", check, w.Code)
				}
				body := w.Body.String()
				for _, text := range []string{`id="order-payment-processing"`, i18n.T(ctx, i18n.KeyPayProcessingTitle), i18n.T(ctx, i18n.KeyOrderStopChecking), `href="` + path + `?paid=1&amp;confirmation=done"`} {
					if !strings.Contains(body, text) {
						t.Errorf("check %d missing %q", check, text)
					}
				}
				for _, text := range []string{`id="order-unpaid"`, `href="` + path + `/pay"`, `action="` + path + `/cancel"`} {
					if strings.Contains(body, text) {
						t.Errorf("check %d offers %q before confirmation", check, text)
					}
				}
				refresh := w.Header().Get("Refresh")
				if !strings.HasPrefix(refresh, "5; url="+path) {
					t.Fatalf("check %d refresh=%q", check, refresh)
				}
				target = strings.TrimPrefix(refresh, "5; url=")
			}
			if want := path + "?paid=1&confirmation=done"; target != want {
				t.Fatalf("final confirmation target=%q, want %q", target, want)
			}
			ended := get(target, true)
			if ended.Code != http.StatusOK || ended.Header().Get("Refresh") != "" {
				t.Fatalf("ended confirmation: status=%d refresh=%q", ended.Code, ended.Header().Get("Refresh"))
			}
			notice := "我們還在確認付款結果，確認後會寄信通知你，請不要重複付款。"
			if locale == i18n.En {
				notice = "We are still confirming your payment and will email you; please do not pay again."
			}
			_, pending, found := strings.Cut(ended.Body.String(), `id="order-payment-confirmation-pending"`)
			pending, _, _ = strings.Cut(pending, "</p>")
			if !found || !strings.Contains(pending, notice) || !strings.Contains(pending, `href="/contact"`) {
				t.Errorf("ended confirmation lacks its pending notice and contact link: %q", pending)
			}
			for _, text := range []string{`id="order-unpaid"`, `href="` + path + `/pay"`, `id="order-payment-processing"`} {
				if strings.Contains(ended.Body.String(), text) {
					t.Errorf("ended confirmation renders %q", text)
				}
			}
			if !strings.Contains(ended.Body.String(), `action="`+path+`/cancel"`) {
				t.Error("ended confirmation removed the existing cancellation control")
			}
			stranger = get(target, false)
			if stranger.Code != http.StatusNotFound || stranger.Header().Get("Refresh") != "" || strings.Contains(stranger.Body.String(), notice) {
				t.Errorf("unauthorized ended confirmation: status=%d refresh=%q", stranger.Code, stranger.Header().Get("Refresh"))
			}
			for _, suffix := range []string{"", "?confirmation=done", "?paid=0", "?paid=1&confirmation=3", "?paid=1&confirmation=-1", "?paid=1&confirmation=wrong", "?paid=1&confirmation=999999999999999999999"} {
				w := get(path+suffix, true)
				if w.Header().Get("Refresh") != "" {
					t.Errorf("%q keeps refreshing", suffix)
				}
				for _, text := range []string{`id="order-unpaid"`, `href="` + path + `/pay"`, `action="` + path + `/cancel"`} {
					if !strings.Contains(w.Body.String(), text) {
						t.Errorf("%q did not restore %q", suffix, text)
					}
				}
			}
			view, err := s.Order(ctx, number)
			if err != nil || !view.AwaitingPayment() || !view.CanCancel() {
				t.Fatalf("return hint changed order: %+v, %v", view, err)
			}
			var payments int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM payments WHERE order_id = (SELECT id FROM orders WHERE order_number = $1)`, number).Scan(&payments); err != nil || payments != 0 {
				t.Fatalf("return hint wrote a payment: count=%d err=%v", payments, err)
			}

			session := "cs_return_" + number
			if _, err := pool.Exec(ctx, `SELECT open_payment(id, $2, $3) FROM orders WHERE order_number = $1`, number, session, view.OwedCents); err != nil {
				t.Fatalf("open payment: %v", err)
			}
			if _, err := pool.Exec(ctx, `SELECT capture_payment($1, $2, NULL, NULL)`, session, view.OwedCents); err != nil {
				t.Fatalf("capture payment: %v", err)
			}
			for _, suffix := range []string{"?paid=1&confirmation=1", "?paid=1&confirmation=done"} {
				paid := get(path+suffix, true)
				if paid.Header().Get("Refresh") != "" {
					t.Error("confirmed order keeps refreshing")
				}
				for _, text := range []string{`id="order-payment-processing"`, `id="order-unpaid"`, `id="order-payment-confirmation-pending"`, `action="` + path + `/cancel"`} {
					if strings.Contains(paid.Body.String(), text) {
						t.Errorf("confirmed order still renders %q", text)
					}
				}
			}
		})
	}
}
