//go:build integration

package checkout_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/checkout/checkouttest"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/invoice/issuer"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/order/delivery"
	"github.com/koopa0/goen/internal/order/ordertest"
)

type checkoutBarcode struct {
	status  issuer.BarcodeStatus
	err     error
	calls   int
	barcode string
}

func (c *checkoutBarcode) CheckBarcode(_ context.Context, barcode string) (issuer.BarcodeStatus, error) {
	c.calls++
	c.barcode = barcode
	return c.status, c.err
}

type barcodeCheckout struct {
	logs    *bytes.Buffer
	handler *checkout.Handler
	form    url.Values
	token   string
	variant uuid.UUID
	stock   int32
	id      uuid.UUID
}

func barcodeCheckoutFor(t *testing.T, checker *checkoutBarcode) barcodeCheckout {
	t.Helper()
	s := checkout.NewStore(pool)
	token := bearer.New()
	id, err := s.Carts().Create(t.Context(), token, uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}
	variant := ordertest.FreshVariant(t, pool, "carrier-"+uuid.NewString())
	if err := s.Carts().Add(t.Context(), id, variant, 1); err != nil {
		t.Fatal(err)
	}
	shipID := shipVersionFor(t, "home_delivery")
	form := url.Values{
		"email": {"carrier@example.com"}, "name": {"王小明"}, "phone": {"0912345678"},
		"postal_code": {"110"}, "city": {"台北市"}, "district": {"信義區"}, "street": {"松仁路 200 號"}, "note": {"please ring"},
		"shipping": {shipID.String()}, "idempotency": {checkouttest.AttemptKey(uuid.NewString())},
		"invoice_type": {string(invoice.PreferenceMobile)}, "invoice_carrier": {" /abc+123 "},
		"checkout_quote": {checkouttest.Quote(t, s, id, uuid.NullUUID{}, shipID, &delivery.Details{PostalCode: "110"}, "").String()},
	}
	logs := &bytes.Buffer{}
	return barcodeCheckout{
		logs:    logs,
		handler: newHandler(s, access.NewStore(pool, false), slog.New(slog.NewTextHandler(logs, nil)), nil, checker),
		form:    form, token: token, variant: variant, stock: ordertest.StockOf(t, pool, variant), id: id,
	}
}

func (c barcodeCheckout) post(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/checkout", strings.NewReader(c.form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	//nolint:gosec // G124: the browser's own cart cookie for this checkout
	r.AddCookie(&http.Cookie{Name: "goen_cart", Value: c.token})
	w := httptest.NewRecorder()
	c.handler.PlaceOrder(w, r)
	return w
}

func (c barcodeCheckout) assertUnplaced(t *testing.T) {
	t.Helper()
	var attempts, lines int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM checkout_attempts WHERE idempotency_key = $1`, c.form.Get("idempotency")).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM cart_items WHERE cart_id = $1`, c.id).Scan(&lines); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 || lines != 1 || ordertest.StockOf(t, pool, c.variant) != c.stock {
		t.Fatalf("refusal wrote checkout state: attempts=%d cart lines=%d", attempts, lines)
	}
}

// TestHandler_PlaceOrderBarcodeMissing: PlaceOrder refuses at 422 a mobile
// barcode the provider reports missing, keeps what the shopper typed, and
// writes no checkout state.
func TestHandler_PlaceOrderBarcodeMissing(t *testing.T) {
	checker := &checkoutBarcode{status: issuer.BarcodeMissing}
	attempt := barcodeCheckoutFor(t, checker)
	w := attempt.post(t)
	if w.Code != http.StatusUnprocessableEntity || checker.calls != 1 || checker.barcode != "/ABC+123" {
		t.Fatalf("missing barcode: status=%d calls=%d barcode=%q", w.Code, checker.calls, checker.barcode)
	}
	for _, want := range []string{i18n.T(t.Context(), i18n.KeyMobileBarcodeMissing), `id="invoice_carrier"`, `aria-describedby="invoice_carrier-example invoice_carrier-error"`, `aria-invalid="true"`, "carrier@example.com", "please ring", "松仁路 200 號", " /abc+123 ", attempt.form.Get("idempotency")} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("refusal lost %q", want)
		}
	}
	attempt.assertUnplaced(t)
}

// TestHandler_PlaceOrderBarcodeProviderDown: PlaceOrder places the order when
// the barcode provider fails or gives no verdict, logging a failure once
// without the barcode, and an idempotent retry neither rechecks nor changes the
// order.
func TestHandler_PlaceOrderBarcodeProviderDown(t *testing.T) {
	for name, checker := range map[string]*checkoutBarcode{
		"provider error": {status: issuer.BarcodeUnknown, err: errors.New("provider unavailable")},
		"no verdict":     {status: issuer.BarcodeUnknown},
	} {
		t.Run(name, func(t *testing.T) {
			attempt := barcodeCheckoutFor(t, checker)
			w := attempt.post(t)
			if w.Code != http.StatusSeeOther || checker.calls != 1 {
				t.Fatalf("unavailable provider: status=%d calls=%d", w.Code, checker.calls)
			}
			if checker.err != nil {
				if got := strings.Count(attempt.logs.String(), "check mobile barcode"); got != 1 || !strings.Contains(attempt.logs.String(), "level=WARN") || strings.Contains(attempt.logs.String(), "ABC+123") {
					t.Fatalf("provider failure logged wrongly (%d entries): %s", got, attempt.logs.String())
				}
			}
			location := w.Header().Get("Location")
			w = attempt.post(t)
			if w.Code != http.StatusSeeOther || w.Header().Get("Location") != location || checker.calls != 1 {
				t.Fatal("idempotent retry rechecked or changed the placed order")
			}
		})
	}
}

// TestHandler_PlaceOrderBarcodeAfterValidation: PlaceOrder asks the barcode
// provider only after local validation passes and the form is being placed: a
// malformed barcode, another refused field or a chooser change never calls it.
func TestHandler_PlaceOrderBarcodeAfterValidation(t *testing.T) {
	for _, mode := range []string{"malformed", "other field", "chooser"} {
		t.Run(mode, func(t *testing.T) {
			checker := &checkoutBarcode{status: issuer.BarcodeExists}
			attempt := barcodeCheckoutFor(t, checker)
			switch mode {
			case "malformed":
				attempt.form.Set("invoice_carrier", "bad")
			case "other field":
				attempt.form.Set("email", "bad")
			case "chooser":
				attempt.form.Set("update", "invoice")
			}
			w := attempt.post(t)
			if checker.calls != 0 || (w.Code != http.StatusOK && w.Code != http.StatusUnprocessableEntity) {
				t.Fatalf("%s called provider %d times: status %d", mode, checker.calls, w.Code)
			}
			attempt.assertUnplaced(t)
		})
	}
}

// TestHandler_PlaceOrderBarcodeExists: PlaceOrder places the order when the
// provider says the barcode exists, and freezes the barcode in canonical form
// in the invoice preference.
func TestHandler_PlaceOrderBarcodeExists(t *testing.T) {
	checker := &checkoutBarcode{status: issuer.BarcodeExists}
	attempt := barcodeCheckoutFor(t, checker)
	w := attempt.post(t)
	if w.Code != http.StatusSeeOther || checker.calls != 1 {
		t.Fatalf("existing barcode = %d, calls=%d", w.Code, checker.calls)
	}
	number := strings.TrimSuffix(strings.TrimPrefix(w.Header().Get("Location"), "/orders/"), "/pay")
	var barcode string
	if err := pool.QueryRow(t.Context(), `SELECT ip.carrier_code FROM invoice_preferences ip JOIN orders o ON o.id = ip.order_id WHERE o.order_number = $1`, number).Scan(&barcode); err != nil {
		t.Fatal(err)
	}
	if barcode != "/ABC+123" {
		t.Fatalf("frozen barcode = %q", barcode)
	}
}
