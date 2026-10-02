//go:build integration

// Package admintest is what the back office's integration suites share. Every
// file carries the integration tag, so no production build and no untagged tool
// sees it.
package admintest

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/web"
)

// BackOffice wraps a handler as its route in cmd/goen does, with 2FA off.
var BackOffice = access.New(slog.New(slog.DiscardHandler), nil)

// LoadCatalogue puts the development catalogue into a database dbtest started.
// A suite's TestMain still calls dbtest.Start itself: internal/db refuses a
// TestMain that does not, so no suite applies migrations its own way.
func LoadCatalogue(ctx context.Context, pool *pgxpool.Pool) error {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return errors.New("admintest: cannot locate this source file")
	}
	path := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "seed", "dev_catalog.sql")
	seed, err := os.ReadFile(path) //nolint:gosec // G304: a fixed path inside this repository
	if err != nil {
		return fmt.Errorf("read seed: %w", err)
	}
	if _, err := pool.Exec(ctx, string(seed)); err != nil {
		return fmt.Errorf("load seed: %w", err)
	}
	return nil
}

// Pool is a catalogued database of one test's own, for a test that counts rows
// the rest of its suite also writes.
func Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := dbtest.Pool(t)
	if err := LoadCatalogue(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

// StaffContext creates an admin user in p and returns a context that carries it
// and a request id, as the access wrapper leaves it for a back-office handler.
func StaffContext(t *testing.T, p *pgxpool.Pool) (context.Context, uuid.UUID) {
	t.Helper()
	var id uuid.UUID
	if err := p.QueryRow(t.Context(), `
		INSERT INTO users (email, role, full_name)
		VALUES ('audit-' || gen_random_uuid() || '@goen.invalid', 'admin', '稽核測試')
		RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("create staff: %v", err)
	}
	ctx := account.WithUser(t.Context(), account.User{ID: id.String(), Role: "admin"})
	return web.WithRequestID(ctx, "req-"+id.String()[:8]), id
}

// AuditRows counts the audit events of one action.
func AuditRows(t *testing.T, p *pgxpool.Pool, action audit.Action) int {
	t.Helper()
	var n int
	if err := p.QueryRow(t.Context(),
		`SELECT count(*) FROM audit_events WHERE action = $1`, string(action)).Scan(&n); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	return n
}

// CreditedAccount creates a customer holding a store-credit account of this
// balance; an account of zero is made directly, because a zero grant is refused.
func CreditedAccount(t *testing.T, pool *pgxpool.Pool, cents int64) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	var userID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name)
		VALUES ('cust-'||gen_random_uuid()||'@goen.invalid', 'customer', '顧客測試')
		RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("create customer: %v", err)
	}
	// A zero grant would meet store_credit_entries_amount_non_zero, so an empty account is made directly.
	if cents > 0 {
		if _, err := pool.Exec(ctx,
			`SELECT post_store_credit($1, $2, '測試發放', NULL, $3, NULL)`,
			userID, cents, "grant:"+userID.String()); err != nil {
			t.Fatalf("grant credit: %v", err)
		}
	} else {
		if _, err := pool.Exec(ctx,
			`INSERT INTO store_credit_accounts (user_id) VALUES ($1)`, userID); err != nil {
			t.Fatalf("create credit account: %v", err)
		}
	}
	return userID
}

// OrderForCustomer places an order of one line for the customer, paid by card
// when paid.
func OrderForCustomer(t *testing.T, pool *pgxpool.Pool, userID uuid.UUID, cents int64, paid bool) uuid.UUID {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit

	var orderID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, user_id, shipping_version_id,
		                    shipping_method_code, shipping_method_name, shipping_cents)
		SELECT next_order_number(), $1, v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1 RETURNING id`, userID).Scan(&orderID); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'cust@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("delivery details: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'CUST-SKU', '顧客頁測試', $2, 1)`, orderID, cents); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if paid {
		ref := "cust_" + orderID.String()
		if _, err := pool.Exec(ctx, `SELECT open_payment($1, $2, $3::bigint)`,
			orderID, ref, cents); err != nil {
			t.Fatalf("open payment: %v", err)
		}
		if _, err := pool.Exec(ctx, `SELECT capture_payment($1, $2::bigint, NULL, NULL)`,
			ref, cents); err != nil {
			t.Fatalf("capture: %v", err)
		}
	}
	return orderID
}

// AdminUser creates an admin and returns their id and address.
func AdminUser(t *testing.T, p *pgxpool.Pool) (userID, email string) {
	t.Helper()
	var id uuid.UUID
	if err := p.QueryRow(t.Context(), `
		INSERT INTO users (email, role, full_name)
		VALUES ('totp-' || gen_random_uuid() || '@goen.invalid', 'admin', '測試')
		RETURNING id, email`).Scan(&id, &email); err != nil {
		t.Fatalf("create staff: %v", err)
	}
	return id.String(), email
}

// CampaignSlug is a slug no other test's campaign has.
func CampaignSlug(t *testing.T) string {
	t.Helper()
	return "admin-camp-" + uuid.NewString()[:8]
}

// DiscountedProductSlug is an active product with a marked-down variant, which
// is what a campaign may feature.
func DiscountedProductSlug(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var slug string
	if err := pool.QueryRow(t.Context(), `
		SELECT p.slug FROM products p JOIN product_variants pv ON pv.product_id = p.id
		WHERE p.status = 'active' AND pv.is_active
		  AND pv.compare_at_price_cents > pv.price_cents
		LIMIT 1`).Scan(&slug); err != nil {
		t.Fatalf("find discounted product: %v", err)
	}
	return slug
}

// NamedPool is a one-connection pool whose connections carry an application
// name, so a test can tell which of two concurrent writers is blocked.
func NamedPool(t *testing.T, pool *pgxpool.Pool, applicationName string) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(pool.Config().ConnString())
	if err != nil {
		t.Fatalf("parse admin pool config: %v", err)
	}
	cfg.MaxConns = 1
	cfg.ConnConfig.RuntimeParams["application_name"] = applicationName
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open traced admin pool: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

func AssertRefusedInput(t *testing.T, body, id, raw string) {
	t.Helper()
	input := InputElementByID(t, body, id)
	if got := InputAttribute(t, input, "value"); got != raw {
		t.Errorf("input %q value = %q, want raw %q", id, got, raw)
	}
	if got := InputAttribute(t, input, "aria-invalid"); got != "true" {
		t.Errorf("input %q aria-invalid = %q, want true", id, got)
	}
	errorID := id + "-error"
	if got := InputAttribute(t, input, "aria-describedby"); got != errorID {
		t.Errorf("input %q aria-describedby = %q, want %q", id, got, errorID)
	}
	if !regexp.MustCompile(`<p[^>]*id="` + regexp.QuoteMeta(errorID) + `"[^>]*>[^<]+</p>`).
		MatchString(body) {
		t.Errorf("input %q has no nonempty error element %q", id, errorID)
	}
}

func AssertTextNumberControl(t *testing.T, body, id string) {
	t.Helper()
	input := InputElementByID(t, body, id)
	if got := InputAttribute(t, input, "type"); got != "text" {
		t.Errorf("input %q type = %q, want text", id, got)
	}
	if got := InputAttribute(t, input, "inputmode"); got != "numeric" {
		t.Errorf("input %q inputmode = %q, want numeric", id, got)
	}
	for _, attribute := range []string{"min", "max", "step"} {
		if got := InputAttribute(t, input, attribute); got != "" {
			t.Errorf("input %q retains ineffective %s=%q", id, attribute, got)
		}
	}
}

func InputAttribute(t *testing.T, input, name string) string {
	t.Helper()
	match := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `="([^"]*)"`).FindStringSubmatch(input)
	if len(match) != 2 {
		return ""
	}
	return html.UnescapeString(match[1])
}

func InputElementByID(t *testing.T, body, id string) string {
	t.Helper()
	match := regexp.MustCompile(`<input\b[^>]*\bid="` + regexp.QuoteMeta(id) + `"[^>]*>`).
		FindString(body)
	if match == "" {
		t.Fatalf("no input with id %q in rendered page", id)
	}
	return match
}

func WaitForBlockedApplication(
	t *testing.T, pool *pgxpool.Pool, ctx context.Context, applicationName string, blockerPID int32,
) int32 {
	t.Helper()
	for {
		var pid int32
		var blockers []int32
		err := pool.QueryRow(ctx, `
			SELECT pid, pg_blocking_pids(pid) FROM pg_stat_activity
			WHERE datname = current_database() AND application_name = $1
			  AND state = 'active' AND wait_event_type = 'Lock'`, applicationName).
			Scan(&pid, &blockers)
		if err == nil {
			for _, got := range blockers {
				if got == blockerPID {
					return pid
				}
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("trace blocked writer %q: %v", applicationName, err)
		}
		if ctx.Err() != nil {
			t.Fatalf("writer %q never blocked behind pid %d: %v", applicationName, blockerPID, ctx.Err())
		}
	}
}
