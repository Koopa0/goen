//go:build integration

package db_test

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db/dbtest"
)

// TestOrderNumbersAreRandomDistinctAndWellFormed places 1,000 orders as the store
// role from 8 concurrent sessions, each in the transaction a checkout uses, so
// the number comes from the column DEFAULT exactly as it does in production.
// Every number must be new, match the format, and not be a digit counter in
// disguise: the digits-only share of a random six-character base32 suffix is
// 10^6 / 32^6, about 0.09%, so a share above 5% is a sequence.
func TestOrderNumbersAreRandomDistinctAndWellFormed(t *testing.T) {
	const (
		sessions   = 8
		perSession = 125
		total      = sessions * perSession
	)
	owner := dbtest.Pool(t)
	ctx := t.Context()

	var method, version string
	if err := owner.QueryRow(ctx, `INSERT INTO shipping_methods (code) VALUES ('home_delivery') RETURNING id::text`).Scan(&method); err != nil {
		t.Fatalf("create the shipping method: %v", err)
	}
	if err := owner.QueryRow(ctx, `
		INSERT INTO shipping_method_versions (method_id, name, fee_cents) VALUES ($1, 'Delivery', 0)
		RETURNING id::text`, method).Scan(&version); err != nil {
		t.Fatalf("create the shipping version: %v", err)
	}
	var today string
	if err := owner.QueryRow(ctx, `SELECT to_char(shop_today(), 'YYMMDD')`).Scan(&today); err != nil {
		t.Fatalf("read the shop's day: %v", err)
	}

	cfg := owner.Config().Copy()
	cfg.MaxConns = sessions
	cfg.MinConns = sessions
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE store")
		return err
	}
	store, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("open the store pool: %v", err)
	}
	t.Cleanup(store.Close)

	numbers := make(chan string, total)
	errs := make(chan error, sessions)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range sessions {
		wg.Go(func() {
			<-start
			for range perSession {
				number, placeErr := placeOrderAsStore(ctx, store, version)
				if placeErr != nil {
					errs <- placeErr
					return
				}
				numbers <- number
			}
		})
	}
	close(start)
	wg.Wait()
	close(numbers)
	close(errs)
	for placeErr := range errs {
		t.Errorf("place an order: %v", placeErr)
	}

	format := regexp.MustCompile(`^GO-` + today + `-[0-9A-HJKMNP-TV-Z]{6}$`)
	seen := map[string]bool{}
	digitsOnly := 0
	for number := range numbers {
		if !format.MatchString(number) {
			t.Errorf("order number %q does not match %s", number, format)
		}
		if seen[number] {
			t.Errorf("order number %q was handed out twice", number)
		}
		seen[number] = true
		if strings.Trim(number[len("GO-YYMMDD-"):], "0123456789") == "" {
			digitsOnly++
		}
	}
	if len(seen) != total {
		t.Errorf("%d distinct order numbers from %d orders", len(seen), total)
	}
	if digitsOnly*100 >= total*5 {
		t.Errorf("%d of %d suffixes are digits only (limit 5%%; random is about 0.09%%): the suffix is a counter, not a draw", digitsOnly, total)
	}
}

// placeOrderAsStore inserts one complete order, its number from the column
// DEFAULT, and commits it, so the next draw sees it.
func placeOrderAsStore(ctx context.Context, store *pgxpool.Pool, version string) (string, error) {
	tx, beginErr := store.Begin(ctx)
	if beginErr != nil {
		return "", beginErr
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var id, number string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (shipping_version_id, shipping_method_code, shipping_method_name)
		VALUES ($1, 'home_delivery', 'Delivery')
		RETURNING id::text, order_number`, version).Scan(&id, &number); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone, postal_code, city, district, street)
		VALUES ($1, 'buyer@example.com', 'Buyer', '0912345678', '110', 'Taipei', 'Xinyi', '1 Songgao Rd')`, id); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity, position)
		VALUES ($1, 'NUMBER-TEST', 'Number test', 1000, 1, 0)`, id); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return number, nil
}
