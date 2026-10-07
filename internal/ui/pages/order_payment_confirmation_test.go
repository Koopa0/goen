package pages

import (
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
)

func TestOrderPaymentConfirmationEndingOffersContactInsteadOfPayment(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale i18n.Locale
		notice string
	}{
		{i18n.ZhHant, "我們還在確認付款結果，確認後會寄信通知你，請不要重複付款。"},
		{i18n.En, "We are still confirming your payment and will email you; please do not pay again."},
	} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			view := OrderView{Number: "ORD-1", Status: order.FulfillmentPending, OwedCents: 100, PaymentsEnabled: true, PaymentConfirmationPending: true, PaymentReturnHint: true, Now: time.Date(2026, time.October, 7, 12, 0, 0, 0, time.UTC), HoldUntil: time.Date(2026, time.October, 7, 13, 0, 0, 0, time.UTC)}
			html := renderComponent(t, ctx, Order(OrderMeta(ctx, view.Number), &view))
			_, pending, found := strings.Cut(html, `id="order-payment-confirmation-pending"`)
			pending, _, _ = strings.Cut(pending, "</p>")
			if !found || !strings.Contains(pending, tt.notice) || !strings.Contains(pending, `href="/contact"`) {
				t.Errorf("ended confirmation lacks its notice and contact link: %q", pending)
			}
			for _, want := range []string{`id="order-payment-confirmation-pending"`, tt.notice, `href="/contact"`} {
				if !strings.Contains(html, want) {
					t.Errorf("ended confirmation missing %q", want)
				}
			}
			for _, unwanted := range []string{`id="order-unpaid"`, `href="/orders/ORD-1/pay"`, `id="order-payment-processing"`, `action="/orders/ORD-1/cancel"`} {
				if strings.Contains(html, unwanted) {
					t.Errorf("ended confirmation renders %q", unwanted)
				}
			}
			view.PaymentConfirmationPending = false
			view.PaymentRefreshURL = "/orders/ORD-1?paid=1&confirmation=1"
			html = renderComponent(t, ctx, Order(OrderMeta(ctx, view.Number), &view))
			if !strings.Contains(html, `href="/orders/ORD-1?paid=1&amp;confirmation=done"`) {
				t.Error("stop automatic updates drops the payment return hint")
			}
			view.PaymentRefreshURL = ""
			view.PaymentReturnHint = false
			html = renderComponent(t, ctx, Order(OrderMeta(ctx, view.Number), &view))
			for _, want := range []string{`id="order-unpaid"`, `href="/orders/ORD-1/pay"`, `action="/orders/ORD-1/cancel"`} {
				if !strings.Contains(html, want) {
					t.Errorf("bare order missing %q", want)
				}
			}
		})
	}
}

func TestOrderPaymentReturnCancellationUsesTheStoredDeadline(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.October, 7, 12, 0, 0, 0, time.UTC)
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			for _, tt := range []struct {
				name    string
				until   time.Time
				refresh bool
				want    bool
			}{
				{name: "live ended", until: now.Add(time.Minute)},
				{name: "live checking", until: now.Add(time.Minute), refresh: true},
				{name: "expired ended", until: now.Add(-time.Nanosecond), want: true},
				{name: "expired checking", until: now.Add(-time.Nanosecond), refresh: true},
				{name: "no hold checking", refresh: true},
				{name: "equal deadline checking", until: now, refresh: true},
				{name: "equal deadline", until: now, want: true},
				{name: "no hold", want: true},
			} {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					ctx := i18n.WithLocale(t.Context(), locale)
					view := OrderView{Number: "ORD-1", Status: order.FulfillmentPending, OwedCents: 100, PaymentReturnHint: true, PaymentConfirmationPending: !tt.refresh, Now: now, HoldUntil: tt.until}
					if tt.refresh {
						view.PaymentRefreshURL = "/orders/ORD-1?paid=1&confirmation=1"
					}
					html := renderComponent(t, ctx, Order(OrderMeta(ctx, view.Number), &view))
					if got := strings.Contains(html, `action="/orders/ORD-1/cancel"`); got != tt.want {
						t.Errorf("cancel form present = %v, want %v", got, tt.want)
					}
					if strings.Contains(html, `href="/orders/ORD-1/pay"`) {
						t.Error("the unresolved return invited another payment")
					}
					if !tt.refresh && !strings.Contains(html, `href="/contact"`) {
						t.Error("ended confirmation lost its contact link")
					}
				})
			}
		})
	}
}
