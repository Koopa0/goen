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
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/checkout"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/order/buyer"
	"github.com/koopa0/goen/internal/order/delivery"
	"github.com/koopa0/goen/internal/payment"
)

func TestOrderConfirmationIsNotEnumerable(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	id := newCart(t, s)
	if err := s.Carts().Add(ctx, id, freshVariant(t, "confirmation-enumeration"), 1); err != nil {
		t.Fatalf("add: %v", err)
	}
	var shipID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM shipping_method_versions ORDER BY effective_at LIMIT 1`).Scan(&shipID); err != nil {
		t.Fatalf("shipping: %v", err)
	}
	addr := &delivery.Details{
		Email: "enumerate@example.com", RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	number, err := placeOrder(t, s, ctx, id, uuid.NullUUID{}, shipID, addr, "", "enum-test-1")
	if err != nil {
		t.Fatalf("place: %v", err)
	}

	h := buyer.NewHandler(buyer.NewStore(pool), access.NewStore(pool, false), slog.New(slog.DiscardHandler), testLimiter(), false)

	stranger := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+number, http.NoBody)
	stranger.SetPathValue("number", number)
	res := httptest.NewRecorder()
	h.OrderPage(res, stranger)

	if res.Code != http.StatusNotFound {
		t.Errorf("a stranger reached the confirmation page: status %d, want 404", res.Code)
	}
	if strings.Contains(res.Body.String(), "enumerate@example.com") {
		t.Error("the confirmation page leaked the customer's email to a stranger")
	}
	if strings.Contains(res.Body.String(), "松高路") {
		t.Error("the confirmation page leaked the delivery address to a stranger")
	}

	placer := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+number, http.NoBody)
	placer.SetPathValue("number", number)
	placer.AddCookie(placedCookie(t, number))
	ok := httptest.NewRecorder()
	h.OrderPage(ok, placer)

	if ok.Code != http.StatusOK {
		t.Fatalf("the browser that placed the order got %d, want 200", ok.Code)
	}
	if !strings.Contains(ok.Body.String(), number) {
		t.Error("the confirmation page does not name the order it confirms")
	}
}

// TestASecondOrderKeepsTheFirstOnesGrantAlive. The cookie is re-issued with a
// fresh MaxAge on every order while a grant is swept on its own created_at, so
// equal durations are not equal deadlines unless the second order restarts it.
func TestASecondOrderKeepsTheFirstOnesGrantAlive(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	h := buyer.NewHandler(buyer.NewStore(pool), access.NewStore(pool, false), slog.New(slog.DiscardHandler), testLimiter(), false)

	first := placeUnpaidOrderFor(t, s, "twice@example.com")
	firstCookie := placedCookie(t, first)
	second := placeUnpaidOrderFor(t, s, "twice@example.com")

	retain := access.Retain.String()
	// Just inside the window: close enough to expiry that sweep would drop the row
	// without touch, but still live when Grant restarts the clock.
	if _, err := pool.Exec(ctx, `
		UPDATE order_access_grants
		SET created_at = now() - $1::interval + interval '1 second'
		WHERE order_id = (SELECT id FROM orders WHERE order_number = $2)`,
		retain, first); err != nil {
		t.Fatalf("age the first grant: %v", err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", http.NoBody)
	r.AddCookie(firstCookie)
	if err := access.NewStore(pool, false).Grant(w, r, second); err != nil {
		t.Fatalf("remember the second order: %v", err)
	}
	var carried *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "goen_placed" {
			carried = c
		}
	}
	if carried == nil {
		t.Fatal("the second order set no cookie")
	}
	if !strings.Contains(carried.Value, firstCookie.Value) {
		t.Fatalf("the re-issued cookie dropped the first order's token; " +
			"this test would pass for the wrong reason")
	}

	// Sweep compares created_at with now(), so a grant that is only just inside
	// the window is not expired yet and the sweep would keep it even without the
	// touch. Moving both grants back 2 s puts an untouched first grant at Retain
	// + 1 s (swept) and a touched one at about 2 s (kept).
	if _, err := pool.Exec(ctx, `
		UPDATE order_access_grants SET created_at = created_at - interval '2 seconds'
		WHERE order_id IN (SELECT id FROM orders WHERE order_number = ANY($1))`,
		[]string{first, second}); err != nil {
		t.Fatalf("move both grants back 2 s: %v", err)
	}

	if err := access.NewStore(pool, false).Sweep(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+first, http.NoBody)
	req.SetPathValue("number", first)
	req.AddCookie(carried)
	rec := httptest.NewRecorder()
	h.OrderPage(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("the browser that placed this order got %d for it, want 200 — its "+
			"grant was swept while the cookie carrying it was still live, which is "+
			"the state GrantRetain's own comment says must never happen", rec.Code)
	}
}

// TestAStaleCarriedGrantStaysDeadAfterAnotherOrder. TouchOrderAccessGrants must
// not revive a grant at or past GrantRetain; placing another order with the
// stale token carried forward must leave the first order unreachable.
func TestAStaleCarriedGrantStaysDeadAfterAnotherOrder(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	h := buyer.NewHandler(buyer.NewStore(pool), access.NewStore(pool, false), slog.New(slog.DiscardHandler), testLimiter(), false)

	retain := access.Retain.String()

	for _, tc := range []struct {
		name   string
		offset string
	}{
		{name: "exact_boundary", offset: "0"},
		{name: "just_outside", offset: "-1 microsecond"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := placeUnpaidOrderFor(t, s, tc.name+"@example.com")
			firstCookie := placedCookie(t, first)

			if _, err := pool.Exec(ctx, `
				UPDATE order_access_grants
				SET created_at = now() - $1::interval + $2::interval
				WHERE order_id = (SELECT id FROM orders WHERE order_number = $3)`,
				retain, tc.offset, first); err != nil {
				t.Fatalf("age the first grant: %v", err)
			}

			second := placeUnpaidOrderFor(t, s, tc.name+"@example.com")
			w := httptest.NewRecorder()
			r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", http.NoBody)
			r.AddCookie(firstCookie)
			if err := access.NewStore(pool, false).Grant(w, r, second); err != nil {
				t.Fatalf("remember the second order: %v", err)
			}
			var carried *http.Cookie
			for _, c := range w.Result().Cookies() {
				if c.Name == "goen_placed" {
					carried = c
				}
			}
			if carried == nil {
				t.Fatal("the second order set no cookie")
			}

			req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+first, http.NoBody)
			req.SetPathValue("number", first)
			req.AddCookie(carried)
			rec := httptest.NewRecorder()
			h.OrderPage(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Errorf("a stale carried grant reached the first order: status %d, want 404", rec.Code)
			}
			if strings.Contains(rec.Body.String(), tc.name+"@example.com") {
				t.Error("a stale carried grant leaked the first order's email")
			}
		})
	}
}

// TestGrantRetainBoundsOrderAccess. Order access uses the database clock and a
// strict created_at > now() - retain. One transaction fixes now() across the
// fixture timestamps and the lookup so the equality case cannot flake.
func TestGrantRetainBoundsOrderAccess(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	number := placeUnpaidOrderFor(t, s, "retain-bound@example.com")
	cookie := placedCookie(t, number)
	token := cookie.Value
	if i := strings.IndexByte(token, '.'); i >= 0 {
		token = token[:i]
	}

	retain := access.Retain.String()
	retainParam := pgtype.Interval{
		Microseconds: int64(access.Retain / time.Microsecond), Valid: true,
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := db.New(tx)

	for _, tc := range []struct {
		name   string
		offset string
		want   bool
	}{
		{name: "just_inside", offset: "1 microsecond", want: true},
		{name: "exact_boundary", offset: "0", want: false},
		{name: "just_outside", offset: "-1 microsecond", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tx.Exec(ctx, `
				UPDATE order_access_grants
				SET created_at = now() - $1::interval + $2::interval
				WHERE order_id = (SELECT id FROM orders WHERE order_number = $3)`,
				retain, tc.offset, number); err != nil {
				t.Fatalf("set grant age: %v", err)
			}
			ok, err := q.OrderAccessibleWith(ctx, db.OrderAccessibleWithParams{
				OrderNumber: number,
				Digests:     [][]byte{bearer.Digest(token)},
				Retain:      retainParam,
			})
			if err != nil {
				t.Fatalf("OrderAccessibleWith: %v", err)
			}
			if ok != tc.want {
				t.Errorf("OrderAccessibleWith = %v, want %v", ok, tc.want)
			}
		})
	}
}

func TestAForgedPlacedCookieReachesNothing(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	h := buyer.NewHandler(buyer.NewStore(pool), access.NewStore(pool, false), slog.New(slog.DiscardHandler), testLimiter(), false)
	number := placeUnpaidOrderFor(t, s, "forged@example.com")

	// The VICTIM's own browser holds a REAL grant before the attack starts: without
	// it the access query's EXISTS is false whatever the digest comparison does, and
	// the case stays green with `g.digest = ANY(...)` replaced by `OR true`.
	victim := placedCookie(t, number)

	for _, value := range []string{
		number,
		anotherOrderNumber(number),
		number + "." + number,
	} {
		t.Run(value, func(t *testing.T) {
			r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+number, http.NoBody)
			r.SetPathValue("number", number)
			r.AddCookie(&http.Cookie{Name: "goen_placed", Value: value}) //nolint:gosec // G124: the forgery under test
			w := httptest.NewRecorder()
			h.OrderPage(w, r)

			if w.Code != http.StatusNotFound {
				t.Errorf("a forged cookie reached the order page: status %d, want 404", w.Code)
			}
			if strings.Contains(w.Body.String(), "forged@example.com") {
				t.Error("a forged cookie leaked the customer's email")
			}
		})
	}

	held := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+number, http.NoBody)
	held.SetPathValue("number", number)
	held.AddCookie(victim)
	ok := httptest.NewRecorder()
	h.OrderPage(ok, held)
	if ok.Code != http.StatusOK {
		t.Fatalf("the browser holding a real token got %d, want 200", ok.Code)
	}
}

func TestATokenReachesOnlyItsOwnOrder(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)
	h := buyer.NewHandler(buyer.NewStore(pool), access.NewStore(pool, false), slog.New(slog.DiscardHandler), testLimiter(), false)

	mine := placeUnpaidOrderFor(t, s, "mine@example.com")
	theirs := placeUnpaidOrderFor(t, s, "theirs@example.com")

	placedCookie(t, theirs)

	r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+theirs, http.NoBody)
	r.SetPathValue("number", theirs)
	r.AddCookie(placedCookie(t, mine))
	w := httptest.NewRecorder()
	h.OrderPage(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("a token for %s reached %s: status %d", mine, theirs, w.Code)
	}
	if strings.Contains(w.Body.String(), "theirs@example.com") {
		t.Error("one order's token leaked another order's email")
	}
}

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

func assertFindOrderGrantFailurePage(t *testing.T, body string) {
	t.Helper()
	if !strings.Contains(body, "/orders/find") {
		t.Fatal("find-order grant failure did not offer find-order recovery")
	}
	zh := i18n.T(i18n.WithLocale(t.Context(), i18n.ZhHant), i18n.KeyFindOrderGrantFailedBody)
	if !strings.Contains(body, zh) {
		t.Fatalf("find-order grant failure body missing recovery copy: %q", body)
	}
	if strings.Contains(body, i18n.T(i18n.WithLocale(t.Context(), i18n.ZhHant), i18n.KeyPlacementGrantFailedBody)) {
		t.Fatal("find-order grant failure reused placement recovery copy")
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
				firstOrder = placeUnpaidOrderFor(t, s, "carried-touch@example.com")
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
				"checkout_quote": {checkoutQuote(t, s, cartID, uuid.NullUUID{}, shipID, addr, "").String()},
				"idempotency":    {checkoutAttemptKey("grant-" + tt.name + "-" + uuid.NewString())},
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

			h := newHandler(s, access.NewStore(pool, false), slog.New(slog.DiscardHandler), nil)
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

// TestFindOrderGrantFailureDoesNotRedirectTo404 locks the grant failure
// handling in FindOrder: if Grant fails, FindOrder must answer 500
// rather than 303 redirecting to the order page where access would fail.
func TestFindOrderGrantFailureDoesNotRedirectTo404(t *testing.T) {
	ctx := t.Context()
	s := checkout.NewStore(pool)

	orderNumber := placeUnpaidOrderFor(t, s, "find-grant-fail@example.com")

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	fnName := pgx.Identifier{"test_fail_find_grant_" + suffix}.Sanitize()
	trgName := pgx.Identifier{"test_fail_find_grant_" + suffix}.Sanitize()
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $body$
		BEGIN
			RAISE EXCEPTION 'simulated find grant insert failure';
		END
		$body$;
		CREATE TRIGGER %s BEFORE INSERT ON order_access_grants
		FOR EACH ROW EXECUTE FUNCTION %s()`,
		fnName, trgName, fnName)); err != nil {
		t.Fatalf("install find grant trigger: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		if _, err := pool.Exec(cleanupCtx, fmt.Sprintf(
			"DROP TRIGGER IF EXISTS %s ON order_access_grants; DROP FUNCTION IF EXISTS %s()",
			trgName, fnName)); err != nil {
			t.Errorf("remove find grant trigger: %v", err)
		}
	})

	form := url.Values{
		"number": {orderNumber},
		"email":  {"find-grant-fail@example.com"},
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/orders/find",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	h := buyer.NewHandler(buyer.NewStore(pool), access.NewStore(pool, false), slog.New(slog.DiscardHandler), testLimiter(), false)

	res := httptest.NewRecorder()
	h.FindOrder(res, req)

	if res.Code == http.StatusSeeOther {
		t.Fatalf("find order with grant failure answered 303 (Location: %q), want 500",
			res.Header().Get("Location"))
	}
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("find order answered %d, want 500 Internal Server Error", res.Code)
	}
	assertFindOrderGrantFailurePage(t, res.Body.String())
	for _, c := range res.Result().Cookies() {
		if c.Name == "goen_placed" {
			t.Fatal("find order grant failure response issued a goen_placed cookie")
		}
	}

	// Remove failure trigger to simulate recovery.
	if _, err := pool.Exec(ctx, fmt.Sprintf(
		"DROP TRIGGER IF EXISTS %s ON order_access_grants; DROP FUNCTION IF EXISTS %s()",
		trgName, fnName)); err != nil {
		t.Fatalf("remove find grant trigger: %v", err)
	}

	// Retry find order.
	recoveredRes := httptest.NewRecorder()
	h.FindOrder(recoveredRes, req)

	if recoveredRes.Code != http.StatusSeeOther {
		t.Fatalf("recovered find order answered %d, want 303", recoveredRes.Code)
	}
	wantLocation := "/orders/" + url.PathEscape(orderNumber)
	if recoveredRes.Header().Get("Location") != wantLocation {
		t.Fatalf("recovered find order Location = %q, want %q", recoveredRes.Header().Get("Location"), wantLocation)
	}
	var recoveredCookie *http.Cookie
	for _, c := range recoveredRes.Result().Cookies() {
		if c.Name == "goen_placed" {
			recoveredCookie = c
			break
		}
	}
	if recoveredCookie == nil {
		t.Fatal("recovered find order set no goen_placed cookie")
	}

	orderReq := httptest.NewRequestWithContext(ctx, http.MethodGet, wantLocation, http.NoBody)
	orderReq.SetPathValue("number", orderNumber)
	orderReq.AddCookie(recoveredCookie)
	orderRes := httptest.NewRecorder()
	h.OrderPage(orderRes, orderReq)
	if orderRes.Code != http.StatusOK {
		t.Fatalf("recovered find-order cookie could not reach order page: %d", orderRes.Code)
	}

	stranger := httptest.NewRequestWithContext(ctx, http.MethodGet, wantLocation, http.NoBody)
	stranger.SetPathValue("number", orderNumber)
	strangerRes := httptest.NewRecorder()
	h.OrderPage(strangerRes, stranger)
	if strangerRes.Code != http.StatusNotFound {
		t.Fatalf("unrelated browser reached order page after find-order recovery: %d, want 404",
			strangerRes.Code)
	}

	forged := httptest.NewRequestWithContext(ctx, http.MethodGet, wantLocation, http.NoBody)
	forged.SetPathValue("number", orderNumber)
	forged.AddCookie(&http.Cookie{Name: "goen_placed", Value: orderNumber}) //nolint:gosec // G124: forgery under test
	forgedRes := httptest.NewRecorder()
	h.OrderPage(forgedRes, forged)
	if forgedRes.Code != http.StatusNotFound {
		t.Fatalf("order number alone in cookie reached order page: %d, want 404", forgedRes.Code)
	}
}
