//go:build integration

package checkout_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/checkout/checkouttest"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/order/buyer"
	"github.com/koopa0/goen/internal/order/delivery"
	"github.com/koopa0/goen/internal/payment"
)

// anotherOrderNumber is a well-formed number that differs from number in its
// last character, as a neighbouring order's would.
func anotherOrderNumber(number string) string {
	last := "0"
	if strings.HasSuffix(number, "0") {
		last = "1"
	}
	return number[:len(number)-1] + last
}

func assertPlacementGrantFailurePage(t *testing.T, body, orderNumber string) {
	t.Helper()
	if !strings.Contains(body, "/orders/find") {
		t.Fatal("placement grant failure did not offer find-order recovery")
	}
	zh := i18n.T(i18n.WithLocale(t.Context(), i18n.ZhHant), i18n.KeyPlacementGrantFailedBody)
	if !strings.Contains(body, zh) {
		t.Fatalf("placement grant failure body missing recovery copy: %q", body)
	}
	if strings.Contains(body, i18n.T(i18n.WithLocale(t.Context(), i18n.ZhHant), i18n.KeyCartUnavailable)) {
		t.Fatal("placement grant failure used the generic cart-unavailable notice")
	}
	if orderNumber != "" && strings.Contains(body, orderNumber) {
		t.Fatal("placement grant failure leaked the order number")
	}
}

func assertPayUnreachableWithoutGrant(
	t *testing.T, ctx context.Context, payH *payment.Handler, number string,
) {
	t.Helper()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+number+"/pay", http.NoBody)
	req.SetPathValue("number", number)
	res := httptest.NewRecorder()
	payH.Page(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("unrelated browser reached pay page for %s: %d, want 404", number, res.Code)
	}
}

// TestHandler_PlaceOrderGrantFailure locks the post-commit failure handling in
// PlaceOrder: when Grant fails, inserting the new order's grant or touching a
// grant the browser carries from an earlier order, the handler must not 303 to
// /pay, where the customer would get a 404. It answers 500 and keeps the
// committed order and its stock hold; a replay keeps answering 500 until the
// grant can be written, then reaches the same order's pay page exactly once,
// and an order the browser carried stays reachable throughout.
func TestHandler_PlaceOrderGrantFailure(t *testing.T) {
	for _, tt := range []struct {
		name    string
		event   string // the order_access_grants write that fails
		carried bool   // the browser carries an earlier order's grant, which placement touches
	}{
		{name: "insert", event: "INSERT"},
		{name: "carried_touch", event: "UPDATE", carried: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			s := checkout.NewStore(pool)

			var firstOrder string
			var firstCookie *http.Cookie
			if tt.carried {
				firstOrder = checkouttest.PlaceUnpaidOrderFor(t, pool, s, "carried-touch@example.com")
				firstCookie = placedCookie(t, firstOrder)
			}

			token := bearer.New()
			cartID, err := s.Carts().Create(ctx, token, uuid.NullUUID{})
			if err != nil {
				t.Fatalf("create cart: %v", err)
			}
			variant := variantOf(t, "pixelight-9-pro")
			if err := s.Carts().Add(ctx, cartID, variant, 1); err != nil {
				t.Fatalf("add: %v", err)
			}
			var shipID uuid.UUID
			if err := pool.QueryRow(ctx,
				`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
				t.Fatalf("shipping: %v", err)
			}

			addr := &delivery.Details{
				Email: "grant-" + tt.name + "-" + uuid.NewString()[:8] + "@example.com", RecipientName: "王小明",
				Phone: "0912345678", PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
			}
			form := url.Values{
				"email": {addr.Email}, "name": {addr.RecipientName}, "phone": {addr.Phone},
				"postal_code": {addr.PostalCode}, "city": {addr.City}, "district": {addr.District},
				"street":         {addr.Street},
				"shipping":       {shipID.String()},
				"checkout_quote": {checkouttest.Quote(t, s, cartID, uuid.NullUUID{}, shipID, addr, "").String()},
				"idempotency":    {checkouttest.AttemptKey("grant-" + tt.name + "-" + uuid.NewString())},
			}
			placeRequest := func() *http.Request {
				req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/checkout",
					strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				//nolint:gosec // G124: the browser's own cart cookie, read back by this handler
				req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token})
				if firstCookie != nil {
					req.AddCookie(firstCookie)
				}
				return req
			}

			name := pgx.Identifier{"test_fail_grant_" + strings.ToLower(tt.event) + "_" +
				strings.ReplaceAll(uuid.NewString(), "-", "")}.Sanitize()
			drop := fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON order_access_grants; DROP FUNCTION IF EXISTS %s()", name, name)
			if _, err := pool.Exec(ctx, fmt.Sprintf(`
				CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $body$
				BEGIN
					RAISE EXCEPTION 'simulated grant %s failure';
				END
				$body$;
				CREATE TRIGGER %s BEFORE %s ON order_access_grants
				FOR EACH ROW EXECUTE FUNCTION %s()`,
				name, strings.ToLower(tt.event), name, tt.event, name)); err != nil {
				t.Fatalf("install grant trigger: %v", err)
			}
			t.Cleanup(func() {
				cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
				defer cancel()
				if _, err := pool.Exec(cleanupCtx, drop); err != nil {
					t.Errorf("remove grant trigger: %v", err)
				}
			})

			h := newHandler(s, access.NewStore(pool, false), slog.New(slog.DiscardHandler), nil, nil)
			orders := buyer.NewHandler(buyer.NewStore(pool), access.NewStore(pool, false), slog.New(slog.DiscardHandler), testLimiter(), false)
			assertFirstOrderReachable := func(cookie *http.Cookie, when string) {
				t.Helper()
				if !tt.carried {
					return
				}
				req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+firstOrder, http.NoBody)
				req.SetPathValue("number", firstOrder)
				req.AddCookie(cookie)
				res := httptest.NewRecorder()
				orders.OrderPage(res, req)
				if res.Code != http.StatusOK {
					t.Fatalf("carried first-order access lost %s: %d, want 200", when, res.Code)
				}
			}

			res := httptest.NewRecorder()
			h.PlaceOrder(res, placeRequest())
			if res.Code == http.StatusSeeOther {
				t.Fatalf("grant failure answered 303 See Other (Location: %q), want 500 server error",
					res.Header().Get("Location"))
			}
			if res.Code != http.StatusInternalServerError {
				t.Fatalf("grant failure answered %d, want 500 Internal Server Error", res.Code)
			}
			assertPlacementGrantFailurePage(t, res.Body.String(), "")
			for _, c := range res.Result().Cookies() {
				if c.Name == "goen_placed" {
					t.Fatal("grant failure response issued a goen_placed cookie")
				}
			}

			// The order was committed and not rolled back.
			var orderCount int
			var orderNumber string
			if err := pool.QueryRow(ctx, `
				SELECT count(*), coalesce(max(o.order_number), '')
				FROM orders o
				JOIN order_private_data pd ON pd.order_id = o.id
				WHERE pd.email = $1`, addr.Email).Scan(&orderCount, &orderNumber); err != nil {
				t.Fatalf("query order after grant failure: %v", err)
			}
			if orderCount != 1 {
				t.Fatalf("order count for %s = %d, want exactly 1 committed order", addr.Email, orderCount)
			}
			assertPlacementGrantFailurePage(t, res.Body.String(), orderNumber)

			var reserved int32
			if err := pool.QueryRow(ctx, `
				SELECT coalesce(sum(ir.quantity), 0) FROM inventory_reservations ir
				JOIN orders o ON o.id = ir.order_id
				WHERE o.order_number = $1 AND ir.variant_id = $2 AND ir.state = 'held'`,
				orderNumber, variant).Scan(&reserved); err != nil {
				t.Fatalf("read stock hold after grant failure: %v", err)
			}
			if reserved != 1 {
				t.Fatalf("order %s holds %d units after grant failure, want 1", orderNumber, reserved)
			}
			assertFirstOrderReachable(firstCookie, "after the failure")

			// A replay of the prior attempt meets the same failure and answers 500 rather than 303.
			replayRes := httptest.NewRecorder()
			h.PlaceOrder(replayRes, placeRequest())
			if replayRes.Code == http.StatusSeeOther {
				t.Fatalf("prior checkout replay with grant failure answered 303 (Location: %q), want 500",
					replayRes.Header().Get("Location"))
			}
			if replayRes.Code != http.StatusInternalServerError {
				t.Fatalf("prior checkout replay answered %d, want 500", replayRes.Code)
			}

			// Once the grant can be written, the replay issues it and redirects to pay.
			if _, err := pool.Exec(ctx, drop); err != nil {
				t.Fatalf("remove grant trigger: %v", err)
			}
			recoveredRes := httptest.NewRecorder()
			h.PlaceOrder(recoveredRes, placeRequest())
			if recoveredRes.Code != http.StatusSeeOther {
				t.Fatalf("recovered replay answered %d, want 303 See Other", recoveredRes.Code)
			}
			wantLocation := "/orders/" + orderNumber + "/pay"
			if recoveredRes.Header().Get("Location") != wantLocation {
				t.Fatalf("recovered replay Location = %q, want %q", recoveredRes.Header().Get("Location"), wantLocation)
			}
			var recovered *http.Cookie
			for _, c := range recoveredRes.Result().Cookies() {
				if c.Name == "goen_placed" {
					recovered = c
					break
				}
			}
			if recovered == nil {
				t.Fatal("recovered replay set no goen_placed cookie")
			}

			payH := testPayHandler(t)
			followPayWithCookie(t, ctx, payH, orderNumber, recovered)
			assertPayUnreachableWithoutGrant(t, ctx, payH, orderNumber)

			wrong := httptest.NewRequestWithContext(ctx, http.MethodGet,
				"/orders/"+anotherOrderNumber(orderNumber)+"/pay", http.NoBody)
			wrong.SetPathValue("number", anotherOrderNumber(orderNumber))
			wrong.AddCookie(recovered)
			wrongRes := httptest.NewRecorder()
			payH.Page(wrongRes, wrong)
			if wrongRes.Code != http.StatusNotFound {
				t.Fatalf("cookie alone reached another order's pay page: %d, want 404", wrongRes.Code)
			}
			assertFirstOrderReachable(recovered, "after the recovery")

			// The order was not duplicated.
			if err := pool.QueryRow(ctx, `
				SELECT count(*)
				FROM orders o
				JOIN order_private_data pd ON pd.order_id = o.id
				WHERE pd.email = $1`, addr.Email).Scan(&orderCount); err != nil {
				t.Fatalf("query order after recovery: %v", err)
			}
			if orderCount != 1 {
				t.Fatalf("order count after recovery = %d, want exactly 1", orderCount)
			}
		})
	}
}
