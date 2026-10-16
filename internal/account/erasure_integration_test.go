//go:build integration

package account_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/delivery"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/user"
)

// Delivery PII is erased, while the minimum immutable filing snapshot remains
// with the sale so its statutory invoice/allowance lifecycle is not destroyed.
func TestEraseRemovesPersonalDataAndKeepsTheRecord(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := register(t, s, "gdpr@example.com")
	number := placeOrderFor(t, u.ID)

	var orderID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM orders WHERE order_number = $1`, number).Scan(&orderID); err != nil {
		t.Fatalf("read order: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoice_preferences
		    (order_id, invoice_type, carrier_code, customer_name, customer_email)
		VALUES ($1, 'mobile_carrier', '/ABC+123', '收件人', 'x@example.com')`, orderID); err != nil {
		t.Fatalf("invoice preference: %v", err)
	}

	if err := s.Erase(ctx, u.ID); err != nil {
		t.Fatalf("erase: %v", err)
	}

	for _, probe := range []struct {
		what  string
		query string
		arg   any
	}{
		{"the account", `SELECT count(*) FROM users WHERE email = 'gdpr@example.com'`, nil},
		{"delivery details", `SELECT count(*) FROM order_private_data
			WHERE order_id = $1 AND email IS NOT NULL`, orderID},
	} {
		var n int
		var err error
		if probe.arg == nil {
			err = pool.QueryRow(ctx, probe.query).Scan(&n)
		} else {
			err = pool.QueryRow(ctx, probe.query, probe.arg).Scan(&n)
		}
		if err != nil {
			t.Fatalf("probe %s: %v", probe.what, err)
		}
		if n != 0 {
			t.Errorf("%s survived erasure (%d row(s))", probe.what, n)
		}
	}

	var invoiceType, carrier, filingName, filingEmail string
	if err := pool.QueryRow(ctx, `
		SELECT invoice_type, carrier_code, customer_name, customer_email
		FROM invoice_preferences WHERE order_id = $1`, orderID).
		Scan(&invoiceType, &carrier, &filingName, &filingEmail); err != nil {
		t.Fatalf("read retained tax snapshot: %v", err)
	}
	if invoiceType != "mobile_carrier" || carrier != "/ABC+123" ||
		filingName != "收件人" || filingEmail != "x@example.com" {
		t.Errorf("retained tax snapshot = %q/%q/%q/%q",
			invoiceType, carrier, filingName, filingEmail)
	}

	var subtotal int64
	if err := pool.QueryRow(ctx, `
		SELECT coalesce(sum(unit_price_cents * quantity), 0) FROM order_lines WHERE order_id = $1`,
		orderID).Scan(&subtotal); err != nil {
		t.Fatalf("read order lines: %v", err)
	}
	if subtotal != 100000 {
		t.Errorf("the erased customer's order is worth %d, want 100000 — erasure altered "+
			"the financial record", subtotal)
	}
}

// TestErasureWaitsForAStoreCreditFundedReturn keeps the customer attached to an
// open claim until the shop has returned the money needed to settle it. The
// account relation is what lets compensate_return_with_credit identify the
// owner; erasing it while a requested or approved return is still owed credit
// would turn a valid return into an orphan the payout door must refuse.
func TestErasureWaitsForAStoreCreditFundedReturn(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := register(t, s, "erase-open-return-"+uuid.NewString()+"@example.com")
	userID := uuid.MustParse(u.ID)
	requestID, orderID, creditAccountID := creditFundedOpenReturnForErasure(t, u)

	if err := s.Erase(ctx, u.ID); !errors.Is(err, account.ErrOpenReturn) {
		t.Fatalf("erase with requested return = %v, want ErrOpenReturn", err)
	}
	assertOpenReturnErasureRolledBack(t, userID, orderID, creditAccountID)

	// The browser gets a recoverable account-page outcome and keeps its session;
	// an ordinary server error would hide the action the customer must wait for.
	h := account.NewHandler(s, nil, slog.New(slog.DiscardHandler), false, nil)
	form := url.Values{"confirm": {u.Email}}
	token, err := s.StartSession(ctx, u.ID, "test", "192.0.2.1")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	req := httptest.NewRequestWithContext(user.NewContext(ctx, u), http.MethodPost,
		"/account/erase", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "goen_session", Value: token}) //nolint:gosec // G124: a request cookie, not a response
	out := httptest.NewRecorder()
	h.Erase(out, req)
	if out.Code != http.StatusSeeOther || out.Header().Get("Location") != "/account?erase=return" {
		t.Fatalf("erase handler = %d Location %q, want 303 /account?erase=return",
			out.Code, out.Header().Get("Location"))
	}
	if cookies := out.Result().Cookies(); len(cookies) != 0 {
		t.Errorf("refused erasure changed %d cookie(s); the signed-in account must remain usable", len(cookies))
	}
	assertOpenReturnErasureRolledBack(t, userID, orderID, creditAccountID)

	// A decision does not settle the customer's money. Approval therefore stays
	// protected until the exact credit-funded amount has landed in the ledger.
	if _, err := pool.Exec(ctx, `
		UPDATE return_requests
		SET status = 'approved', resolution = '同意退貨', decided_at = now()
		WHERE id = $1`, requestID); err != nil {
		t.Fatalf("approve return: %v", err)
	}
	if err := s.Erase(ctx, u.ID); !errors.Is(err, account.ErrOpenReturn) {
		t.Fatalf("erase with approved unpaid return = %v, want ErrOpenReturn", err)
	}
	assertOpenReturnErasureRolledBack(t, userID, orderID, creditAccountID)

	var actorID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name)
		VALUES ('erase-return-staff-' || gen_random_uuid() || '@goen.invalid',
		        'staff', '退貨處理員')
		RETURNING id`).Scan(&actorID); err != nil {
		t.Fatalf("create return actor: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`SELECT compensate_return_with_credit($1, 100000, $2)`, requestID, actorID); err != nil {
		t.Fatalf("settle return credit: %v", err)
	}
	if err := s.Erase(ctx, u.ID); err != nil {
		t.Fatalf("erase after return credit settled: %v", err)
	}

	var userGone, creditDetached, orderDetached, deliveryErased, returnPreserved bool
	if err := pool.QueryRow(ctx, `
		SELECT
			NOT EXISTS (SELECT 1 FROM users WHERE id = $1),
			EXISTS (SELECT 1 FROM store_credit_accounts
			        WHERE id = $2 AND user_id IS NULL),
			EXISTS (SELECT 1 FROM orders WHERE id = $3 AND user_id IS NULL),
			EXISTS (SELECT 1 FROM order_private_data
			        WHERE order_id = $3 AND erased_at IS NOT NULL AND email IS NULL),
			EXISTS (SELECT 1 FROM return_requests
			        WHERE id = $4 AND status = 'approved')`,
		userID, creditAccountID, orderID, requestID).Scan(
		&userGone, &creditDetached, &orderDetached, &deliveryErased, &returnPreserved,
	); err != nil {
		t.Fatalf("read settled erasure state: %v", err)
	}
	if !userGone || !creditDetached || !orderDetached || !deliveryErased || !returnPreserved {
		t.Errorf("settled erasure state userGone/creditDetached/orderDetached/"+
			"deliveryErased/returnPreserved = %t/%t/%t/%t/%t, want all true",
			userGone, creditDetached, orderDetached, deliveryErased, returnPreserved)
	}
}

func creditFundedOpenReturnForErasure(
	t *testing.T,
	u user.User,
) (requestID, orderID, creditAccountID uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	userID := uuid.MustParse(u.ID)
	number := placeOrderFor(t, u.ID)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin credit-funded return: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var lineID uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT o.id, ol.id
		FROM orders o JOIN order_lines ol ON ol.order_id = o.id
		WHERE o.order_number = $1`, number).Scan(&orderID, &lineID); err != nil {
		t.Fatalf("read return order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		SELECT post_store_credit($1, 100000, '測試退貨額度', NULL, $2, NULL)`,
		userID, "grant:erase-return-"+orderID.String()); err != nil {
		t.Fatalf("grant return fixture credit: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT spend_store_credit($1, -100000)`, orderID); err != nil {
		t.Fatalf("fund return fixture order with credit: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'picking' WHERE id = $1`, orderID); err != nil {
		t.Fatalf("move credit-funded order to picking: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'shipped' WHERE id = $1`, orderID); err != nil {
		t.Fatalf("move credit-funded order to shipped: %v", err)
	}
	var shipmentID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (order_id, carrier, tracking_number)
		VALUES ($1, 'black_cat', 'ERASE-RETURN-' || $2)
		RETURNING id`, orderID, number).Scan(&shipmentID); err != nil {
		t.Fatalf("create return fixture shipment: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_shipment_lines
			(order_id, shipment_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 1)`, orderID, shipmentID, lineID); err != nil {
		t.Fatalf("ship return fixture line: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO return_requests (order_id, requested_by_user_id, reason)
		VALUES ($1, $2, '不合用')
		RETURNING id`, orderID, userID).Scan(&requestID); err != nil {
		t.Fatalf("open return fixture: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO return_request_lines
			(order_id, return_request_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 1)`, orderID, requestID, lineID); err != nil {
		t.Fatalf("add return fixture line: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`SELECT id FROM store_credit_accounts WHERE user_id = $1`, userID).
		Scan(&creditAccountID); err != nil {
		t.Fatalf("read return fixture credit account: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit credit-funded return: %v", err)
	}
	return requestID, orderID, creditAccountID
}

func assertOpenReturnErasureRolledBack(
	t *testing.T,
	userID, orderID, creditAccountID uuid.UUID,
) {
	t.Helper()
	var userPresent, creditAttached, orderAttached, deliveryPresent bool
	if err := pool.QueryRow(t.Context(), `
		SELECT
			EXISTS (SELECT 1 FROM users WHERE id = $1),
			EXISTS (SELECT 1 FROM store_credit_accounts
			        WHERE id = $2 AND user_id = $1),
			EXISTS (SELECT 1 FROM orders WHERE id = $3 AND user_id = $1),
			EXISTS (SELECT 1 FROM order_private_data
			        WHERE order_id = $3 AND erased_at IS NULL AND email IS NOT NULL)`,
		userID, creditAccountID, orderID).Scan(
		&userPresent, &creditAttached, &orderAttached, &deliveryPresent,
	); err != nil {
		t.Fatalf("read refused erasure state: %v", err)
	}
	if !userPresent || !creditAttached || !orderAttached || !deliveryPresent {
		t.Errorf("refused erasure state user/credit/order/delivery = %t/%t/%t/%t, want all true",
			userPresent, creditAttached, orderAttached, deliveryPresent)
	}
}

// TestUnverifiedAccountErasureDoesNotClaimTheMailbox keeps account deletion
// from becoming an address-wide eraser. Registration accepts an unproved email;
// the account therefore has no authority over guest or independently confirmed
// records that happen to use the same address.
func TestUnverifiedAccountErasureDoesNotClaimTheMailbox(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	victim := "unproved-victim-" + suffix + "@goen.invalid"
	u := register(t, s, victim)

	var variantID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM product_variants WHERE is_active ORDER BY position, id LIMIT 1`).
		Scan(&variantID); err != nil {
		t.Fatalf("read victim restock variant: %v", err)
	}
	mailKey := "unproved-victim-mail:" + suffix
	for name, seed := range map[string]struct {
		query string
		args  []any
	}{
		"newsletter subscription": {
			`INSERT INTO newsletter_subscribers (email, unsubscribe_token) VALUES ($1, $2)`,
			[]any{victim, "unproved-victim-unsubscribe-" + suffix},
		},
		"contact message": {
			`INSERT INTO contact_messages (name, email, subject, message)
			 VALUES ('Guest owner', $1, 'order', 'This belongs to the guest mailbox owner.')`,
			[]any{victim},
		},
		"guest restock request": {
			`INSERT INTO stock_notifications (variant_id, email) VALUES ($1, $2)`,
			[]any{variantID, victim},
		},
		"transactional mail": {
			`INSERT INTO outbox_messages (topic, dedupe_key, payload)
			 VALUES ($1, $2, jsonb_build_object('email', $3::text, 'order_number', 'G-VICTIM'))`,
			[]any{outbox.TopicOrderPlaced.Name(), mailKey, victim},
		},
	} {
		if _, err := pool.Exec(ctx, seed.query, seed.args...); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	t.Cleanup(func() {
		cleanupCtx := context.WithoutCancel(ctx)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM outbox_messages WHERE dedupe_key = $1`, mailKey)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM stock_notifications WHERE lower(email) = lower($1)`, victim)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM contact_messages WHERE lower(email) = lower($1)`, victim)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM newsletter_subscribers WHERE lower(email) = lower($1)`, victim)
	})

	if err := s.Erase(ctx, u.ID); err != nil {
		t.Fatalf("erase unverified account: %v", err)
	}

	for name, probe := range map[string]struct {
		query string
		args  []any
	}{
		"newsletter subscription": {
			`SELECT count(*) FROM newsletter_subscribers WHERE lower(email) = lower($1)`,
			[]any{victim},
		},
		"contact message": {
			`SELECT count(*) FROM contact_messages WHERE lower(email) = lower($1)`,
			[]any{victim},
		},
		"guest restock request": {
			`SELECT count(*) FROM stock_notifications WHERE lower(email) = lower($1)`,
			[]any{victim},
		},
		"transactional mail": {
			`SELECT count(*) FROM outbox_messages WHERE dedupe_key = $1`,
			[]any{mailKey},
		},
	} {
		var rows int
		if err := pool.QueryRow(ctx, probe.query, probe.args...).Scan(&rows); err != nil {
			t.Fatalf("count surviving %s: %v", name, err)
		}
		if rows != 1 {
			t.Errorf("unverified erasure left %d %s rows, want 1", rows, name)
		}
	}
}

// TestErasureSnapshotsBeforeConcurrentCheckout pauses erase_user after it has
// cleared every order it can currently see. A signed-in checkout starts in that
// window. Its user KEY SHARE must wait before taking the cart; erasure can then
// delete the account and cart, and checkout returns without creating fresh PII.
func TestErasureSnapshotsBeforeConcurrentCheckout(t *testing.T) {
	ctx := t.Context()
	u := register(t, account.NewStore(pool), "erase-checkout-"+uuid.NewString()+"@example.com")
	userID := uuid.MustParse(u.ID)
	owner := uuid.NullUUID{UUID: userID, Valid: true}

	var variantID, cartID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM product_variants WHERE is_active ORDER BY position, id LIMIT 1`).
		Scan(&variantID); err != nil {
		t.Fatalf("checkout variant: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO carts (digest, user_id) VALUES ($1, $2) RETURNING id`,
		bearer.Digest("erase-checkout-cart-"+u.ID), userID).Scan(&cartID); err != nil {
		t.Fatalf("create erase-race cart: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO cart_items (cart_id, variant_id, quantity) VALUES ($1, $2, 1)`,
		cartID, variantID); err != nil {
		t.Fatalf("fill erase-race cart: %v", err)
	}

	var shippingID uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT v.id
		FROM shipping_method_versions v
		JOIN shipping_methods m ON m.id = v.method_id
		WHERE m.code = 'home_delivery'
		ORDER BY v.effective_at DESC, v.id DESC LIMIT 1`).Scan(&shippingID); err != nil {
		t.Fatalf("read home-delivery version: %v", err)
	}
	addr := &delivery.Details{
		Email: u.Email, RecipientName: "王小明", Phone: "0912345678",
		PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
	}
	shown := accountCheckoutQuote(t, cart.NewStore(pool), cartID, owner, shippingID, addr.PostalCode)

	var notificationID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO stock_notifications (variant_id, user_id, email)
		VALUES ($1, $2, $3) RETURNING id`, variantID, userID, u.Email).
		Scan(&notificationID); err != nil {
		t.Fatalf("create erasure blocker row: %v", err)
	}
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin erasure blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := blocker.Exec(ctx,
		`SELECT 1 FROM stock_notifications WHERE id = $1 FOR UPDATE`, notificationID); err != nil {
		t.Fatalf("lock erasure blocker row: %v", err)
	}

	suffix := uuid.NewString()[:8]
	eraseName, checkoutName := "erase-before-checkout-"+suffix, "checkout-behind-erase-"+suffix
	eraseStore := account.NewStore(accountStorePool(t, eraseName))
	checkoutStore := cart.NewStore(accountStorePool(t, checkoutName))
	eraseDone, checkoutDone := make(chan error, 1), make(chan error, 1)
	go func() { eraseDone <- eraseStore.Erase(ctx, u.ID) }()
	waitForAccountLock(t, eraseName, eraseDone)
	go func() {
		_, placeErr := checkoutStore.PlaceOrder(
			ctx, cartID, owner, shippingID, addr, nil, "", shown,
			checkoutAttemptKey("erase-checkout-"+suffix),
		)
		checkoutDone <- placeErr
	}()
	waitForAccountLock(t, checkoutName, checkoutDone)

	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release erasure: %v", err)
	}
	if err := operationResult(t, eraseDone); err != nil {
		t.Fatalf("erase in checkout interleaving: %v", err)
	}
	if err := operationResult(t, checkoutDone); !errors.Is(err, cart.ErrNotFound) {
		t.Fatalf("checkout after erasure = %v, want cart.ErrNotFound", err)
	}

	var users, orders, privateRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id = $1`, userID).
		Scan(&users); err != nil {
		t.Fatalf("count erased user: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM orders WHERE user_id = $1`, userID).
		Scan(&orders); err != nil {
		t.Fatalf("count concurrent checkout orders: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM order_private_data WHERE lower(email) = lower($1)`, u.Email).
		Scan(&privateRows); err != nil {
		t.Fatalf("count PII written after erasure: %v", err)
	}
	if users != 0 || orders != 0 || privateRows != 0 {
		t.Errorf("erasure survivors user/order/private = %d/%d/%d, want 0/0/0",
			users, orders, privateRows)
	}
}

// TestConcurrentAdminErasureKeepsOneAdmin holds erase_user's global decision
// lock so both requests are known to have started. After release, one may erase
// itself; the other must count the committed survivor state and receive the
// named last-admin refusal rather than letting both snapshots observe two.
func TestConcurrentAdminErasureKeepsOneAdmin(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	first := register(t, s, "erase-admin-a-"+uuid.NewString()+"@example.com")
	second := register(t, s, "erase-admin-b-"+uuid.NewString()+"@example.com")
	adminIDs := []uuid.UUID{uuid.MustParse(first.ID), uuid.MustParse(second.ID)}
	t.Cleanup(func() {
		// Keep a suite sentinel before removing the surviving fixture: the schema
		// invariant deliberately gives even the owner no transition back to zero.
		cleanupCtx := context.WithoutCancel(ctx)
		_, _ = pool.Exec(cleanupCtx, `
			INSERT INTO users (email, role, full_name)
			VALUES ('account-suite-admin@goen.invalid', 'admin', 'Account suite sentinel')
			ON CONFLICT (lower(email)) DO UPDATE SET role = 'admin'`)
		_, _ = pool.Exec(cleanupCtx,
			`UPDATE users SET role = 'customer' WHERE id = ANY($1::uuid[])`, adminIDs)
		_, _ = pool.Exec(cleanupCtx,
			`DELETE FROM users WHERE id = ANY($1::uuid[])`, adminIDs)
	})
	if _, err := pool.Exec(ctx,
		`UPDATE users SET role = 'admin' WHERE id = ANY($1::uuid[])`,
		adminIDs); err != nil {
		t.Fatalf("promote erasure-race admins: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE users SET role = 'customer'
		WHERE role = 'admin' AND id <> ALL($1::uuid[])`, adminIDs); err != nil {
		t.Fatalf("leave exactly the erasure-race admins: %v", err)
	}
	var before int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE role = 'admin'`).Scan(&before); err != nil {
		t.Fatalf("count admins before erasure race: %v", err)
	}
	if before != 2 {
		t.Fatalf("erasure-race fixture has %d admins, want exactly 2", before)
	}

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin admin-erasure blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := blocker.Exec(ctx, `SELECT lock_admin_roster()`); err != nil {
		t.Fatalf("lock admin-erasure decision: %v", err)
	}

	suffix := uuid.NewString()[:8]
	firstName, secondName := "erase-admin-a-"+suffix, "erase-admin-b-"+suffix
	firstStore := account.NewStore(accountStorePool(t, firstName))
	secondStore := account.NewStore(accountStorePool(t, secondName))
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() { firstDone <- firstStore.Erase(ctx, first.ID) }()
	go func() { secondDone <- secondStore.Erase(ctx, second.ID) }()
	waitForAccountLock(t, firstName, firstDone)
	waitForAccountLock(t, secondName, secondDone)

	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release admin-erasure decision: %v", err)
	}
	firstErr, secondErr := operationResult(t, firstDone), operationResult(t, secondDone)
	refused := func(err error) bool {
		pgErr, ok := errors.AsType[*pgconn.PgError](err)
		return ok && pgErr.ConstraintName == "erase_user_keeps_one_admin"
	}
	oneSucceededAndOneWasRefused := firstErr == nil && refused(secondErr) ||
		secondErr == nil && refused(firstErr)
	if !oneSucceededAndOneWasRefused {
		t.Fatalf("concurrent admin erasures = %v / %v, want one success and one named refusal",
			firstErr, secondErr)
	}

	var admins int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE role = 'admin'`).Scan(&admins); err != nil {
		t.Fatalf("count admins after erasure race: %v", err)
	}
	if admins != 1 {
		t.Errorf("concurrent erasure left %d admins, want 1", admins)
	}
}

// TestTheLastAdminIsToldWhyErasureWasRefused: erase_user refuses the only
// administrator by name, and the page must say so and keep the account usable
// rather than answer a server error for a request the shop decided to refuse.
func TestTheLastAdminIsToldWhyErasureWasRefused(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := register(t, s, "erase-last-admin-"+uuid.NewString()+"@example.com")
	id := uuid.MustParse(u.ID)
	t.Cleanup(func() {
		// The schema gives even the owner no way back to zero administrators, so a
		// sentinel takes the role before the fixture gives it up.
		cleanupCtx := context.WithoutCancel(ctx)
		_, _ = pool.Exec(cleanupCtx, `
			INSERT INTO users (email, role, full_name)
			VALUES ('account-suite-admin@goen.invalid', 'admin', 'Account suite sentinel')
			ON CONFLICT (lower(email)) DO UPDATE SET role = 'admin'`)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1`, id)
	})
	if _, err := pool.Exec(ctx, `UPDATE users SET role = 'admin' WHERE id = $1`, id); err != nil {
		t.Fatalf("promote the fixture: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE users SET role = 'customer' WHERE role = 'admin' AND id <> $1`, id); err != nil {
		t.Fatalf("leave the fixture as the only administrator: %v", err)
	}

	if err := s.Erase(ctx, u.ID); !errors.Is(err, account.ErrLastAdmin) {
		t.Fatalf("erasing the last administrator = %v, want ErrLastAdmin", err)
	}

	h := account.NewHandler(s, nil, slog.New(slog.DiscardHandler), false, nil)
	token, err := s.StartSession(ctx, u.ID, "test", "192.0.2.1")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	form := url.Values{"confirm": {u.Email}}
	req := httptest.NewRequestWithContext(user.NewContext(ctx, u), http.MethodPost,
		"/account/erase", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "goen_session", Value: token}) //nolint:gosec // G124: a request cookie, not a response
	out := httptest.NewRecorder()
	h.Erase(out, req)
	if out.Code != http.StatusSeeOther || out.Header().Get("Location") != "/account?erase=admin" {
		t.Fatalf("erase handler = %d Location %q, want 303 /account?erase=admin",
			out.Code, out.Header().Get("Location"))
	}
	if cookies := out.Result().Cookies(); len(cookies) != 0 {
		t.Errorf("refused erasure changed %d cookie(s); the signed-in account must remain usable", len(cookies))
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id = $1 AND role = 'admin'`, id).
		Scan(&left); err != nil {
		t.Fatalf("read the administrator: %v", err)
	}
	if left != 1 {
		t.Error("the refused erasure removed the only administrator")
	}
}

// TestResetIssueAndErasureCannotSplitTokenFromMessage pauses reset issuance at
// its outbox insert, after it has locked the account and inserted the token in
// the same transaction. Erasure must wait, then purge both committed records;
// if erasure wins before the account lock, reset instead becomes a quiet no-op.
func TestResetIssueAndErasureCannotSplitTokenFromMessage(t *testing.T) {
	ctx := t.Context()
	email := "reset-erase-" + uuid.NewString() + "@example.com"
	u := register(t, account.NewStore(pool), email)

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := pgx.Identifier{"test_reset_outbox_barrier_" + suffix}.Sanitize()
	triggerName := pgx.Identifier{"test_reset_outbox_trigger_" + suffix}.Sanitize()
	const barrierKey int64 = 8_112_233_445_566_778
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $body$
		BEGIN
			IF NEW.topic = 'account.password_reset' THEN
				PERFORM pg_advisory_xact_lock(%d);
			END IF;
			RETURN NEW;
		END
		$body$`, functionName, barrierKey)); err != nil {
		t.Fatalf("create reset outbox barrier: %v", err)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE TRIGGER %s BEFORE INSERT ON outbox_messages
		FOR EACH ROW EXECUTE FUNCTION %s()`, triggerName, functionName)); err != nil {
		t.Fatalf("create reset outbox trigger: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.WithoutCancel(ctx)
		_, _ = pool.Exec(cleanupCtx, fmt.Sprintf(
			`DROP TRIGGER IF EXISTS %s ON outbox_messages`, triggerName))
		_, _ = pool.Exec(cleanupCtx, fmt.Sprintf(
			`DROP FUNCTION IF EXISTS %s()`, functionName))
	})

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin reset outbox blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := blocker.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, barrierKey); err != nil {
		t.Fatalf("lock reset outbox barrier: %v", err)
	}

	resetName, eraseName := "reset-issue-"+suffix[:8], "reset-erase-"+suffix[:8]
	resetStore := account.NewStore(accountStorePool(t, resetName))
	eraseStore := account.NewStore(accountStorePool(t, eraseName))
	resetDone, eraseDone := make(chan error, 1), make(chan error, 1)
	go func() { resetDone <- account.BeginReset(context.WithoutCancel(ctx), resetStore, email) }()
	waitForAccountLock(t, resetName, resetDone)
	go func() { eraseDone <- eraseStore.Erase(context.WithoutCancel(ctx), u.ID) }()
	waitForAccountLock(t, eraseName, eraseDone)

	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release reset outbox barrier: %v", err)
	}
	if err := operationResult(t, resetDone); err != nil {
		t.Fatalf("issue reset: %v", err)
	}
	if err := operationResult(t, eraseDone); err != nil {
		t.Fatalf("erase reset account: %v", err)
	}

	var users, tokens, messages int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id = $1`,
		uuid.MustParse(u.ID)).Scan(&users); err != nil {
		t.Fatalf("count reset user: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM password_reset_tokens WHERE user_id = $1`,
		uuid.MustParse(u.ID)).Scan(&tokens); err != nil {
		t.Fatalf("count reset tokens: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages
		WHERE topic = $1 AND lower(payload->>'email') = lower($2)`,
		outbox.TopicPasswordReset.Name(), email).Scan(&messages); err != nil {
		t.Fatalf("count reset messages: %v", err)
	}
	if users != 0 || tokens != 0 || messages != 0 {
		t.Errorf("reset/erase survivors user/token/message = %d/%d/%d, want 0/0/0",
			users, tokens, messages)
	}
}

// TestResetCompletionAndErasureUseUserBeforeToken pauses the token spend after
// completion has locked the account. Erasure must wait on that account instead
// of holding it while completion holds the token, the former ABBA cycle.
func TestResetCompletionAndErasureUseUserBeforeToken(t *testing.T) {
	ctx := t.Context()
	email := "complete-erase-" + uuid.NewString() + "@example.com"
	u := register(t, account.NewStore(pool), email)
	token := beginReset(t, account.NewStore(pool), email)
	digest := sha256.Sum256([]byte(token))

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := pgx.Identifier{"test_reset_spend_barrier_" + suffix}.Sanitize()
	triggerName := pgx.Identifier{"test_reset_spend_trigger_" + suffix}.Sanitize()
	const barrierKey int64 = 8_112_233_445_566_779
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $body$
		BEGIN
			IF OLD.digest = decode('%x', 'hex') THEN
				PERFORM pg_advisory_xact_lock(%d);
			END IF;
			RETURN NEW;
		END
		$body$`, functionName, digest, barrierKey)); err != nil {
		t.Fatalf("create reset spend barrier: %v", err)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE TRIGGER %s BEFORE UPDATE OF used_at ON password_reset_tokens
		FOR EACH ROW EXECUTE FUNCTION %s()`, triggerName, functionName)); err != nil {
		t.Fatalf("create reset spend trigger: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.WithoutCancel(ctx)
		_, _ = pool.Exec(cleanupCtx, fmt.Sprintf(
			`DROP TRIGGER IF EXISTS %s ON password_reset_tokens`, triggerName))
		_, _ = pool.Exec(cleanupCtx, fmt.Sprintf(
			`DROP FUNCTION IF EXISTS %s()`, functionName))
	})

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin reset spend blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := blocker.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, barrierKey); err != nil {
		t.Fatalf("lock reset spend barrier: %v", err)
	}

	completeName, eraseName := "reset-complete-"+suffix[:8], "complete-erase-"+suffix[:8]
	completeStore := account.NewStore(accountStorePool(t, completeName))
	eraseStore := account.NewStore(accountStorePool(t, eraseName))
	completeDone, eraseDone := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := completeStore.CompleteReset(
			context.WithoutCancel(ctx), token, "completed before erasure")
		completeDone <- err
	}()
	waitForAccountLock(t, completeName, completeDone)
	go func() { eraseDone <- eraseStore.Erase(context.WithoutCancel(ctx), u.ID) }()
	waitForAccountLock(t, eraseName, eraseDone)

	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release reset spend barrier: %v", err)
	}
	if err := operationResult(t, completeDone); err != nil {
		t.Fatalf("complete reset: %v", err)
	}
	if err := operationResult(t, eraseDone); err != nil {
		t.Fatalf("erase reset account: %v", err)
	}

	var users, tokens int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id = $1`,
		uuid.MustParse(u.ID)).Scan(&users); err != nil {
		t.Fatalf("count reset user: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM password_reset_tokens WHERE user_id = $1`,
		uuid.MustParse(u.ID)).Scan(&tokens); err != nil {
		t.Fatalf("count reset tokens: %v", err)
	}
	if users != 0 || tokens != 0 {
		t.Errorf("complete/erase survivors user/token = %d/%d, want 0/0", users, tokens)
	}
}

func TestErasurePurgesOnlyItsPendingVerificationMessage(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := register(t, s, "verification-erase-old-"+uuid.NewString()+"@goen.invalid")
	pending := "verification-erase-new-" + uuid.NewString() + "@goen.invalid"
	if err := account.RequestVerification(ctx, s, u.ID, pending); err != nil {
		t.Fatalf("request address change: %v", err)
	}
	verificationDedupe := func(userID string) string {
		t.Helper()
		var key string
		if err := pool.QueryRow(ctx, `
			SELECT 'verify:' || encode(digest, 'hex')
			FROM email_verifications WHERE user_id = $1`, userID).Scan(&key); err != nil {
			t.Fatalf("read verification message identity: %v", err)
		}
		return key
	}
	erasedDedupe := verificationDedupe(u.ID)

	// A pending address has not been proved. Another account may have asked to
	// prove the same mailbox, and unrelated transactional mail may already target
	// it; neither belongs to the account being erased.
	other := register(t, s, "verification-erase-other-"+uuid.NewString()+"@goen.invalid")
	if err := account.RequestVerification(ctx, s, other.ID, pending); err != nil {
		t.Fatalf("request the same pending address for another account: %v", err)
	}
	otherDedupe := verificationDedupe(other.ID)
	transactionalDedupe := "verification-erase-victim-mail:" + uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO outbox_messages (topic, dedupe_key, payload)
		VALUES ($1, $2, jsonb_build_object('email', $3::text, 'order_number', 'G-VICTIM'))`,
		outbox.TopicOrderPlaced.Name(), transactionalDedupe, pending); err != nil {
		t.Fatalf("seed victim transactional mail: %v", err)
	}
	if err := s.Erase(ctx, u.ID); err != nil {
		t.Fatalf("erase account: %v", err)
	}

	var erasedMessages int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages
		WHERE dedupe_key = $1`, erasedDedupe).Scan(&erasedMessages); err != nil {
		t.Fatalf("count erased account's verification messages: %v", err)
	}
	if erasedMessages != 0 {
		t.Errorf("%d messages still carry the erased account's verification identity", erasedMessages)
	}

	var victimMessages int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages
		WHERE dedupe_key IN ($1, $2)`, otherDedupe, transactionalDedupe).Scan(&victimMessages); err != nil {
		t.Fatalf("count other owner's pending-address messages: %v", err)
	}
	if victimMessages != 2 {
		t.Errorf("erasure retained %d/2 messages that target an unproved victim mailbox", victimMessages)
	}
}

// TestErasureMatchesOutboxRecipientsExactly protects both halves of the
// recipient predicate. Percent and underscore are legal mailbox characters but
// SQL pattern wildcards, and an address in a non-recipient field does not make
// that message the erased customer's mail.
func TestErasureMatchesOutboxRecipientsExactly(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	current := "erase_%" + suffix + "@goen.invalid"
	currentLookalike := "erase-looks-like-" + suffix + "@goen.invalid"
	pending := "pending_%" + suffix + "@goen.invalid"
	pendingLookalike := "pending-looks-like-" + suffix + "@goen.invalid"
	u := register(t, s, current)
	currentToken := requestVerification(t, s, u.ID, current)
	if _, err := s.CompleteRegistration(ctx, currentToken, "a sufficiently long password"); err != nil {
		t.Fatalf("prove patterned current address: %v", err)
	}
	if err := account.RequestVerification(ctx, s, u.ID, pending); err != nil {
		t.Fatalf("request patterned pending address: %v", err)
	}

	dedupePrefix := "erase-recipient-exact:" + suffix + ":"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.WithoutCancel(ctx),
			`DELETE FROM outbox_messages WHERE dedupe_key LIKE $1`, dedupePrefix+"%")
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO outbox_messages (topic, dedupe_key, payload) VALUES
			('test.erase-current', $1,
			 jsonb_build_object('email', $2::text, 'token', 'reset-link')),
			('test.erase-current-lookalike', $3,
			 jsonb_build_object('email', $4::text, 'name', $2::text)),
			('test.erase-pending-lookalike', $5,
			 jsonb_build_object('email', $6::text))`,
		dedupePrefix+"current", current,
		dedupePrefix+"current-lookalike", currentLookalike,
		dedupePrefix+"pending-lookalike", pendingLookalike,
	); err != nil {
		t.Fatalf("seed exact-recipient messages: %v", err)
	}

	if err := s.Erase(ctx, u.ID); err != nil {
		t.Fatalf("erase patterned address: %v", err)
	}

	var erasedRecipients int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages
		WHERE lower(coalesce(payload ->> 'email', '')) =
		      ANY(ARRAY[lower($1), lower($2)])`, current, pending).Scan(&erasedRecipients); err != nil {
		t.Fatalf("count erased recipients: %v", err)
	}
	if erasedRecipients != 0 {
		t.Errorf("%d outbox messages still name an erased current or pending recipient", erasedRecipients)
	}

	var lookalikes int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages
		WHERE dedupe_key IN ($1, $2)`,
		dedupePrefix+"current-lookalike", dedupePrefix+"pending-lookalike").Scan(&lookalikes); err != nil {
		t.Fatalf("count lookalike recipients: %v", err)
	}
	if lookalikes != 2 {
		t.Errorf("erasure retained %d/2 lookalike-recipient messages; %% and _ must be literal", lookalikes)
	}
}

// TestVerificationAndErasureUseUserBeforeToken pauses token deletion after
// confirmation has locked the user. Erasure must wait on the user instead of
// holding it while confirmation holds the verification row.
func TestVerificationAndErasureUseUserBeforeToken(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	u := register(t, s, "verify-race-old-"+uuid.NewString()+"@goen.invalid")
	next := "verify-race-new-" + uuid.NewString() + "@goen.invalid"
	token := requestVerification(t, s, u.ID, next)
	digest := bearer.Digest(token)

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := pgx.Identifier{"test_verification_spend_barrier_" + suffix}.Sanitize()
	triggerName := pgx.Identifier{"test_verification_spend_trigger_" + suffix}.Sanitize()
	const barrierKey int64 = 8_112_233_445_566_780
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $body$
		BEGIN
			IF OLD.digest = decode('%x', 'hex') THEN
				PERFORM pg_advisory_xact_lock(%d);
			END IF;
			RETURN OLD;
		END
		$body$`, functionName, digest, barrierKey)); err != nil {
		t.Fatalf("create verification spend barrier: %v", err)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE TRIGGER %s BEFORE DELETE ON email_verifications
		FOR EACH ROW EXECUTE FUNCTION %s()`, triggerName, functionName)); err != nil {
		t.Fatalf("create verification spend trigger: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.WithoutCancel(ctx)
		_, _ = pool.Exec(cleanupCtx, fmt.Sprintf(
			`DROP TRIGGER IF EXISTS %s ON email_verifications`, triggerName))
		_, _ = pool.Exec(cleanupCtx, fmt.Sprintf(
			`DROP FUNCTION IF EXISTS %s()`, functionName))
	})

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin verification spend blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := blocker.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, barrierKey); err != nil {
		t.Fatalf("lock verification spend barrier: %v", err)
	}

	confirmName, eraseName := "verify-confirm-"+suffix[:8], "verify-erase-"+suffix[:8]
	confirmStore := account.NewStore(accountStorePool(t, confirmName))
	eraseStore := account.NewStore(accountStorePool(t, eraseName))
	confirmDone, eraseDone := make(chan error, 1), make(chan error, 1)
	go func() {
		_, confirmErr := confirmStore.ConfirmVerification(context.WithoutCancel(ctx), token, u.ID)
		confirmDone <- confirmErr
	}()
	waitForAccountLock(t, confirmName, confirmDone)
	go func() { eraseDone <- eraseStore.Erase(context.WithoutCancel(ctx), u.ID) }()
	waitForAccountLock(t, eraseName, eraseDone)

	if err := blocker.Commit(ctx); err != nil {
		t.Fatalf("release verification spend barrier: %v", err)
	}
	if err := operationResult(t, confirmDone); err != nil {
		t.Fatalf("confirm verification: %v", err)
	}
	if err := operationResult(t, eraseDone); err != nil {
		t.Fatalf("erase verified account: %v", err)
	}

	var users, verifications int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id = $1`,
		uuid.MustParse(u.ID)).Scan(&users); err != nil {
		t.Fatalf("count verification user: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM email_verifications WHERE user_id = $1`,
		uuid.MustParse(u.ID)).Scan(&verifications); err != nil {
		t.Fatalf("count verification rows: %v", err)
	}
	if users != 0 || verifications != 0 {
		t.Errorf("verification/erase survivors user/token = %d/%d, want 0/0",
			users, verifications)
	}
}

// user_identities cascades on the user, so erase_user reaches it without naming it.
func TestErasingAnAccountTakesItsIdentities(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	email := "oauth-erase-" + uuid.NewString()[:8] + "@example.com"

	u, err := s.SignInWithGoogle(ctx, account.Identity{
		Subject: "google-sub-" + uuid.NewString()[:8],
		Email:   email, EmailVerified: true, Name: "谷歌使用者",
	})
	if err != nil {
		t.Fatalf("SignInWithGoogle: %v", err)
	}
	if err := s.Erase(ctx, u.ID); err != nil {
		t.Fatalf("Erase: %v", err)
	}

	var identities int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM user_identities WHERE user_id = $1`, u.ID).Scan(&identities); err != nil {
		t.Fatalf("count identities: %v", err)
	}
	if identities != 0 {
		t.Errorf("%d identities survived erasure — the same Google account could "+
			"sign back into an account that no longer exists", identities)
	}
}

// TestErasureNeedsARecentSignIn keeps a stolen or unattended session from
// erasing the account: only a session created within EraseSignInWindow may.
func TestErasureNeedsARecentSignIn(t *testing.T) {
	ctx := t.Context()
	s := account.NewStore(pool)
	h := account.NewHandler(s, nil, slog.New(slog.DiscardHandler), false, nil)
	erase := func(u user.User, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(user.NewContext(ctx, u), http.MethodPost,
			"/account/erase", strings.NewReader(url.Values{"confirm": {u.Email}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "goen_session", Value: token}) //nolint:gosec // G124: a request cookie, not a response
		out := httptest.NewRecorder()
		h.Erase(out, req)
		return out
	}
	exists := func(u user.User) bool {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id = $1`, u.ID).Scan(&n); err != nil {
			t.Fatalf("count user: %v", err)
		}
		return n == 1
	}

	stale := register(t, s, "erase-stale-"+uuid.NewString()+"@example.com")
	staleToken, err := s.StartSession(ctx, stale.ID, "test", "192.0.2.1")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if _, err = pool.Exec(ctx, `
		UPDATE sessions
		SET created_at = now() - $2::interval - interval '1 minute',
		    expires_at = now() + interval '1 day'
		WHERE digest = $1`, bearer.Digest(staleToken),
		account.EraseSignInWindow.String()); err != nil {
		t.Fatalf("age session: %v", err)
	}
	out := erase(stale, staleToken)
	if out.Code != http.StatusSeeOther || out.Header().Get("Location") != "/signin?next=%2Faccount&reauth=erase" {
		t.Fatalf("stale-session erase = %d Location %q, want 303 /signin?next=%%2Faccount&reauth=erase",
			out.Code, out.Header().Get("Location"))
	}
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		lctx := i18n.WithLocale(ctx, locale)
		page := httptest.NewRecorder()
		request := httptest.NewRequestWithContext(lctx, http.MethodGet,
			out.Header().Get("Location"), http.NoBody)
		for _, cookie := range out.Result().Cookies() {
			request.AddCookie(cookie)
		}
		h.SignInPage(page, request)
		assertSignInPrefill(t, page.Body.String(), stale.Email, true)
		if want := html.EscapeString(i18n.T(lctx, i18n.KeyEraseNeedsRecentSignIn)); !strings.Contains(page.Body.String(), want) {
			t.Errorf("%s sign-in page after the redirect lacks %q", locale, want)
		}
	}
	if !exists(stale) {
		t.Fatal("a session older than the window erased the account")
	}
	if _, err = s.SessionUser(ctx, staleToken); !errors.Is(err, account.ErrNotFound) {
		t.Errorf("stale session after refusal = %v, want ErrNotFound so /signin does not bounce back", err)
	}

	fresh := register(t, s, "erase-fresh-"+uuid.NewString()+"@example.com")
	freshToken, err := s.StartSession(ctx, fresh.ID, "test", "192.0.2.1")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	out = erase(fresh, freshToken)
	if out.Code != http.StatusSeeOther || out.Header().Get("Location") != "/?erased=1" {
		t.Fatalf("fresh-session erase = %d Location %q, want 303 /?erased=1",
			out.Code, out.Header().Get("Location"))
	}
	if exists(fresh) {
		t.Error("a fresh session did not erase the account")
	}
}
