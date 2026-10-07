//go:build integration

package cart_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
			asStore := storeRolePool(t)
			var role string
			if err := asStore.QueryRow(ctx, `SELECT current_user`).Scan(&role); err != nil || role != "store" {
				t.Fatalf("return handler role = %q, want store: %v", role, err)
			}
			s := cart.NewStore(asStore)
			h := cart.NewHandler(s, orderaccess.NewStore(asStore, false), slog.New(slog.DiscardHandler), false, testLimiter(), paymentsOn{}, nil)
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
			if strings.Contains(ended.Body.String(), `action="`+path+`/cancel"`) {
				t.Error("ended confirmation offers cancellation before the stock hold ends")
			}
			reloaded := get(target, true)
			if reloaded.Code != http.StatusOK || strings.Contains(reloaded.Body.String(), `action="`+path+`/cancel"`) {
				t.Error("reloading ended confirmation restores cancellation before the stock hold ends")
			}
			stranger = get(target, false)
			if stranger.Code != http.StatusNotFound || stranger.Header().Get("Refresh") != "" || strings.Contains(stranger.Body.String(), notice) {
				t.Errorf("unauthorized ended confirmation: status=%d refresh=%q", stranger.Code, stranger.Header().Get("Refresh"))
			}
			for _, suffix := range []string{"", "?confirmation=done", "?paid=0", "?paid=true"} {
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
			for _, suffix := range []string{"?paid=1&confirmation=3", "?paid=1&confirmation=-1", "?paid=1&confirmation=wrong", "?paid=1&confirmation=999999999999999999999"} {
				w := get(path+suffix, true)
				if w.Header().Get("Refresh") != "" || w.Code != http.StatusOK {
					t.Errorf("malformed counter %q: status=%d refresh=%q", suffix, w.Code, w.Header().Get("Refresh"))
				}
				if strings.Contains(w.Body.String(), `action="`+path+`/cancel"`) {
					t.Errorf("malformed counter %q restores cancellation despite the live return hint", suffix)
				}
				for _, text := range []string{`id="order-unpaid"`, `href="` + path + `/pay"`} {
					if !strings.Contains(w.Body.String(), text) {
						t.Errorf("malformed counter %q changed existing bare presentation %q", suffix, text)
					}
				}
			}
			view, err := s.Order(ctx, number)
			if err != nil || !view.AwaitingPayment() || !view.CanCancel() {
				t.Fatalf("return hint changed order: %+v, %v", view, err)
			}
			var heldUntil time.Time
			if holdErr := pool.QueryRow(ctx, `SELECT min(expires_at) FROM inventory_reservations WHERE order_id = (SELECT id FROM orders WHERE order_number = $1)`, number).Scan(&heldUntil); holdErr != nil {
				t.Fatalf("read canonical hold deadline: %v", holdErr)
			}
			if !view.HoldUntil.Equal(heldUntil) || !view.HoldUntil.After(view.Now) {
				t.Fatalf("stored deadline = %v, want actual future reservation deadline %v", view.HoldUntil, heldUntil)
			}
			var payments int
			if paymentCountErr := pool.QueryRow(ctx, `SELECT count(*) FROM payments WHERE order_id = (SELECT id FROM orders WHERE order_number = $1)`, number).Scan(&payments); paymentCountErr != nil || payments != 0 {
				t.Fatalf("return hint wrote a payment: count=%d err=%v", payments, paymentCountErr)
			}

			session := "cs_return_" + number
			if _, openErr := pool.Exec(ctx, `SELECT open_payment(id, $2, $3) FROM orders WHERE order_number = $1`, number, session, view.OwedCents); openErr != nil {
				t.Fatalf("open payment: %v", openErr)
			}
			if _, captureErr := pool.Exec(ctx, `SELECT capture_payment($1, $2, NULL, NULL)`, session, view.OwedCents); captureErr != nil {
				t.Fatalf("capture payment: %v", captureErr)
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

			number = placeUnpaidOrderFor(t, s, "return-expiry@example.com")
			cookie = placedCookie(t, number)
			path = "/orders/" + number
			expireHolds(t, number)
			expired, err := s.Order(ctx, number)
			if err != nil || !expired.AwaitingPayment() || !expired.CanCancel() || !expired.HoldUntil.Before(expired.Now) {
				t.Fatalf("expired but unswept order lost its factual cancellation eligibility: %+v, %v", expired, err)
			}
			for _, tt := range []struct {
				suffix  string
				cancel  bool
				refresh string
			}{
				{suffix: "?paid=1", refresh: "5; url=" + path + "?paid=1&confirmation=1"},
				{suffix: "?paid=1&confirmation=done", cancel: true},
			} {
				w := get(path+tt.suffix, true)
				if w.Code != http.StatusOK || w.Header().Get("Refresh") != tt.refresh {
					t.Errorf("expired return %q: status=%d refresh=%q, want 200 and %q", tt.suffix, w.Code, w.Header().Get("Refresh"), tt.refresh)
				}
				if got := strings.Contains(w.Body.String(), `action="`+path+`/cancel"`); got != tt.cancel {
					t.Errorf("expired return %q cancel form present = %v, want %v", tt.suffix, got, tt.cancel)
				}
			}

			number = placeUnpaidOrderFor(t, s, "return-no-hold@example.com")
			cookie = placedCookie(t, number)
			path = "/orders/" + number
			deleted, err := pool.Exec(ctx, `DELETE FROM inventory_reservations WHERE order_id = (SELECT id FROM orders WHERE order_number = $1)`, number)
			if err != nil || deleted.RowsAffected() == 0 {
				t.Fatalf("remove fixture hold rows: %v, rows=%d", err, deleted.RowsAffected())
			}
			withoutHold, err := s.Order(ctx, number)
			if err != nil || !withoutHold.HoldUntil.IsZero() || !withoutHold.CanCancel() {
				t.Fatalf("order with no hold rows = %+v, %v", withoutHold, err)
			}
			for _, tt := range []struct {
				suffix  string
				cancel  bool
				refresh string
			}{
				{suffix: "?paid=1", refresh: "5; url=" + path + "?paid=1&confirmation=1"},
				{suffix: "?paid=1&confirmation=done", cancel: true},
			} {
				w := get(path+tt.suffix, true)
				if w.Code != http.StatusOK || w.Header().Get("Refresh") != tt.refresh {
					t.Errorf("no-hold return %q: status=%d refresh=%q, want 200 and %q", tt.suffix, w.Code, w.Header().Get("Refresh"), tt.refresh)
				}
				if got := strings.Contains(w.Body.String(), `action="`+path+`/cancel"`); got != tt.cancel {
					t.Errorf("no-hold return %q cancel form present = %v, want %v", tt.suffix, got, tt.cancel)
				}
			}

			fundedID := creditFundedHeldOrder(t, freshVariant(t, "return-funded"), -time.Hour)
			if numberErr := pool.QueryRow(ctx, `SELECT order_number FROM orders WHERE id = $1`, fundedID).Scan(&number); numberErr != nil {
				t.Fatalf("read funded order number: %v", numberErr)
			}
			cookie = placedCookie(t, number)
			path = "/orders/" + number
			funded, err := s.Order(ctx, number)
			if err != nil || funded.OwedCents != 0 || funded.AwaitingPayment() || !funded.HoldUntil.After(funded.Now) {
				t.Fatalf("fully credited order funding/hold facts = %+v, %v", funded, err)
			}
			w := get(path+"?paid=1&confirmation=done", true)
			if w.Code != http.StatusOK || w.Header().Get("Refresh") != "" || !strings.Contains(w.Body.String(), `action="`+path+`/cancel"`) || strings.Contains(w.Body.String(), `id="order-payment-confirmation-pending"`) {
				t.Error("return hint changed the fully credited order's existing cancellation or payment presentation")
			}
		})
	}
}
