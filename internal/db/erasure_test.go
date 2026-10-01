//go:build integration

package db_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// addressSurvivors names any table allowed to keep an erased address, with the reason.
var addressSurvivors = map[string]string{}

// TestTheLastAdminCannotBeErased holds erase_user to refusing the only admin.
func TestTheLastAdminCannotBeErased(t *testing.T) {
	ctx := t.Context()
	tx, beginErr := schemaPool(t).Begin(ctx)
	if beginErr != nil {
		t.Fatalf("begin: %v", beginErr)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, fixtures); err != nil {
		t.Fatalf("fixtures: %v", err)
	}

	const only = "55555555-5555-4555-8555-555555555555"
	const second = "5555aaaa-5555-4555-8555-555555555555"
	if _, err := tx.Exec(ctx,
		`UPDATE users SET role = 'admin' WHERE id = $1`, only); err != nil {
		t.Fatalf("make an admin: %v", err)
	}

	// Inside a SAVEPOINT: the refusal aborts the transaction, and the control below needs these fixtures.
	if _, err := tx.Exec(ctx, `SAVEPOINT last_admin`); err != nil {
		t.Fatalf("savepoint: %v", err)
	}
	_, eraseErr := tx.Exec(ctx, `SELECT erase_user($1)`, only)
	if pgErr, ok := errors.AsType[*pgconn.PgError](eraseErr); !ok ||
		pgErr.ConstraintName != "erase_user_keeps_one_admin" {
		t.Fatalf("erasing the last admin = %v, want the erase_user_keeps_one_admin refusal", eraseErr)
	}
	if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT last_admin`); err != nil {
		t.Fatalf("roll back to the savepoint: %v", err)
	}

	// The control: a function refusing EVERY admin would satisfy the assertion above.
	if _, err := tx.Exec(ctx,
		`UPDATE users SET role = 'admin' WHERE id = $1`, second); err != nil {
		t.Fatalf("make a second admin: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT erase_user($1)`, only); err != nil {
		t.Fatalf("erasing an admin who is not the last one was refused: %v", err)
	}
}

// TestUsersTriggerKeepsOneAdmin proves the database-wide fallback, independent
// of the application functions: even an owner-issued raw role update or delete
// cannot remove the only administrator.
func TestUsersTriggerKeepsOneAdmin(t *testing.T) {
	ctx := t.Context()
	tx, err := schemaPool(t).Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, fixtures); err != nil {
		t.Fatalf("fixtures: %v", err)
	}
	const only = "55555555-5555-4555-8555-555555555555"
	const second = "5555aaaa-5555-4555-8555-555555555555"
	if _, err := tx.Exec(ctx,
		`UPDATE users SET role = 'admin' WHERE id = $1`, only); err != nil {
		t.Fatalf("make an admin: %v", err)
	}

	for name, statement := range map[string]string{
		"role":   `UPDATE users SET role = 'staff' WHERE id = '` + only + `'`,
		"delete": `DELETE FROM users WHERE id = '` + only + `'`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := tx.Exec(ctx, `SAVEPOINT trigger_guard`); err != nil {
				t.Fatalf("savepoint: %v", err)
			}
			_, transitionErr := tx.Exec(ctx, statement)
			if pgErr, ok := errors.AsType[*pgconn.PgError](transitionErr); !ok ||
				pgErr.ConstraintName != "users_keep_one_admin" {
				t.Fatalf("last-admin %s = %v, want users_keep_one_admin", name, transitionErr)
			}
			if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT trigger_guard`); err != nil {
				t.Fatalf("rollback refusal: %v", err)
			}
		})
	}

	// A second admin is the neighbouring legal state; the trigger is not a ban
	// on every roster change.
	if _, err := tx.Exec(ctx,
		`UPDATE users SET role = 'admin' WHERE id = $1`, second); err != nil {
		t.Fatalf("make a second admin: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE users SET role = 'staff' WHERE id = $1`, only); err != nil {
		t.Fatalf("demote with a surviving admin: %v", err)
	}
}

// TestErasureLeavesNoCustomerDetailsInPaymentEvents plants payment events the way a
// restore loads them — before any trigger exists — so the reduction at write time
// has not touched them, and holds erase_user to reducing the erased customer's.
func TestErasureLeavesNoCustomerDetailsInPaymentEvents(t *testing.T) {
	ctx := t.Context()
	tx, err := schemaPool(t).Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, fixtures); err != nil {
		t.Fatalf("fixtures: %v", err)
	}

	const user = "55555555-5555-4555-8555-555555555555"
	if _, err := tx.Exec(ctx,
		`UPDATE users SET email_verified_at = now() WHERE id = $1`, user); err != nil {
		t.Fatalf("prove the mailbox: %v", err)
	}

	type planted struct {
		event, objectRef, email, phone string
	}
	// Attributed through the fixture order's own payment, under an address typed at
	// Stripe that the account never proved; named only by the proved address, in
	// another case; and somebody else's, which must survive.
	owned := planted{"evt_erase_owned", "pi_fixture", "ming.checkout@example.com", "0912345678"}
	unlinked := planted{"evt_erase_unlinked", "cs_erase_unlinked", "MING@example.com", "0911222333"}
	other := planted{"evt_erase_other", "cs_erase_other", "someone.else@example.com", "0987654321"}

	if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
		t.Fatalf("load as a restore does: %v", err)
	}
	for _, p := range []planted{owned, unlinked, other} {
		if _, err := tx.Exec(ctx, `
			INSERT INTO payment_webhook_events (provider, event_id, type, object_ref, payload)
			VALUES ('stripe', $1, 'checkout.session.completed', $2, jsonb_build_object(
			    'id', $1::text, 'type', 'checkout.session.completed',
			    'data', jsonb_build_object('object', jsonb_build_object(
			        'id', $2::text, 'amount_total', 6790000, 'customer_email', $3::text,
			        'customer_details', jsonb_build_object(
			            'email', $3::text, 'phone', $4::text, 'name', '王小明',
			            'address', jsonb_build_object('line1', '松高路 68 號'))))))`,
			p.event, p.objectRef, p.email, p.phone); err != nil {
			t.Fatalf("plant %s: %v", p.event, err)
		}
	}
	if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = origin`); err != nil {
		t.Fatalf("restore trigger enforcement: %v", err)
	}

	if _, err := tx.Exec(ctx, `SELECT erase_user($1)`, user); err != nil {
		t.Fatalf("erase_user: %v", err)
	}

	read := func(event string) string {
		t.Helper()
		var payload string
		if err := tx.QueryRow(ctx, `
			SELECT payload::text FROM payment_webhook_events
			WHERE provider = 'stripe' AND event_id = $1`, event).Scan(&payload); err != nil {
			t.Fatalf("read %s: %v", event, err)
		}
		return payload
	}
	for _, p := range []planted{owned, unlinked} {
		payload := read(p.event)
		for _, personal := range []string{strings.ToLower(p.email), p.phone, "王小明", "松高路"} {
			if strings.Contains(strings.ToLower(payload), personal) {
				t.Errorf("%s still carries %q after its customer was erased: %s",
					p.event, personal, payload)
			}
		}
		if !strings.Contains(payload, p.event) || !strings.Contains(payload, p.objectRef) ||
			!strings.Contains(payload, "6790000") {
			t.Errorf("%s lost the evidence erasure has no reason to touch: %s", p.event, payload)
		}
	}

	// The control: an erasure reducing every event would pass everything above.
	if payload := read(other.event); !strings.Contains(payload, other.email) {
		t.Errorf("erasing one customer reduced another's event: %s", payload)
	}
}

// TestErasureReachesTheGuestOrdersOfAProvedAddress holds erase_user to treating an
// order the proved address placed as a guest the way it treats the account's own:
// delivery details, the customer's note and every browser's access go, and the
// order stays as the financial record it is. The controls are the three orders the
// same address must NOT reach.
func TestErasureReachesTheGuestOrdersOfAProvedAddress(t *testing.T) {
	ctx := t.Context()
	tx, err := schemaPool(t).Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, fixtures); err != nil {
		t.Fatalf("fixtures: %v", err)
	}

	const (
		ming     = "55555555-5555-4555-8555-555555555555"
		hua      = "5555aaaa-5555-4555-8555-555555555555"
		unproved = "11110090-0000-4000-8000-000000000001"
		// Placed as a guest under Ming's address, in another case.
		guestOrder = "11110091-0000-4000-8000-000000000001"
		// Hua's own order, delivered to Ming's address: it is Hua's to erase.
		huaOrder = "11110091-0000-4000-8000-000000000002"
		// A guest order under an address its account never proved.
		unprovedOrder = "11110091-0000-4000-8000-000000000003"
		// The fixture's guest order, under somebody else's address entirely.
		otherGuestOrder = "6666aaaa-6666-4666-8666-666666666666"
	)
	if _, err := tx.Exec(ctx, `
		UPDATE users SET email_verified_at = now() WHERE id = $1;`, ming); err != nil {
		t.Fatalf("prove Ming's mailbox: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO users (id, email, full_name) VALUES ($1, 'unproved@example.com', '未驗證');`,
		unproved); err != nil {
		t.Fatalf("create the unproved account: %v", err)
	}
	for _, o := range []struct{ id, number, owner, email string }{
		{guestOrder, "GO-260721-000981", "", "MING@example.com"},
		{huaOrder, "GO-260721-000982", hua, "ming@example.com"},
		{unprovedOrder, "GO-260721-000983", "", "unproved@example.com"},
	} {
		for _, stmt := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO orders (id, order_number, user_id, shipping_version_id,
			                      shipping_method_code, shipping_method_name, customer_note)
			  VALUES ($1, $2, nullif($3, '')::uuid, 'ffff0002-0000-4000-8000-000000000000',
			          'home_delivery', '宅配到府', '請撥 0912345678 找王小明')`,
				[]any{o.id, o.number, o.owner}},
			{`INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
			  VALUES ($1, 'GUEST-ERASE', '訪客訂單商品', 1000, 1)`, []any{o.id}},
			{`INSERT INTO order_private_data (order_id, email, recipient_name, phone,
			                                  postal_code, city, district, street)
			  VALUES ($1, $2, '王小明', '0912345678', '110', '台北市', '信義區', '松高路 68 號')`,
				[]any{o.id, o.email}},
			{`INSERT INTO order_access_grants (digest, order_id)
			  VALUES (sha256(convert_to($1::uuid::text, 'UTF8')), $1)`, []any{o.id}},
		} {
			if _, err := tx.Exec(ctx, stmt.sql, stmt.args...); err != nil {
				t.Fatalf("place order %s: %v", o.number, err)
			}
		}
	}

	for _, user := range []string{ming, unproved} {
		if _, err := tx.Exec(ctx, `SELECT erase_user($1)`, user); err != nil {
			t.Fatalf("erase_user(%s): %v", user, err)
		}
	}

	type state struct {
		email, recipient, phone, street, note *string
		erased                                bool
		grants                                int
		lines                                 int
	}
	read := func(order string) state {
		t.Helper()
		var s state
		if err := tx.QueryRow(ctx, `
			SELECT pd.email, pd.recipient_name, pd.phone, pd.street, o.customer_note,
			       pd.erased_at IS NOT NULL,
			       (SELECT count(*) FROM order_access_grants g WHERE g.order_id = o.id),
			       (SELECT count(*) FROM order_lines l WHERE l.order_id = o.id)
			FROM orders o JOIN order_private_data pd ON pd.order_id = o.id
			WHERE o.id = $1`, order).Scan(&s.email, &s.recipient, &s.phone, &s.street,
			&s.note, &s.erased, &s.grants, &s.lines); err != nil {
			t.Fatalf("read order %s: %v", order, err)
		}
		return s
	}

	got := read(guestOrder)
	if got.email != nil || got.recipient != nil || got.phone != nil || got.street != nil ||
		!got.erased {
		t.Errorf("the guest order Ming's proved address placed kept its delivery details "+
			"(email %v, recipient %v, phone %v, street %v, erased %t)",
			deref(got.email), deref(got.recipient), deref(got.phone), deref(got.street), got.erased)
	}
	if got.note != nil {
		t.Errorf("the guest order kept the customer's note %q", *got.note)
	}
	if got.grants != 0 {
		t.Errorf("%d browser(s) can still open the erased guest order", got.grants)
	}
	if got.lines != 1 {
		t.Errorf("the guest order has %d line(s) after erasure, want the 1 it was sold with", got.lines)
	}

	for _, control := range []struct{ order, why string }{
		{huaOrder, "an account's order delivered to that address is the account's to erase"},
		{unprovedOrder, "an address nobody proved is no authority over the orders placed under it"},
		{otherGuestOrder, "a guest order under another address is nobody's business here"},
	} {
		got := read(control.order)
		if got.email == nil || got.recipient == nil || got.erased {
			t.Errorf("order %s lost its delivery details, but %s", control.order, control.why)
		}
	}
	if note := read(huaOrder).note; note == nil {
		t.Errorf("Hua's order lost its note to Ming's erasure")
	}
}

// TestAnAccountEveryForeignKeyNamesCanBeErased references one account from every
// foreign key to users, derived from the catalog so a new one is refused until it
// is populated here, and erases it as store, the role /account/erase runs as. Each
// key's ON DELETE action fires the triggers of the table it rewrites, and one of
// them refusing is a customer who can never leave.
func TestAnAccountEveryForeignKeyNamesCanBeErased(t *testing.T) {
	ctx := t.Context()
	tx, err := schemaPool(t).Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, fixErr := tx.Exec(ctx, fixtures); fixErr != nil {
		t.Fatalf("fixtures: %v", fixErr)
	}

	const (
		account  = "11110095-0000-4000-8000-000000000001"
		order    = "11110095-0000-4000-8000-000000000002"
		line     = "11110095-0000-4000-8000-000000000003"
		parcel   = "11110095-0000-4000-8000-000000000004"
		claim    = "11110095-0000-4000-8000-000000000005"
		question = "11110095-0000-4000-8000-000000000006"
	)
	for _, stmt := range []string{
		`INSERT INTO users (id, email, full_name) VALUES ('` + account + `', 'everything@example.com', '全部')`,
		// The account's own order, free through its discount so it can ship with
		// no payment, delivered so a unit of it can carry cover.
		`INSERT INTO orders (id, order_number, user_id, shipping_version_id, shipping_method_code,
		                     shipping_method_name, discount_cents)
		 VALUES ('` + order + `', 'GO-260721-000995', '` + account + `',
		         'ffff0002-0000-4000-8000-000000000000', 'home_delivery', '宅配到府', 1000)`,
		`INSERT INTO order_lines (id, order_id, variant_id, sku, product_name, unit_price_cents, quantity, position)
		 VALUES ('` + line + `', '` + order + `', '44444444-4444-4444-8444-444444444444',
		         'PXL-9P-256-BL', 'Pixelight 9 Pro 5G', 1000, 1, 0)`,
		`INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street)
		 VALUES ('` + order + `', 'everything@example.com', '全部', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		`UPDATE orders SET fulfillment_status = 'picking' WHERE id = '` + order + `'`,
		`UPDATE orders SET fulfillment_status = 'shipped' WHERE id = '` + order + `'`,
		`INSERT INTO order_shipments (id, order_id, carrier, tracking_number, delivered_at)
		 VALUES ('` + parcel + `', '` + order + `', '黑貓宅急便', 'EVERY-FK-1', now())`,
		`INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		 VALUES ('` + order + `', '` + parcel + `', '` + line + `', 1)`,
		`INSERT INTO warranty_registrations (order_line_id, unit_no, user_id, expires_on)
		 VALUES ('` + line + `', 1, '` + account + `', current_date + 365)`,
		`INSERT INTO coupon_redemptions (coupon_id, order_id, user_id, amount_cents)
		 VALUES ('cccc0009-0000-4000-8000-000000000009', '` + order + `', '` + account + `', 1000)`,
		`INSERT INTO return_requests (id, order_id, requested_by_user_id, reason)
		 VALUES ('` + claim + `', '` + order + `', '` + account + `', '')`,
		`INSERT INTO return_eligibility_assessments
		     (order_id, return_request_id, version, assessed_by, assessed_by_snapshot, basis)
		 VALUES ('` + order + `', '` + claim + `', 1, '` + account + `', '` + account + `', '看過了')`,
		`INSERT INTO order_events (order_id, kind, actor_user_id) VALUES ('` + order + `', 'in_transit', '` + account + `')`,
		`INSERT INTO invoice_operations (order_id, kind, provider_key, amount_cents, request_payload,
		                                 actor_user_id, actor_id_snapshot, request_id)
		 VALUES ('66666666-6666-4666-8666-666666666666', 'issue', 'GO260721000387', 6788000, '{}',
		         '` + account + `', '` + account + `', 'every-fk')`,
		`INSERT INTO audit_events (actor_user_id, actor_id_snapshot, action, entity_table)
		 VALUES ('` + account + `', '` + account + `', 'probe.every_fk', 'users')`,
		`SELECT record_inventory_movement('44444444-4444-4444-8444-444444444444', 1, 'receipt',
		        'every-fk-receipt', 'admin', NULL, '` + account + `')`,
		`INSERT INTO store_credit_accounts (user_id) VALUES ('` + account + `')`,
		`INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key, actor_user_id)
		 VALUES ('a0000001-0000-4000-8000-000000000000', 100, '補償', 'every-fk-credit', '` + account + `')`,
		`INSERT INTO loyalty_redemption_operations (user_id) VALUES ('` + account + `')`,
		`INSERT INTO newsletter_issues (subject, body, sent_by) VALUES ('每一個', '每一個外鍵', '` + account + `')`,
		`INSERT INTO addresses (user_id, recipient_name, phone, postal_code, city, district, street)
		 VALUES ('` + account + `', '全部', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		`INSERT INTO carts (token_hash, user_id) VALUES ('\x0995', '` + account + `')`,
		`INSERT INTO wishlist_items (user_id, product_id) VALUES ('` + account + `', '33333333-3333-4333-8333-333333333333')`,
		`INSERT INTO stock_notifications (variant_id, user_id, email)
		 VALUES ('44444444-4444-4444-8444-444444444444', '` + account + `', 'everything@example.com')`,
		`INSERT INTO product_questions (id, product_id, user_id, body)
		 VALUES ('` + question + `', '33333333-3333-4333-8333-333333333333', '` + account + `', '有保固嗎?')`,
		`INSERT INTO product_answers (question_id, user_id, body) VALUES ('` + question + `', '` + account + `', '有。')`,
		`INSERT INTO product_reviews (product_id, user_id, rating, body)
		 VALUES ('33333333-3333-4333-8333-333333333333', '` + account + `', 5, '好用')`,
		`INSERT INTO sessions (token_hash, user_id, expires_at)
		 VALUES (sha256('every-fk-session'::bytea), '` + account + `', now() + interval '1 day')`,
		`INSERT INTO password_reset_tokens (token_hash, user_id, expires_at)
		 VALUES (sha256('every-fk-reset'::bytea), '` + account + `', now() + interval '1 hour')`,
		`INSERT INTO email_verifications (user_id, email, digest, expires_at)
		 VALUES ('` + account + `', 'everything.new@example.com', sha256('every-fk-verify'::bytea), now() + interval '1 day')`,
		`INSERT INTO user_identities (user_id, provider, provider_subject) VALUES ('` + account + `', 'google', 'every-fk')`,
		`INSERT INTO staff_totp_credentials (user_id, secret_encrypted) VALUES ('` + account + `', '\x01')`,
	} {
		if _, refErr := tx.Exec(ctx, stmt); refErr != nil {
			t.Fatalf("reference the account: %v\n%s", refErr, stmt)
		}
	}

	type key struct{ table, column string }
	rows, err := tx.Query(ctx, `
		SELECT c.conrelid::regclass::text, a.attname
		FROM pg_constraint c
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
		WHERE c.contype = 'f' AND c.confrelid = 'users'::regclass
		ORDER BY 1, 2`)
	if err != nil {
		t.Fatalf("read the foreign keys to users: %v", err)
	}
	var keys []key
	for rows.Next() {
		var k key
		if scanErr := rows.Scan(&k.table, &k.column); scanErr != nil {
			t.Fatalf("scan: %v", scanErr)
		}
		keys = append(keys, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("walk the foreign keys: %v", err)
	}
	if len(keys) < 20 {
		t.Fatalf("only %d foreign keys to users found — the catalog query is wrong", len(keys))
	}
	references := func(k key) int {
		t.Helper()
		var n int
		if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %s WHERE %s = $1`,
			pgx.Identifier{k.table}.Sanitize(), pgx.Identifier{k.column}.Sanitize()),
			account).Scan(&n); err != nil {
			t.Fatalf("count %s.%s: %v", k.table, k.column, err)
		}
		return n
	}
	for _, k := range keys {
		if references(k) == 0 {
			t.Errorf("no row of %s.%s names the account. Add one above: a foreign key "+
				"nobody erases through is where the next refused erasure hides.", k.table, k.column)
		}
	}
	if t.Failed() {
		t.FailNow()
	}

	if _, err := tx.Exec(ctx, `SET LOCAL ROLE store`); err != nil {
		t.Fatalf("assume store: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT erase_user($1)`, account); err != nil {
		t.Fatalf("erase_user refused an account every foreign key names: %v", err)
	}
	if _, err := tx.Exec(ctx, `RESET ROLE`); err != nil {
		t.Fatalf("reset role: %v", err)
	}
	for _, k := range keys {
		if n := references(k); n != 0 {
			t.Errorf("%s.%s still names the erased account in %d row(s)", k.table, k.column, n)
		}
	}
}

// deref reads an optional column for a failure message.
func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

// assertNoTableHoldsTheAddress asks every table with a text email column, derived from
// information_schema, whether it still holds the address after erasure.
func assertNoTableHoldsTheAddress(ctx context.Context, t *testing.T, tx pgx.Tx, addr string) {
	t.Helper()
	assertNoJSONHoldsTheAddress(ctx, t, tx, addr)

	rows, err := tx.Query(ctx, `
		SELECT c.table_name
		FROM information_schema.columns c
		JOIN information_schema.tables t
		  ON t.table_schema = c.table_schema AND t.table_name = c.table_name
		WHERE c.table_schema = 'public' AND t.table_type = 'BASE TABLE'
		  AND c.column_name = 'email' AND c.data_type = 'text'
		ORDER BY c.table_name`)
	if err != nil {
		t.Fatalf("find the tables holding an address: %v", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if scanErr := rows.Scan(&name); scanErr != nil {
			t.Fatalf("scan a table name: %v", scanErr)
		}
		tables = append(tables, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("walk the tables holding an address: %v", err)
	}
	if len(tables) < 4 {
		t.Fatalf("only %d tables carry an email column, want more — the query is wrong",
			len(tables))
	}

	found := 0
	for _, table := range tables {
		var n int
		if err := tx.QueryRow(ctx, fmt.Sprintf(
			`SELECT count(*) FROM %s WHERE lower(email) = lower($1)`,
			pgx.Identifier{table}.Sanitize()), addr).Scan(&n); err != nil {
			t.Fatalf("probe %s for the erased address: %v", table, err)
		}
		if n == 0 {
			continue
		}
		if why, ok := addressSurvivors[table]; ok {
			found++
			t.Logf("%s keeps the address: %s", table, why)
			continue
		}
		t.Errorf("%s still holds the erased address after erase_user (%d row(s)).\n"+
			"  If it keys on the ADDRESS rather than on the user, a DELETE of the "+
			"account never reaches it — which is how contact_messages kept a "+
			"customer's own words, address and phone number after they asked to be "+
			"forgotten, and how the newsletter would have gone on emailing them.\n"+
			"  Either erase_user should reach it, or it belongs in addressSurvivors "+
			"with the reason.", table, n)
	}
	if found < len(addressSurvivors) {
		t.Errorf("%d of the %d addressSurvivors entries actually hold the address; the "+
			"rest are stale entries claiming a gap that has been closed",
			found, len(addressSurvivors))
	}
}

// assertNoJSONHoldsTheAddress is the half a column sweep cannot see: an address
// inside a jsonb payload sits under no column named `email`. goen has two such
// stores — the outbox freezes the recipient into every message it enqueues, and
// audit_events is append-only with erase_user unable to reach it at all.
//
// Derived from information_schema like its neighbour, so a third jsonb store is
// covered the day it is added.
func assertNoJSONHoldsTheAddress(ctx context.Context, t *testing.T, tx pgx.Tx, addr string) {
	t.Helper()

	rows, err := tx.Query(ctx, `
		SELECT c.table_name, c.column_name
		FROM information_schema.columns c
		JOIN information_schema.tables t
		  ON t.table_schema = c.table_schema AND t.table_name = c.table_name
		WHERE c.table_schema = 'public' AND t.table_type = 'BASE TABLE'
		  AND c.data_type IN ('jsonb', 'json')
		ORDER BY c.table_name, c.column_name`)
	if err != nil {
		t.Fatalf("find the jsonb columns: %v", err)
	}
	type col struct{ table, name string }
	var cols []col
	for rows.Next() {
		var c col
		if scanErr := rows.Scan(&c.table, &c.name); scanErr != nil {
			t.Fatalf("scan a jsonb column: %v", scanErr)
		}
		cols = append(cols, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("walk the jsonb columns: %v", err)
	}
	if len(cols) < 2 {
		t.Fatalf("only %d jsonb columns found, want at least the outbox payload and "+
			"the audit trail — the query is wrong and this proved nothing", len(cols))
	}

	// The whole value as text, not payload->>'email': an address can sit at any
	// key, and the point is that it is GONE rather than gone from one field.
	for _, c := range cols {
		var n int
		if err := tx.QueryRow(ctx, fmt.Sprintf(
			`SELECT count(*) FROM %s WHERE %s::text ILIKE '%%' || $1 || '%%'`,
			pgx.Identifier{c.table}.Sanitize(),
			pgx.Identifier{c.name}.Sanitize()), addr).Scan(&n); err != nil {
			t.Fatalf("probe %s.%s for the erased address: %v", c.table, c.name, err)
		}
		if n > 0 {
			t.Errorf("%s.%s still holds the erased address in %d row(s) — the privacy "+
				"page promises it is deleted, and JSON is where a column sweep does not look",
				c.table, c.name, n)
		}
	}
}
