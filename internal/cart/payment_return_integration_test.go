//go:build integration

package cart_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
)

func TestPaymentReturnHintExpiresWithoutChangingTheOrder(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(locale.Tag(), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), locale)
			s := cart.NewStore(pool)
			h := cart.NewHandler(s, slog.New(slog.DiscardHandler), false, testLimiter(), nil, nil)
			number := placeUnpaidOrderFor(t, s, "return@example.com")
			cookie := placedCookie(t, s, number)
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
				for _, text := range []string{`id="order-payment-processing"`, i18n.T(ctx, i18n.KeyPayProcessingTitle), i18n.T(ctx, i18n.KeyOrderStopChecking), `href="` + path + `"`} {
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
			if target != path {
				t.Fatalf("hint did not end after three checks: %q", target)
			}
			for _, suffix := range []string{"", "?paid=0", "?paid=1&confirmation=3", "?paid=1&confirmation=-1", "?paid=1&confirmation=wrong", "?paid=1&confirmation=999999999999999999999"} {
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
			paid := get(path+"?paid=1&confirmation=1", true)
			if paid.Header().Get("Refresh") != "" {
				t.Error("confirmed order keeps refreshing")
			}
			for _, text := range []string{`id="order-payment-processing"`, `id="order-unpaid"`, `action="` + path + `/cancel"`} {
				if strings.Contains(paid.Body.String(), text) {
					t.Errorf("confirmed order still renders %q", text)
				}
			}
		})
	}
}
