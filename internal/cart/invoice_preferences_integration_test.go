//go:build integration

package cart_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/cart"
	invoicepkg "github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/ratelimit"
)

func TestCheckoutPersistsTheDonationPreference(t *testing.T) {
	for _, tt := range []struct{ name, kind, barcode, donation string }{
		{"donation", "donation", "", "00123"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			s := cart.NewStore(storeRolePool(t))
			cartID := newCart(t, s)
			if err := s.Add(ctx, cartID, freshVariant(t, "invoice-"+tt.name), 1); err != nil {
				t.Fatal(err)
			}
			shippingID := shipVersionFor(t, "home_delivery")
			addr := &cart.Address{Email: "buyer@example.com", Name: "Buyer", Phone: "0912345678", PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號"}
			// The type is the value the checkout radio group submits.
			inv := &cart.Invoice{Type: invoicepkg.Preference(tt.kind), MobileBarcode: tt.barcode, DonationCode: tt.donation}
			shown := checkoutQuote(t, s, cartID, uuid.NullUUID{}, shippingID, addr, "")
			number, err := s.PlaceOrder(ctx, cartID, uuid.NullUUID{}, shippingID, addr, inv, "", shown, checkoutAttemptKey("invoice-"+uuid.NewString()))
			if err != nil {
				t.Fatalf("PlaceOrder: %v", err)
			}
			var kind, barcode, donation, taxID string
			if err := pool.QueryRow(ctx, `SELECT ip.invoice_type,coalesce(ip.carrier_code,''),coalesce(ip.donation_code,''),coalesce(ip.tax_id,'') FROM invoice_preferences ip JOIN orders o ON o.id=ip.order_id WHERE o.order_number=$1`, number).Scan(&kind, &barcode, &donation, &taxID); err != nil {
				t.Fatal(err)
			}
			if kind != tt.kind || barcode != tt.barcode || donation != tt.donation || taxID != "" {
				t.Fatalf("stored preference %q/%q/%q/%q", kind, barcode, donation, taxID)
			}
		})
	}
}

func TestCheckoutDonationChoiceRoundTripWithoutScript(t *testing.T) {
	for _, tt := range []struct{ kind, field, value string }{
		{"donation", "invoice_donation_code", "00123"},
	} {
		t.Run(tt.kind, func(t *testing.T) {
			ctx := t.Context()
			s := cart.NewStore(pool)
			token, err := cart.NewToken()
			if err != nil {
				t.Fatal(err)
			}
			id, err := s.Create(ctx, token, uuid.NullUUID{})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Add(ctx, id, freshVariant(t, "invoice-form-"+strings.ReplaceAll(tt.kind, "_", "-")), 1); err != nil {
				t.Fatal(err)
			}
			h := cart.NewHandler(s, slog.New(slog.DiscardHandler), false, ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour, MaxKeys: 1000}), nil, nil)
			form := url.Values{"shipping": {shipVersionFor(t, "home_delivery").String()}, "invoice_type": {tt.kind}, tt.field: {tt.value}, "update": {"invoice"}}
			for _, refused := range []bool{false, true} {
				if refused {
					form.Del("update")
					form.Set(tt.field, "bad")
				}
				req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/checkout", strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				//nolint:gosec // G124: this is the browser cart token consumed by checkout.
				req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token})
				res := httptest.NewRecorder()
				h.PlaceOrder(res, req)
				body := res.Body.String()
				if !strings.Contains(body, `name="invoice_type" value="`+tt.kind+`" checked`) || !strings.Contains(body, `name="`+tt.field+`"`) || !strings.Contains(body, `value="`+form.Get(tt.field)+`"`) {
					t.Fatalf("preference did not survive rendering: status %d", res.Code)
				}
				if refused && (res.Code != http.StatusUnprocessableEntity || !strings.Contains(body, `id="`+tt.field+`-error"`) ||
					!strings.Contains(body, `aria-invalid="true"`)) {
					t.Fatalf("refused invoice field lacks 422/error: status %d", res.Code)
				}
			}
		})
	}
}
