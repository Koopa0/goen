//go:build integration

package cart_test

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

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/orderaccess"
)

type checkoutBarcode struct {
	status  invoice.BarcodeStatus
	err     error
	calls   int
	barcode string
}

func (c *checkoutBarcode) CheckBarcode(_ context.Context, barcode string) (invoice.BarcodeStatus, error) {
	c.calls++
	c.barcode = barcode
	return c.status, c.err
}

type barcodeCheckout struct {
	logs    *bytes.Buffer
	handler *cart.Handler
	form    url.Values
	token   string
	variant uuid.UUID
	stock   int32
	id      uuid.UUID
}

func barcodeCheckoutFor(t *testing.T, checker *checkoutBarcode) barcodeCheckout {
	t.Helper()
	s := cart.NewStore(pool)
	token, err := cart.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.Create(t.Context(), token, uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}
	variant := freshVariant(t, "carrier-"+uuid.NewString())
	if err := s.Add(t.Context(), id, variant, 1); err != nil {
		t.Fatal(err)
	}
	shipID := shipVersionFor(t, "home_delivery")
	form := url.Values{
		"email": {"carrier@example.com"}, "name": {"王小明"}, "phone": {"0912345678"},
		"postal_code": {"110"}, "city": {"台北市"}, "district": {"信義區"}, "street": {"松仁路 200 號"}, "note": {"please ring"},
		"shipping": {shipID.String()}, "idempotency": {checkoutAttemptKey(uuid.NewString())},
		"invoice_type": {string(invoice.PreferenceMobile)}, "invoice_carrier": {" /abc+123 "},
		"checkout_quote": {checkoutQuote(t, s, id, uuid.NullUUID{}, shipID, &cart.Address{PostalCode: "110"}, "").String()},
	}
	logs := &bytes.Buffer{}
	return barcodeCheckout{
		logs:    logs,
		handler: cart.NewHandler(s, orderaccess.NewStore(pool, false), slog.New(slog.NewTextHandler(logs, nil)), false, testLimiter(), nil, nil, checker),
		form:    form, token: token, variant: variant, stock: stockOf(t, variant), id: id,
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
	if attempts != 0 || lines != 1 || stockOf(t, c.variant) != c.stock {
		t.Fatalf("refusal wrote checkout state: attempts=%d cart lines=%d", attempts, lines)
	}
}

func TestKnownMissingBarcodeCannotBeOverridden(t *testing.T) {
	checker := &checkoutBarcode{status: invoice.BarcodeMissing}
	checkout := barcodeCheckoutFor(t, checker)
	w := checkout.post(t)
	if w.Code != http.StatusUnprocessableEntity || checker.calls != 1 || checker.barcode != "/ABC+123" {
		t.Fatalf("missing barcode: status=%d calls=%d barcode=%q", w.Code, checker.calls, checker.barcode)
	}
	for _, want := range []string{i18n.T(t.Context(), i18n.KeyMobileBarcodeMissing), `id="invoice_carrier"`, `aria-describedby="invoice_carrier-error"`, `aria-invalid="true"`, "carrier@example.com", "please ring", "松仁路 200 號", " /abc+123 ", checkout.form.Get("idempotency")} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("refusal lost %q", want)
		}
	}
	checkout.assertUnplaced(t)
}

func TestProviderFailureDoesNotStopThePlacement(t *testing.T) {
	for name, checker := range map[string]*checkoutBarcode{
		"provider error": {status: invoice.BarcodeUnknown, err: errors.New("provider unavailable")},
		"no verdict":     {status: invoice.BarcodeUnknown},
	} {
		t.Run(name, func(t *testing.T) {
			checkout := barcodeCheckoutFor(t, checker)
			w := checkout.post(t)
			if w.Code != http.StatusSeeOther || checker.calls != 1 {
				t.Fatalf("unavailable provider: status=%d calls=%d", w.Code, checker.calls)
			}
			if checker.err != nil {
				if got := strings.Count(checkout.logs.String(), "check mobile barcode"); got != 1 || !strings.Contains(checkout.logs.String(), "level=WARN") || strings.Contains(checkout.logs.String(), "ABC+123") {
					t.Fatalf("provider failure logged wrongly (%d entries): %s", got, checkout.logs.String())
				}
			}
			location := w.Header().Get("Location")
			w = checkout.post(t)
			if w.Code != http.StatusSeeOther || w.Header().Get("Location") != location || checker.calls != 1 {
				t.Fatal("idempotent retry rechecked or changed the placed order")
			}
		})
	}
}

func TestBarcodeCheckRunsOnlyAfterLocalPlacementValidation(t *testing.T) {
	for _, mode := range []string{"malformed", "other field", "chooser"} {
		t.Run(mode, func(t *testing.T) {
			checker := &checkoutBarcode{status: invoice.BarcodeExists}
			checkout := barcodeCheckoutFor(t, checker)
			switch mode {
			case "malformed":
				checkout.form.Set("invoice_carrier", "bad")
			case "other field":
				checkout.form.Set("email", "bad")
			case "chooser":
				checkout.form.Set("update", "invoice")
			}
			w := checkout.post(t)
			if checker.calls != 0 || (w.Code != http.StatusOK && w.Code != http.StatusUnprocessableEntity) {
				t.Fatalf("%s called provider %d times: status %d", mode, checker.calls, w.Code)
			}
			checkout.assertUnplaced(t)
		})
	}
}

func TestAnExistingBarcodeIsFrozenAfterTheCheck(t *testing.T) {
	checker := &checkoutBarcode{status: invoice.BarcodeExists}
	checkout := barcodeCheckoutFor(t, checker)
	w := checkout.post(t)
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
