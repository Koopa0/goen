//go:build integration

package invoice

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/pgtx"
)

type invoiceArithmeticItem struct {
	name     string
	price    int64
	quantity int32
}

type canonicalInvoiceRow struct {
	Description    string
	Quantity       int32
	UnitPriceCents int64
	AmountCents    int64
	TaxType        string
	Unit           string
	Position       int32
}

type invoiceArithmeticVector struct {
	name               string
	items              []invoiceArithmeticItem
	discount, shipping int64
	wantTotal          int64
	want               []canonicalInvoiceRow
}

func TestTheItemisationSumsToWhatWasCharged(t *testing.T) {
	for _, tt := range []invoiceArithmeticVector{
		{
			name:      "no discount and no delivery",
			items:     []invoiceArithmeticItem{{name: "A", price: 100000, quantity: 1}},
			wantTotal: 100000,
			want:      []canonicalInvoiceRow{{Description: "A", Quantity: 1, UnitPriceCents: 100000, AmountCents: 100000, TaxType: "taxable", Unit: "個", Position: 0}},
		},
		{
			name:     "delivery is its own line",
			items:    []invoiceArithmeticItem{{name: "A", price: 100000, quantity: 1}},
			shipping: 8000, wantTotal: 108000,
			want: []canonicalInvoiceRow{
				{Description: "A", Quantity: 1, UnitPriceCents: 100000, AmountCents: 100000, TaxType: "taxable", Unit: "個", Position: 0},
				{Description: "運費", Quantity: 1, UnitPriceCents: 8000, AmountCents: 8000, TaxType: "taxable", Unit: "個", Position: 1},
			},
		},
		{
			name:     "a discount with free delivery",
			items:    []invoiceArithmeticItem{{name: "A", price: 100000, quantity: 1}},
			discount: 20000, wantTotal: 80000,
			want: []canonicalInvoiceRow{{Description: "A", Quantity: 1, UnitPriceCents: 80000, AmountCents: 80000, TaxType: "taxable", Unit: "個", Position: 0}},
		},
		{
			name:     "a discount smaller than the delivery fee",
			items:    []invoiceArithmeticItem{{name: "A", price: 100000, quantity: 1}},
			discount: 2000, shipping: 8000, wantTotal: 106000,
			want: []canonicalInvoiceRow{
				{Description: "A", Quantity: 1, UnitPriceCents: 98000, AmountCents: 98000, TaxType: "taxable", Unit: "個", Position: 0},
				{Description: "運費", Quantity: 1, UnitPriceCents: 8000, AmountCents: 8000, TaxType: "taxable", Unit: "個", Position: 1},
			},
		},
		{
			name:     "a percentage coupon leaving fractional cents",
			items:    []invoiceArithmeticItem{{name: "A", price: 99900, quantity: 1}},
			discount: 14985, wantTotal: 84900,
			want: []canonicalInvoiceRow{{Description: "A", Quantity: 1, UnitPriceCents: 84900, AmountCents: 84900, TaxType: "taxable", Unit: "個", Position: 0}},
		},
		{
			name: "a discount split across lines",
			items: []invoiceArithmeticItem{
				{name: "A", price: 33300, quantity: 1},
				{name: "B", price: 33300, quantity: 2},
			},
			discount: 10000, shipping: 6000, wantTotal: 95900,
			want: []canonicalInvoiceRow{
				{Description: "A", Quantity: 1, UnitPriceCents: 29900, AmountCents: 29900, TaxType: "taxable", Unit: "個", Position: 0},
				{Description: "B", Quantity: 2, UnitPriceCents: 29900, AmountCents: 59800, TaxType: "taxable", Unit: "個", Position: 1},
				{Description: "運費", Quantity: 1, UnitPriceCents: 6000, AmountCents: 6000, TaxType: "taxable", Unit: "個", Position: 2},
				{Description: "折扣尾數調整", Quantity: 1, UnitPriceCents: 200, AmountCents: 200, TaxType: "taxable", Unit: "個", Position: 3},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) { assertCanonicalInvoiceVector(t, tt) })
	}
}

func TestTheDeliveryFeeIsNeverDiscounted(t *testing.T) {
	assertCanonicalInvoiceVector(t, invoiceArithmeticVector{
		items:    []invoiceArithmeticItem{{name: "A", price: 100000, quantity: 1}},
		discount: 30000, shipping: 8000, wantTotal: 78000,
		want: []canonicalInvoiceRow{
			{Description: "A", Quantity: 1, UnitPriceCents: 70000, AmountCents: 70000, TaxType: "taxable", Unit: "個", Position: 0},
			{Description: "運費", Quantity: 1, UnitPriceCents: 8000, AmountCents: 8000, TaxType: "taxable", Unit: "個", Position: 1},
		},
	})
}

func TestDiscountAllocationDoesNotOverflowAtSchemaLimits(t *testing.T) {
	assertCanonicalInvoiceVector(t, invoiceArithmeticVector{
		items:    []invoiceArithmeticItem{{name: "schema-limit", price: 10000000000, quantity: 999}},
		discount: 9980000000000, wantTotal: 10000000000,
		want: []canonicalInvoiceRow{
			{Description: "schema-limit", Quantity: 999, UnitPriceCents: 10010000, AmountCents: 9999990000, TaxType: "taxable", Unit: "個", Position: 0},
			{Description: "折扣尾數調整", Quantity: 1, UnitPriceCents: 10000, AmountCents: 10000, TaxType: "taxable", Unit: "個", Position: 1},
		},
	})
}

func TestEveryLineMultipliesOut(t *testing.T) {
	for _, tt := range []invoiceArithmeticVector{
		{
			name:     "three at NT$333 with a discount that does not divide",
			items:    []invoiceArithmeticItem{{name: "A", price: 33300, quantity: 3}},
			discount: 10000, wantTotal: 89900,
			want: []canonicalInvoiceRow{
				{Description: "A", Quantity: 3, UnitPriceCents: 29900, AmountCents: 89700, TaxType: "taxable", Unit: "個", Position: 0},
				{Description: "折扣尾數調整", Quantity: 1, UnitPriceCents: 200, AmountCents: 200, TaxType: "taxable", Unit: "個", Position: 1},
			},
		},
		{
			name: "two lines both multi-quantity",
			items: []invoiceArithmeticItem{
				{name: "A", price: 33300, quantity: 3},
				{name: "B", price: 14300, quantity: 7},
			},
			discount: 33333, wantTotal: 166600,
			want: []canonicalInvoiceRow{
				{Description: "A", Quantity: 3, UnitPriceCents: 27700, AmountCents: 83100, TaxType: "taxable", Unit: "個", Position: 0},
				{Description: "B", Quantity: 7, UnitPriceCents: 11900, AmountCents: 83300, TaxType: "taxable", Unit: "個", Position: 1},
				{Description: "折扣尾數調整", Quantity: 1, UnitPriceCents: 200, AmountCents: 200, TaxType: "taxable", Unit: "個", Position: 2},
			},
		},
		{
			name:      "an even division needs no adjustment",
			items:     []invoiceArithmeticItem{{name: "A", price: 50000, quantity: 2}},
			wantTotal: 100000,
			want:      []canonicalInvoiceRow{{Description: "A", Quantity: 2, UnitPriceCents: 50000, AmountCents: 100000, TaxType: "taxable", Unit: "個", Position: 0}},
		},
	} {
		t.Run(tt.name, func(t *testing.T) { assertCanonicalInvoiceVector(t, tt) })
	}
}

func assertCanonicalInvoiceVector(t *testing.T, vector invoiceArithmeticVector) {
	t.Helper()
	ctx := t.Context()
	tx, beginErr := pool.Begin(ctx)
	if beginErr != nil {
		t.Fatalf("begin invoice vector: %v", beginErr)
	}
	defer pgtx.Rollback(ctx, tx)

	var orderID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (shipping_version_id, shipping_method_code, shipping_method_name,
		                    shipping_cents, discount_cents)
		SELECT smv.id, sm.code, smv.name, $1, $2
		FROM shipping_method_versions smv
		JOIN shipping_methods sm ON sm.id = smv.method_id
		LIMIT 1 RETURNING id`, vector.shipping, vector.discount).Scan(&orderID); err != nil {
		t.Fatalf("create invoice vector order: %v", err)
	}
	for position, item := range vector.items {
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity, position)
			VALUES ($1, $2, $2, $3, $4, $5)`, orderID, item.name, item.price, item.quantity, position); err != nil {
			t.Fatalf("create invoice vector item %q: %v", item.name, err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data
		    (order_id, email, recipient_name, phone, postal_code, city, district, street)
		VALUES ($1, 'arithmetic@goen.invalid', '王小明', '0912345678', '110', '台北市', '信義區', '松高路 1 號')`, orderID); err != nil {
		t.Fatalf("create invoice vector delivery snapshot: %v", err)
	}
	if _, err := tx.Exec(ctx, `SET CONSTRAINTS orders_have_lines IMMEDIATE`); err != nil {
		t.Fatalf("validate complete invoice vector order: %v", err)
	}
	plantCanonicalInvoiceArithmetic(t, tx, orderID, vector)
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE store`); err != nil {
		t.Fatalf("read the invoice as store: %v", err)
	}
	var role string
	if err := tx.QueryRow(ctx, `SELECT current_user`).Scan(&role); err != nil || role != "store" {
		t.Fatalf("canonical invoice caller = %q, error %v, want store", role, err)
	}
	rows, err := tx.Query(ctx, `
		SELECT description, quantity, unit_price_cents, amount_cents, tax_type, unit, line_position
		FROM canonical_invoice_lines($1) ORDER BY line_position`, orderID)
	if err != nil {
		t.Fatalf("read canonical invoice vector: %v", err)
	}
	got, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (canonicalInvoiceRow, error) {
		var line canonicalInvoiceRow
		scanErr := row.Scan(&line.Description, &line.Quantity, &line.UnitPriceCents, &line.AmountCents,
			&line.TaxType, &line.Unit, &line.Position)
		return line, scanErr
	})
	if err != nil {
		t.Fatalf("collect canonical invoice vector: %v", err)
	}
	if diff := cmp.Diff(vector.want, got); diff != "" {
		t.Errorf("canonical_invoice_lines(%s) (-want +got):\n%s", orderID, diff)
	}
	var total int64
	for _, line := range got {
		if line.UnitPriceCents*int64(line.Quantity) != line.AmountCents || line.AmountCents <= 0 || line.AmountCents%100 != 0 {
			t.Errorf("canonical invoice line contradicts its quantity or whole-dollar amount: %+v", line)
		}
		total += line.AmountCents
	}
	if total != vector.wantTotal {
		t.Errorf("canonical invoice total = %d, want %d cents", total, vector.wantTotal)
	}
}

func plantCanonicalInvoiceArithmetic(t *testing.T, tx pgx.Tx, orderID uuid.UUID, vector invoiceArithmeticVector) {
	t.Helper()
	var target, replacement string
	switch {
	case t.Name() == "TestDiscountAllocationDoesNotOverflowAtSchemaLimits":
		target = "floor(gross * discount / subtotal)"
		replacement = "floor((gross::bigint * discount::bigint)::numeric / subtotal)"
	case strings.HasPrefix(t.Name(), "TestEveryLineMultipliesOut/"):
		target = "floor(discounted_amount / quantity / 100)"
		replacement = "floor(discounted_amount / 100)"
	default:
		return
	}
	ctx := t.Context()
	if _, baselineRoleErr := tx.Exec(ctx, `SET LOCAL ROLE store`); baselineRoleErr != nil {
		t.Fatalf("read original canonical fixture as store: %v", baselineRoleErr)
	}
	baselineRows, baselineQueryErr := tx.Query(ctx, `
		SELECT description, quantity, unit_price_cents, amount_cents, tax_type, unit, line_position
		FROM canonical_invoice_lines($1) ORDER BY line_position`, orderID)
	if baselineQueryErr != nil {
		t.Fatalf("read original canonical fixture before mutation: %v", baselineQueryErr)
	}
	baseline, baselineCollectErr := pgx.CollectRows(baselineRows, pgx.RowToStructByPos[canonicalInvoiceRow])
	if baselineCollectErr != nil {
		t.Fatalf("collect original canonical fixture before mutation: %v", baselineCollectErr)
	}
	if diff := cmp.Diff(vector.want, baseline); diff != "" {
		t.Fatalf("original canonical fixture failed before mutation (-want +got):\n%s", diff)
	}
	t.Log("original canonical fixture matches all literal rows as store before mutation")
	if _, resetRoleErr := tx.Exec(ctx, `RESET ROLE`); resetRoleErr != nil {
		t.Fatalf("restore fixture owner for transaction-local mutation: %v", resetRoleErr)
	}
	const metadataQuery = `SELECT jsonb_build_object(
		'oid', p.oid, 'owner', p.proowner, 'acl', p.proacl,
		'definer', p.prosecdef, 'configuration', p.proconfig,
		'args', p.proargtypes::text, 'allargs', p.proallargtypes,
		'argmodes', p.proargmodes, 'argnames', p.proargnames,
		'returns', p.prorettype, 'set', p.proretset,
		'language', p.prolang, 'volatility', p.provolatile,
		'strict', p.proisstrict, 'parallel', p.proparallel,
		'leakproof', p.proleakproof, 'cost', p.procost, 'rows', p.prorows,
		'kind', p.prokind, 'support', p.prosupport::text)::text
		FROM pg_catalog.pg_proc p
		WHERE p.oid = 'public.canonical_invoice_lines(uuid)'::regprocedure`
	var before, definition string
	if metadataErr := tx.QueryRow(ctx, metadataQuery).Scan(&before); metadataErr != nil {
		t.Fatalf("read original canonical function attributes: %v", metadataErr)
	}
	if definitionErr := tx.QueryRow(ctx, `SELECT pg_get_functiondef('public.canonical_invoice_lines(uuid)'::regprocedure)`).Scan(&definition); definitionErr != nil {
		t.Fatalf("read actual canonical production definition: %v", definitionErr)
	}
	if count := strings.Count(definition, target); count != 2 {
		t.Fatalf("actual canonical target %q occurs %d times, want 2", target, count)
	}
	mutated := strings.ReplaceAll(definition, target, replacement)
	if _, mutationErr := tx.Exec(ctx, mutated); mutationErr != nil {
		t.Fatalf("plant actual canonical arithmetic expression: %v", mutationErr)
	}
	var after string
	if attributesErr := tx.QueryRow(ctx, metadataQuery).Scan(&after); attributesErr != nil {
		t.Fatalf("read mutated canonical function attributes: %v", attributesErr)
	}
	if before != after {
		t.Fatalf("arithmetic mutation changed canonical identity, privileges or security: before=%s after=%s", before, after)
	}
	t.Logf("planted actual canonical expression %q -> %q in 2 places; identity, privileges and security unchanged", target, replacement)
}
