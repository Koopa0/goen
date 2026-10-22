//go:build integration

package issuer

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/internal/invoice"
)

// TestInvoicePreferencesClosedSet: invoice_preferences stores every preference
// offered at checkout with the fields it needs, and refuses an unknown type by
// invoice_preferences_type_known. It keeps the Go domain type and the CHECK
// constraints as one contract. A new checkout choice must be admitted here with
// the fields its semantics require; an arbitrary string must still be refused
// by the named closed-set constraint.
func TestInvoicePreferencesClosedSet(t *testing.T) {
	ctx := t.Context()
	newOrder := func(t *testing.T) uuid.UUID {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin preference order: %v", err)
		}
		defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
		var orderID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO orders
			    (order_number, shipping_version_id, shipping_method_code,
			     shipping_method_name)
			SELECT next_order_number(), smv.id, sm.code, smv.name
			FROM shipping_method_versions smv
			JOIN shipping_methods sm ON sm.id = smv.method_id
			LIMIT 1
			RETURNING id`).Scan(&orderID); err != nil {
			t.Fatalf("create preference order: %v", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_lines
			    (order_id, variant_id, sku, product_name,
			     unit_price_cents, quantity, position)
			SELECT $1, pv.id, pv.sku, p.name, 10000, 1, 0
			FROM product_variants pv JOIN products p ON p.id = pv.product_id LIMIT 1`, orderID); err != nil {
			t.Fatalf("add preference order line: %v", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_private_data
			    (order_id, email, recipient_name, phone,
			     postal_code, city, district, street)
			VALUES ($1, 'closed-set@goen.invalid', '王小明', '0912345678',
			        '110', '台北市', '信義區', '測試路 1 號')`, orderID); err != nil {
			t.Fatalf("add preference order delivery: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit preference order: %v", err)
		}
		return orderID
	}

	for _, preference := range invoice.OfferedPreferences() {
		t.Run(string(preference), func(t *testing.T) {
			var barcode, taxID, donationCode *string
			if preference.NeedsMobileBarcode() {
				value := "/AB12345"
				barcode = &value
			}
			if preference == invoice.PreferenceDonate {
				value := "00123"
				donationCode = &value
			}
			if preference.NeedsTaxID() {
				value := "04595252"
				taxID = &value
			}

			result, err := pool.Exec(ctx, `
				INSERT INTO invoice_preferences
				    (order_id, invoice_type, carrier_code, tax_id, donation_code,
				     customer_name, customer_email)
				VALUES ($1, $2, $3, $4, $5, '王小明', 'closed-set@goen.invalid')`,
				newOrder(t), string(preference), barcode, taxID, donationCode)
			if err != nil {
				t.Fatalf("database refused offered preference %q: %v", preference, err)
			}
			if result.RowsAffected() != 1 {
				t.Fatalf("stored offered preference %q in %d rows, want 1",
					preference, result.RowsAffected())
			}
		})
	}

	_, err := pool.Exec(ctx, `
		INSERT INTO invoice_preferences
		    (order_id, invoice_type, customer_name, customer_email)
		VALUES ($1, 'paper', '王小明', 'closed-set@goen.invalid')`, newOrder(t))
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.ConstraintName != "invoice_preferences_type_known" {
		t.Fatalf("unknown preference error = %v, want constraint %q",
			err, "invoice_preferences_type_known")
	}
}

// TestValidBusinessTaxID: the SQL function valid_business_tax_id agrees with
// Go's invoice.ValidTaxID. It holds the Go validator and its PL/pgSQL twin to
// one answer over the cases that bend the rule (a seventh digit of 7) and a
// seeded random sample, so neither can drift without this failing.
func TestValidBusinessTaxID(t *testing.T) {
	corpus := []string{
		"", "00000000", "10458575", "04595257", "10458570", "1045857", "104585750",
		"1045857a", "２０４５８５７５", "04595252", "04595253", "00000007", "00000070",
	}
	rng := rand.New(rand.NewPCG(555, 555)) //nolint:gosec // G404: a reproducible test sample, not a secret
	corpus = slices.Grow(corpus, 5000)
	for range 5000 {
		v := fmt.Sprintf("%08d", rng.IntN(100_000_000))
		if rng.IntN(4) == 0 {
			v = v[:6] + "7" + v[7:]
		}
		corpus = append(corpus, v)
	}

	rows, err := pool.Query(t.Context(),
		`SELECT v, valid_business_tax_id(v) FROM unnest($1::text[]) AS v`, corpus)
	if err != nil {
		t.Fatalf("ask the schema: %v", err)
	}
	defer rows.Close()
	var agreed int
	for rows.Next() {
		var value string
		var sql bool
		if err := rows.Scan(&value, &sql); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if goValid := invoice.ValidTaxID(value); goValid != sql {
			t.Errorf("%q: ValidTaxID = %t, valid_business_tax_id = %t", value, goValid, sql)
		}
		agreed++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read: %v", err)
	}
	if agreed != len(corpus) {
		t.Fatalf("the schema answered %d of %d values", agreed, len(corpus))
	}
}
