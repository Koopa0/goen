//go:build integration

package db_test

import (
	"context"
	"errors"
	"maps"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/db/dbtest"
)

// TestInitialDownRefusesADatabaseWithData runs 001.down as golang-migrate
// sends it, the whole file in one implicit transaction, on a shop holding one
// order and on one holding only a category: a shop's data is never undone by a
// migration, so both are refused and no table changes. The empty database's
// down is CI's migration round trip.
func TestInitialDownRefusesADatabaseWithData(t *testing.T) {
	down := initialDown(t)
	for _, tc := range []struct {
		name, data, table string
	}{
		{
			name: "an order",
			data: `INSERT INTO shipping_methods (code) VALUES ('kept');
				INSERT INTO shipping_method_versions (method_id, name, fee_cents)
				SELECT id, 'Kept', 0 FROM shipping_methods WHERE code = 'kept';
				WITH kept_order AS (
					INSERT INTO orders (shipping_version_id, shipping_method_code, shipping_method_name)
					SELECT v.id, sm.code, v.name FROM shipping_method_versions v
					JOIN shipping_methods sm ON sm.id = v.method_id WHERE sm.code = 'kept'
					RETURNING id
				), kept_line AS (
					INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
					SELECT id, 'KEPT-1', 'Kept purchase', 1000, 1 FROM kept_order
				)
				INSERT INTO order_private_data (order_id, email, recipient_name, phone,
				                                postal_code, city, district, street)
				SELECT id, 'kept@example.com', 'Kept recipient', '0912345678',
				       '110', 'Taipei', 'Xinyi', '1 Kept Road' FROM kept_order`,
			table: "orders",
		},
		{
			name:  "a category",
			data:  `INSERT INTO categories (slug, name) VALUES ('kept', 'Kept')`,
			table: "categories",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shop := dbtest.Pool(t)
			if _, err := shop.Exec(t.Context(), tc.data); err != nil {
				t.Fatalf("write %s: %v", tc.name, err)
			}
			before := tableDigests(t, shop)

			_, err := shop.Exec(t.Context(), down)
			if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != "55000" || !strings.Contains(pgErr.Message, tc.table) {
				t.Errorf("001.down with %s = %v, want object_not_in_prerequisite_state (55000) naming %s", tc.name, err, tc.table)
			}
			if after := tableDigests(t, shop); !maps.Equal(before, after) {
				t.Errorf("a refused 001.down changed the tables: %d before, %d after", len(before), len(after))
			}
		})
	}
}

// TestInitialDownSeesARowWrittenAsItRuns: a row committed after 001.down has
// read its table empty, but before it drops the table, still refuses it. The
// row is inserted first and committed once the down migration waits on it, so
// a down migration that drops without locking first would take it.
func TestInitialDownSeesARowWrittenAsItRuns(t *testing.T) {
	shop := dbtest.Pool(t)
	ctx := t.Context()
	writer, err := shop.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Rollback(context.WithoutCancel(ctx)) }()
	if _, insertErr := writer.Exec(ctx, `INSERT INTO categories (slug, name) VALUES ('first', 'First')`); insertErr != nil {
		t.Fatalf("write the first category: %v", insertErr)
	}

	conn, err := shop.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	var pid int32
	if pidErr := conn.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); pidErr != nil {
		t.Fatal(pidErr)
	}
	down := initialDown(t)
	result := make(chan error, 1)
	go func() {
		_, downErr := conn.Exec(ctx, down)
		result <- downErr
	}()

	// 001.down's lock_timeout is 3s; commit as soon as it waits on the row.
	for waiting := false; !waiting; {
		select {
		case early := <-result:
			t.Fatalf("001.down finished (%v) without waiting on an uncommitted row of a table it drops", early)
		case <-time.After(5 * time.Millisecond):
		}
		if _, waiting, err = dbtest.Blocked(ctx, shop, dbtest.Match{PID: pid}); err != nil {
			t.Fatalf("read whether 001.down waits: %v", err)
		}
	}
	if err = writer.Commit(ctx); err != nil {
		t.Fatalf("commit the first category: %v", err)
	}

	err = <-result
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != "55000" || !strings.Contains(pgErr.Message, "categories") {
		t.Errorf("001.down while the first category commits = %v, want object_not_in_prerequisite_state (55000) naming categories", err)
	}
	var categories int
	if err := shop.QueryRow(ctx, `SELECT count(*) FROM categories`).Scan(&categories); err != nil || categories != 1 {
		t.Errorf("after 001.down, categories holds %d rows (%v), want the 1 committed", categories, err)
	}
}

func initialDown(t *testing.T) string {
	t.Helper()
	down, err := os.ReadFile("../../migrations/001_initial_schema.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	return string(down)
}
