//go:build integration

package cart_test

import (
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
)

type checkoutCarrier struct {
	status  invoice.CarrierStatus
	err     error
	calls   int
	barcode string
}

func (c *checkoutCarrier) CheckBarcode(_ context.Context, barcode string) (invoice.CarrierStatus, error) {
	c.calls++
	c.barcode = barcode
	return c.status, c.err
}

type carrierCheckout struct {
	handler *cart.Handler
	form    url.Values
	token   string
	variant uuid.UUID
	stock   int32
	id      uuid.UUID
}

func carrierCheckoutFor(t *testing.T, checker *checkoutCarrier) carrierCheckout {
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
	return carrierCheckout{
		handler: cart.NewHandler(s, slog.New(slog.DiscardHandler), false, testLimiter(), nil, nil, checker),
		form:    form, token: token, variant: variant, stock: stockOf(t, variant), id: id,
	}
}

func (c carrierCheckout) post(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/checkout", strings.NewReader(c.form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	//nolint:gosec // G124: the browser's own cart cookie for this checkout
	r.AddCookie(&http.Cookie{Name: "goen_cart", Value: c.token})
	w := httptest.NewRecorder()
	c.handler.PlaceOrder(w, r)
	return w
}

func (c carrierCheckout) assertUnplaced(t *testing.T) {
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

func TestKnownMissingCarrierCannotBeOverridden(t *testing.T) {
	checker := &checkoutCarrier{status: invoice.CarrierMissing}
	checkout := carrierCheckoutFor(t, checker)
	checkout.form.Set("invoice_carrier_continue", "1")
	w := checkout.post(t)
	if w.Code != http.StatusUnprocessableEntity || checker.calls != 1 || checker.barcode != "/ABC+123" {
		t.Fatalf("missing carrier: status=%d calls=%d barcode=%q", w.Code, checker.calls, checker.barcode)
	}
	for _, want := range []string{i18n.T(t.Context(), i18n.KeyCarrierMissing), `id="invoice_carrier"`, `aria-describedby="invoice_carrier-error"`, `aria-invalid="true"`, "carrier@example.com", "please ring", "松仁路 200 號", " /abc+123 ", checkout.form.Get("idempotency")} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("refusal lost %q", want)
		}
	}
	checkout.assertUnplaced(t)
}

func TestUnknownCarrierRequiresAFreshChoiceAndCheck(t *testing.T) {
	checker := &checkoutCarrier{status: invoice.CarrierUnknown, err: errors.New("provider unavailable")}
	checkout := carrierCheckoutFor(t, checker)
	w := checkout.post(t)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), i18n.T(t.Context(), i18n.KeyCarrierCheckUnavailable)) {
		t.Fatalf("unknown response = %d", w.Code)
	}
	for _, want := range []string{`name="invoice_carrier_continue"`, `value="invoice_member"`, "carrier@example.com", "please ring", " /abc+123 "} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("unknown state lost %q", want)
		}
	}
	checkout.assertUnplaced(t)
	checkout.form.Set("invoice_carrier_continue", "1")
	w = checkout.post(t)
	if w.Code != http.StatusSeeOther || checker.calls != 2 {
		t.Fatalf("explicit continue = %d after %d checks", w.Code, checker.calls)
	}
	location := w.Header().Get("Location")
	w = checkout.post(t)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != location || checker.calls != 2 {
		t.Fatal("idempotent retry rechecked or changed the placed order")
	}
}

func TestUnknownCarrierChoiceCannotBypassANewMissingVerdict(t *testing.T) {
	checker := &checkoutCarrier{status: invoice.CarrierUnknown}
	checkout := carrierCheckoutFor(t, checker)
	if w := checkout.post(t); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("first check = %d", w.Code)
	}
	checker.status = invoice.CarrierMissing
	checkout.form.Set("invoice_carrier_continue", "1")
	if w := checkout.post(t); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("known N after unknown = %d", w.Code)
	}
	if checker.calls != 2 {
		t.Fatalf("provider called %d times, want 2", checker.calls)
	}
	checkout.assertUnplaced(t)
}

func TestCarrierCheckRunsOnlyAfterLocalPlacementValidation(t *testing.T) {
	for _, mode := range []string{"malformed", "other field", "chooser", "member"} {
		t.Run(mode, func(t *testing.T) {
			checker := &checkoutCarrier{status: invoice.CarrierExists}
			checkout := carrierCheckoutFor(t, checker)
			switch mode {
			case "malformed":
				checkout.form.Set("invoice_carrier", "bad")
				checkout.form.Set("invoice_carrier_continue", "1")
			case "other field":
				checkout.form.Set("email", "bad")
			case "chooser":
				checkout.form.Set("update", "invoice")
			case "member":
				checkout.form.Set("update", "invoice_member")
			}
			w := checkout.post(t)
			if checker.calls != 0 || (w.Code != http.StatusOK && w.Code != http.StatusUnprocessableEntity) {
				t.Fatalf("%s called provider %d times: status %d", mode, checker.calls, w.Code)
			}
			checkout.assertUnplaced(t)
			if mode == "member" {
				checkout.form.Del("update")
				checkout.form.Set("invoice_type", string(invoice.PreferenceMember))
				if w := checkout.post(t); w.Code != http.StatusSeeOther || checker.calls != 0 {
					t.Fatalf("member placement = %d, calls=%d", w.Code, checker.calls)
				}
			}
		})
	}
}

func TestCarrierCheckRateLimitDoesNotCountAsProviderUnavailability(t *testing.T) {
	checker := &checkoutCarrier{status: invoice.CarrierMissing}
	checkout := carrierCheckoutFor(t, checker)
	for range 3 {
		checkout.post(t)
	}
	checker.status = invoice.CarrierUnknown
	checkout.form.Set("invoice_carrier_continue", "1")
	w := checkout.post(t)
	if w.Code != http.StatusUnprocessableEntity || checker.calls != 3 || !strings.Contains(w.Body.String(), i18n.T(t.Context(), i18n.KeyCarrierCheckLimited)) {
		t.Fatalf("rate limit = %d, provider calls=%d", w.Code, checker.calls)
	}
	checkout.assertUnplaced(t)
}

func TestAnExistingCarrierIsFrozenAfterTheCheck(t *testing.T) {
	checker := &checkoutCarrier{status: invoice.CarrierExists}
	checkout := carrierCheckoutFor(t, checker)
	w := checkout.post(t)
	if w.Code != http.StatusSeeOther || checker.calls != 1 {
		t.Fatalf("existing carrier = %d, calls=%d", w.Code, checker.calls)
	}
	number := strings.TrimSuffix(strings.TrimPrefix(w.Header().Get("Location"), "/orders/"), "/pay")
	var barcode string
	if err := pool.QueryRow(t.Context(), `SELECT ip.carrier_code FROM invoice_preferences ip JOIN orders o ON o.id = ip.order_id WHERE o.order_number = $1`, number).Scan(&barcode); err != nil {
		t.Fatal(err)
	}
	if barcode != "/ABC+123" {
		t.Fatalf("frozen carrier = %q", barcode)
	}
}
