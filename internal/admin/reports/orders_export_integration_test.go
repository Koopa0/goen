//go:build integration

package reports_test

import (
	"encoding/csv"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/reports"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/shoptime"
)

func TestMonthlyOrdersCSVMatchesRevenueAndKeepsEverySoldOrder(t *testing.T) {
	p := admintest.Pool(t)
	ctx, staff := admintest.StaffContext(t, p)
	fixtures := []struct {
		order exportOrderFixture
		row   []string
	}{
		{order: exportOrderFixture{placed: "2024-02-29 23:59:59", paid: "2024-03-01 00:00:01", paidEvent: "2024-03-01 00:00:02", subtotal: 100000, discount: 10000, shipping: 6000, committed: true, invoice: "AB12345678"}, row: []string{"2024-02-29 23:59:59", "2024-03-01 00:00:02", "96000", "10000", "6000", "0", "96000", "AB12345678"}},
		{order: exportOrderFixture{placed: "2024-02-01 00:00:00", paid: "2024-02-01 00:01:00", subtotal: 200000, discount: 10000, shipping: 5000, credit: 50000, committed: true}, row: []string{"2024-02-01 00:00:00", "2024-02-01 00:01:00", "195000", "10000", "5000", "50000", "145000", ""}},
		{order: exportOrderFixture{placed: "2024-02-15 12:00:00", subtotal: 30000, credit: 30000, committed: true}, row: []string{"2024-02-15 12:00:00", "2024-02-15 12:00:00", "30000", "0", "0", "30000", "0", ""}},
		{order: exportOrderFixture{placed: "2024-02-16 12:00:00", committed: true}, row: []string{"2024-02-16 12:00:00", "2024-02-16 12:00:00", "0", "0", "0", "0", "0", ""}},
	}
	want := map[string][]string{}
	for _, fixture := range fixtures {
		number := exportOrder(t, p, fixture.order)
		want[number] = append([]string{number}, fixture.row...)
	}
	// More than the report's top-ten limit: the monthly ledger must be complete.
	for i := range 11 {
		placed := fmt.Sprintf("2024-02-10 10:00:%02d", i)
		number := exportOrder(t, p, exportOrderFixture{placed: placed, paid: placed, subtotal: 100, committed: true})
		want[number] = []string{number, placed, placed, "100", "0", "0", "0", "100", ""}
	}
	for _, placed := range []string{"2024-01-31 23:59:59", "2024-03-01 00:00:00"} {
		exportOrder(t, p, exportOrderFixture{placed: placed, paid: placed, subtotal: 777, committed: true})
	}
	exportOrder(t, p, exportOrderFixture{placed: "2024-02-10 11:00:00", subtotal: 888})
	refunded := exportOrder(t, p, exportOrderFixture{placed: "2024-02-11 11:00:00", paid: "2024-02-11 11:01:00", subtotal: 999, committed: true})
	if _, err := p.Exec(ctx, `SELECT open_refund_before_shipment($1, 'export fixture', $2, $3)`, refunded, staff, "export-refund-"+refunded); err != nil {
		t.Fatalf("refund before shipment: %v", err)
	}
	adminPool := admintest.AdminRolePool(t, p)
	mux := http.NewServeMux()
	reports.NewHandler(reports.NewStore(adminPool), slog.New(slog.DiscardHandler)).Routes(mux, admintest.BackOffice)
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		request := httptest.NewRequestWithContext(i18n.WithLocale(ctx, locale), http.MethodGet, "/admin/reports/orders.csv?month=2024-02", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, request)
		if w.Code != http.StatusOK {
			t.Fatalf("orders CSV status = %d, want 200: %s", w.Code, w.Body.String())
		}
		for key, value := range map[string]string{"Content-Type": "text/csv; charset=utf-8", "Content-Disposition": `attachment; filename="orders-2024-02.csv"`, "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff"} {
			if got := w.Header().Get(key); got != value {
				t.Errorf("orders CSV %s = %q, want %q", key, got, value)
			}
		}
		body := w.Body.String()
		if !strings.HasPrefix(body, "\xEF\xBB\xBF") {
			t.Fatal("orders CSV has no BOM")
		}
		if strings.Contains(body, "private-export@example.com") || strings.Contains(body, "PRIVATE-EXPORT-NOTE") {
			t.Fatal("orders CSV exposed personal data or free text")
		}
		rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(body, "\xEF\xBB\xBF"))).ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 16 {
			t.Fatalf("orders CSV records = %d, want header plus 15 sold orders", len(rows))
		}
		if rows[0][0] != "訂單編號" || rows[0][3] != "訂單總額_cents" {
			t.Fatalf("orders CSV headers changed with locale %s: %v", locale.Tag(), rows[0])
		}
		got := map[string][]string{}
		var total int64
		var previous string
		for _, row := range rows[1:] {
			if len(row) != 9 {
				t.Fatalf("orders CSV row has %d columns, want 9", len(row))
			}
			if _, duplicate := got[row[0]]; duplicate {
				t.Fatalf("orders CSV repeats order %s", row[0])
			}
			got[row[0]] = row
			if row[1] < previous {
				t.Fatalf("orders CSV placement order decreased from %s to %s", previous, row[1])
			}
			previous = row[1]
			cents, parseErr := strconv.ParseInt(row[3], 10, 64)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			total += cents
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("monthly orders CSV mismatch (-want +got):\n%s", diff)
		}
		from, _ := shoptime.ParseSecond("2024-02-01 00:00:00")
		to, _ := shoptime.ParseSecond("2024-03-01 00:00:00")
		revenue, err := db.New(adminPool).RevenueBetween(ctx, db.RevenueBetweenParams{FromAt: from, ToAt: to})
		if err != nil {
			t.Fatal(err)
		}
		if revenue.Orders != 15 || revenue.RevenueCents != 322100 || total != revenue.RevenueCents {
			t.Errorf("monthly CSV/report = %d/%d cents over %d orders, want 322100 cents over 15", total, revenue.RevenueCents, revenue.Orders)
		}
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/reports/orders.csv?month=2023-01", nil))
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(w.Body.String(), "\xEF\xBB\xBF"))).ReadAll()
	if err != nil || w.Code != http.StatusOK || len(rows) != 1 {
		t.Fatalf("empty month = status %d, records %d, error %v; want header only", w.Code, len(rows), err)
	}
}

type exportOrderFixture struct {
	placed, paid, paidEvent              string
	subtotal, discount, shipping, credit int64
	committed                            bool
	invoice                              string
}

func exportOrder(t *testing.T, p *pgxpool.Pool, f exportOrderFixture) (number string) {
	t.Helper()
	ctx := t.Context()
	var buyer uuid.NullUUID
	if f.credit > 0 {
		buyer = uuid.NullUUID{UUID: admintest.CreditedAccount(t, p, f.credit), Valid: true}
	}
	placed, err := shoptime.ParseSecond(f.placed)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer pgtx.Rollback(ctx, tx)
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `
  INSERT INTO orders (order_number,user_id,shipping_version_id,shipping_method_code,shipping_method_name,shipping_cents,discount_cents,placed_at,customer_note)
  SELECT next_order_number(),$1,v.id,sm.code,v.name,$2,$3,$4,'PRIVATE-EXPORT-NOTE'
  FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id=v.method_id
  WHERE sm.code='home_delivery' ORDER BY v.effective_at LIMIT 1 RETURNING id,order_number`, buyer, f.shipping, f.discount, placed).Scan(&id, &number); err != nil {
		t.Fatalf("create export order: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO order_lines (order_id,sku,product_name,unit_price_cents,quantity) VALUES ($1,'EXPORT-ROW','PRIVATE-EXPORT-PRODUCT',$2,1)`, id, f.subtotal); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO order_private_data (order_id,email,recipient_name,phone,postal_code,city,district,street) VALUES ($1,'private-export@example.com','PRIVATE-EXPORT-RECIPIENT','0912345678','110','Taipei','Xinyi','Private Road')`, id); err != nil {
		t.Fatal(err)
	}
	if f.credit > 0 {
		if _, err := tx.Exec(ctx, `SELECT spend_store_credit($1,$2::bigint)`, id, -f.credit); err != nil {
			t.Fatalf("spend export credit: %v", err)
		}
	}
	fundExportOrder(t, tx, id, f)
	if f.invoice != "" {
		if _, err := tx.Exec(ctx, `
  INSERT INTO refunds (payment_id,request_key,provider_ref,status,amount_cents,succeeded_at)
  SELECT id,$2,$3,'succeeded',1000,'2024-03-01 00:04:00+08'
  FROM payments WHERE order_id=$1 AND status='succeeded'`, id, "export-refund-"+id.String(), "re_export_"+id.String()); err != nil {
			t.Fatalf("record export invoice refund: %v", err)
		}
		if _, err := tx.Exec(ctx, `
  WITH voided_invoice AS (
    INSERT INTO invoice_documents (order_id,kind,number,amount_cents,status,issued_at,voided_at)
    VALUES ($1,'invoice','CD12345678',$3,'voided','2024-03-01 00:01:00+08','2024-03-01 00:02:00+08')
  ), reissued_invoice AS (
    INSERT INTO invoice_documents (order_id,kind,number,amount_cents,issued_at)
    VALUES ($1,'invoice',$2,$3,'2024-03-01 00:03:00+08') RETURNING id
  )
  INSERT INTO invoice_documents (order_id,kind,original_id,number,amount_cents,issued_at)
  SELECT $1,'allowance',id,'2024030100000001',1000,'2024-03-01 00:04:00+08' FROM reissued_invoice`, id, f.invoice, f.subtotal-f.discount+f.shipping); err != nil {
			t.Fatalf("record export invoice history: %v", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit export fixture: %v", err)
	}
	return number
}

func fundExportOrder(t *testing.T, tx pgx.Tx, id uuid.UUID, f exportOrderFixture) {
	t.Helper()
	if !f.committed {
		return
	}
	ctx := t.Context()
	total := f.subtotal - f.discount + f.shipping
	if total <= f.credit {
		if _, err := tx.Exec(ctx, `UPDATE orders SET fulfillment_status='picking' WHERE id=$1`, id); err != nil {
			t.Fatalf("commit funded export order: %v", err)
		}
		return
	}
	paid, err := shoptime.ParseSecond(f.paid)
	if err != nil {
		t.Fatal(err)
	}
	if _, paymentErr := tx.Exec(ctx, `INSERT INTO payments (order_id,provider_ref,status,intended_amount_cents,captured_amount_cents,paid_at) VALUES ($1,$2,'succeeded',$3,$3,$4)`, id, "export_"+id.String(), total-f.credit, paid); paymentErr != nil {
		t.Fatalf("record export payment: %v", paymentErr)
	}
	if f.paidEvent == "" {
		return
	}
	paidEvent, err := shoptime.ParseSecond(f.paidEvent)
	if err != nil {
		t.Fatal(err)
	}
	if _, eventErr := tx.Exec(ctx, `INSERT INTO order_events (order_id,kind,occurred_at) VALUES ($1,'paid',$2)`, id, paidEvent); eventErr != nil {
		t.Fatalf("record export paid event: %v", eventErr)
	}
}
