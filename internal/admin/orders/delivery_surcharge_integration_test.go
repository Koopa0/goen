//go:build integration

package orders_test

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/orders"
	"github.com/koopa0/goen/internal/i18n"
)

type deliveryPriceOrder struct {
	number                         string
	id, version, method            uuid.UUID
	oldZone, newZone               uuid.UUID
	oldPostal, newPostal, siblingP string
}

func pricedDeliveryOrder(t *testing.T, oldRate, newRate, chargedShipping int64) deliveryPriceOrder {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var f deliveryPriceOrder
	code := "correction_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err = tx.QueryRow(ctx, `INSERT INTO shipping_methods(code) VALUES ($1) RETURNING id`, code).Scan(&f.method); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO shipping_method_versions(method_id,name,fee_cents,free_over_cents) VALUES($1,'Correction shipping',7000,100000) RETURNING id`, f.method).Scan(&f.version); err != nil {
		t.Fatal(err)
	}
	var prefixes []string
	if err = tx.QueryRow(ctx, `SELECT array_agg(prefix) FROM (SELECT n::text AS prefix FROM generate_series(100,999) n WHERE NOT EXISTS (SELECT 1 FROM shipping_zone_prefixes z WHERE z.prefix=n::text) ORDER BY n DESC LIMIT 3) p`).Scan(&prefixes); err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 3 {
		t.Fatal("need three unused postcode prefixes")
	}
	f.oldPostal, f.newPostal, f.siblingP = prefixes[0], prefixes[1], prefixes[2]
	for i, rate := range []int64{oldRate, newRate} {
		var zone uuid.UUID
		if err = tx.QueryRow(ctx, `INSERT INTO shipping_zones(code,name) VALUES($1,'Correction zone') RETURNING id`, fmt.Sprintf("%s_%d", code, i)).Scan(&zone); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO shipping_zone_prefixes(prefix,zone_id) VALUES($1,$2)`, prefixes[i], zone); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			f.oldZone = zone
			// A second prefix of the same zone: the correction that must stay legal.
			if _, err = tx.Exec(ctx, `INSERT INTO shipping_zone_prefixes(prefix,zone_id) VALUES($1,$2)`, f.siblingP, zone); err != nil {
				t.Fatal(err)
			}
		} else {
			f.newZone = zone
		}
		if rate > 0 {
			if _, err = tx.Exec(ctx, `INSERT INTO shipping_version_zones(version_id,zone_id,surcharge_cents) VALUES($1,$2,$3)`, f.version, zone, rate); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = tx.QueryRow(ctx, `INSERT INTO orders(order_number,shipping_version_id,shipping_method_code,shipping_method_name,shipping_cents) VALUES(next_order_number(),$1,$2,'Correction shipping',$3) RETURNING id,order_number`, f.version, code, chargedShipping).Scan(&f.id, &f.number); err != nil {
		t.Fatal(err)
	}
	var variant uuid.UUID
	if err = tx.QueryRow(ctx, `INSERT INTO order_lines(order_id,variant_id,sku,product_name,unit_price_cents,quantity) SELECT $1,v.id,v.sku,p.name,100000,1 FROM product_variants v JOIN products p ON p.id=v.product_id WHERE v.is_active AND p.status='active' AND v.stock_quantity-v.safety_stock>2 LIMIT 1 RETURNING variant_id`, f.id).Scan(&variant); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT hold_inventory($1,$2,1,interval '1 hour',$3)`, f.id, variant, "correction-hold:"+f.number); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO order_private_data(order_id,email,recipient_name,phone,postal_code,city,district,street) VALUES($1,'saved@example.com','Saved recipient','0912345678',$2,'City','District','Saved street')`, f.id, f.oldPostal); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO invoice_preferences(order_id,invoice_type,customer_name,customer_email) VALUES($1,'member_carrier','Saved recipient','saved@example.com')`, f.id); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT open_payment($1,$2,$3)`, f.id, "cs_correction_"+f.number, 100000+chargedShipping); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT capture_payment($1,$2,NULL,NULL)`, "cs_correction_"+f.number, 100000+chargedShipping); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE orders SET fulfillment_status='picking' WHERE id=$1`, f.id); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.WithoutCancel(ctx), `DELETE FROM shipping_zone_prefixes WHERE prefix=ANY($1::text[])`, prefixes); err != nil {
			t.Error(err)
		}
	})
	return f
}

func proposedDelivery(postal string) *orders.DeliveryCorrection {
	return &orders.DeliveryCorrection{Email: "proposed@example.com", Recipient: "Proposed recipient", Phone: "0922333444", PostalCode: postal, City: "New city", District: "New district", Street: "Proposed street"}
}

func deliveryMoneySnapshot(t *testing.T, id uuid.UUID) string {
	t.Helper()
	var snapshot string
	err := pool.QueryRow(t.Context(), `SELECT jsonb_build_object('shipping',o.shipping_cents,'discount',o.discount_cents,'tax',o.tax_cents,'owed',order_amount_after_credit(o.id),'committed',order_is_committed(o.id),'payments',(SELECT jsonb_agg(to_jsonb(p) ORDER BY p.id) FROM payments p WHERE p.order_id=o.id),'invoices',(SELECT jsonb_agg(to_jsonb(d) ORDER BY d.id) FROM invoice_documents d WHERE d.order_id=o.id),'operations',(SELECT jsonb_agg(to_jsonb(op) ORDER BY op.id) FROM invoice_operations op WHERE op.order_id=o.id),'credit',(SELECT jsonb_agg(to_jsonb(e) ORDER BY e.id) FROM store_credit_entries e WHERE e.order_id=o.id))::text FROM orders o WHERE o.id=$1`, id).Scan(&snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func zoneRefusal(t *testing.T, err error, want i18n.Key) {
	t.Helper()
	refused, ok := errors.AsType[*orders.DeliveryPostalError](err)
	if !ok || refused.Key != want {
		t.Fatalf("correction error=%v, want a postcode refusal %q", err, want)
	}
}

func TestDeliveryCorrectionRefusesEveryCrossZoneMove(t *testing.T) {
	// Equal surcharges are still different zones: the rule is zone identity.
	for _, tc := range []struct {
		name             string
		oldRate, newRate int64
	}{
		{"increase", 0, 10000}, {"decrease", 10000, 0}, {"equal surcharges", 10000, 10000}, {"neither surcharged", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := pricedDeliveryOrder(t, tc.oldRate, tc.newRate, tc.oldRate)
			ctx, actor := admintest.StaffContext(t, pool)
			if _, err := pool.Exec(ctx, `SELECT claim_invoice_issue($1,$2,$3)`, f.number, actor, "correction-"+uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			before := deliveryMoneySnapshot(t, f.id)
			err := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil).CorrectDelivery(ctx, f.number, proposedDelivery(f.newPostal))
			zoneRefusal(t, err, i18n.KeyDeliveryZoneChanged)
			if got := streetOf(t, f.number); got != "Saved street" {
				t.Fatalf("refusal saved %q", got)
			}
			if got := deliveryMoneySnapshot(t, f.id); got != before {
				t.Fatalf("money changed after refusal:\n%s\n%s", before, got)
			}
		})
	}
}

// The zone is what the order was priced in; the amount on its row is editable
// afterwards, and zero deletes the row. Neither may move the boundary.
func TestEditingASurchargeAfterTheOrderNeverOpensACrossZoneCorrection(t *testing.T) {
	t.Run("island surcharge set to 0 after the order", func(t *testing.T) {
		f := pricedDeliveryOrder(t, 10000, 0, 10000)
		ctx, _ := admintest.StaffContext(t, pool)
		// The destination is the mainland: a postcode in no zone at all.
		if _, err := pool.Exec(ctx, `DELETE FROM shipping_zone_prefixes WHERE prefix=$1`, f.newPostal); err != nil {
			t.Fatal(err)
		}
		// Setting a surcharge to 0 deletes its row, as the shipping page does.
		if _, err := pool.Exec(ctx, `DELETE FROM shipping_version_zones WHERE version_id=$1`, f.version); err != nil {
			t.Fatal(err)
		}
		err := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil).CorrectDelivery(ctx, f.number, proposedDelivery(f.newPostal))
		zoneRefusal(t, err, i18n.KeyDeliveryZoneChanged)
		if got := streetOf(t, f.number); got != "Saved street" {
			t.Fatalf("refusal saved %q", got)
		}
	})
	t.Run("surcharge raised after the order", func(t *testing.T) {
		f := pricedDeliveryOrder(t, 0, 0, 0)
		ctx, _ := admintest.StaffContext(t, pool)
		if _, err := pool.Exec(ctx, `INSERT INTO shipping_version_zones(version_id,zone_id,surcharge_cents) VALUES($1,$2,15000)`, f.version, f.oldZone); err != nil {
			t.Fatal(err)
		}
		if err := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil).CorrectDelivery(ctx, f.number, proposedDelivery(f.siblingP)); err != nil {
			t.Fatalf("same-zone correction refused after a rate edit: %v", err)
		}
	})
}

func TestSameZoneCorrectionRetainsFrozenPriceAfterMethodRetires(t *testing.T) {
	f := pricedDeliveryOrder(t, 10000, 10000, 0)
	ctx, _ := admintest.StaffContext(t, pool)
	if _, err := pool.Exec(ctx, `UPDATE shipping_methods SET is_active=false WHERE id=$1`, f.method); err != nil {
		t.Fatal(err)
	}
	before := deliveryMoneySnapshot(t, f.id)
	s := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)
	for _, postal := range []string{f.oldPostal, f.siblingP, f.siblingP + "123"} {
		if err := s.CorrectDelivery(ctx, f.number, proposedDelivery(postal)); err != nil {
			t.Fatal(err)
		}
	}
	if got := streetOf(t, f.number); got != "Proposed street" {
		t.Fatal(got)
	}
	if got := deliveryMoneySnapshot(t, f.id); got != before {
		t.Fatal("same-zone correction changed frozen money")
	}
}

func TestDeliveryCorrectionCannotPlaceAnAbsentOriginalPostcode(t *testing.T) {
	f := pricedDeliveryOrder(t, 0, 10000, 0)
	ctx, _ := admintest.StaffContext(t, pool)
	// Both destination groups are individually legal in storage. The method is
	// still address delivery, so a missing original postcode is not the mainland.
	if _, err := pool.Exec(ctx, `UPDATE order_private_data SET postal_code=NULL,city=NULL,district=NULL,street=NULL,pickup_chain='family_mart' WHERE order_id=$1`, f.id); err != nil {
		t.Fatal(err)
	}
	err := admintest.OrderStore(pool, admintest.Refunder{}, nil, nil).CorrectDelivery(ctx, f.number, proposedDelivery(f.newPostal))
	zoneRefusal(t, err, i18n.KeyDeliveryZoneUnknown)
}

func postDeliveryCorrection(ctx context.Context, t *testing.T, number, postal string) *httptest.ResponseRecorder {
	t.Helper()
	values := url.Values{"email": {"proposed@example.com"}, "recipient": {"Proposed recipient"}, "phone": {"0922333444"}, "postal_code": {postal}, "city": {"New city"}, "district": {"New district"}, "street": {"Proposed street"}}
	r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/orders/"+number+"/delivery", strings.NewReader(values.Encode()))
	r.SetPathValue("number", number)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	admintest.OrderDesk(admintest.OrderStore(pool, admintest.Refunder{}, nil, nil)).CorrectDelivery(w, r)
	return w
}

func TestDeliveryZoneRefusalPreservesFormInBothLanguages(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
		t.Run(string(locale), func(t *testing.T) {
			f := pricedDeliveryOrder(t, 10000, 10000, 10000)
			ctx, _ := admintest.StaffContext(t, pool)
			ctx = i18n.WithLocale(ctx, locale)
			w := postDeliveryCorrection(ctx, t, f.number, f.newPostal)
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			body := w.Body.String()
			for _, want := range []string{`value="` + f.newPostal + `"`, `value="Proposed street"`, `value="proposed@example.com"`, `aria-invalid="true"`, `aria-describedby="d-postal-error"`, `id="d-postal-error"`, html.EscapeString(i18n.T(ctx, i18n.KeyDeliveryZoneChanged))} {
				if !strings.Contains(body, want) {
					t.Errorf("missing %q", want)
				}
			}
			if got := streetOf(t, f.number); got != "Saved street" {
				t.Fatal("refused form saved address")
			}
		})
	}
}

func waitForDeliveryLock(t *testing.T, blockerPID uint32) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiting bool
		if err := pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)) AND query LIKE '%LockOrderDelivery%')`, int64(blockerPID)).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("correction did not wait for order lock")
}

func TestDeliveryCorrectionRechecksTerminalStateAfterLock(t *testing.T) {
	for _, state := range []string{"shipped"} {
		t.Run(state, func(t *testing.T) {
			f := pricedDeliveryOrder(t, 0, 0, 0)
			ctx, _ := admintest.StaffContext(t, pool)
			blocker, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
			var id uuid.UUID
			if err = blocker.QueryRow(ctx, `SELECT id FROM orders WHERE id=$1 FOR UPDATE`, f.id).Scan(&id); err != nil {
				t.Fatal(err)
			}
			workerCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- admintest.OrderStore(pool, admintest.Refunder{}, nil, nil).CorrectDelivery(workerCtx, f.number, proposedDelivery(f.newPostal))
			}()
			waitForDeliveryLock(t, blocker.Conn().PgConn().PID())
			if _, err = blocker.Exec(ctx, `UPDATE orders SET fulfillment_status=$2 WHERE id=$1`, f.id, state); err != nil {
				t.Fatal(err)
			}
			if err = blocker.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err = <-done; !errors.Is(err, orders.ErrTooLateToCorrect) {
				t.Fatalf("error=%v", err)
			}
			if got := streetOf(t, f.number); got != "Saved street" {
				t.Fatal("terminal order changed")
			}
		})
	}
}

func TestDeliveryCorrectionReadsPostalAfterWaitingForPriorCorrection(t *testing.T) {
	f := pricedDeliveryOrder(t, 0, 10000, 0)
	ctx, _ := admintest.StaffContext(t, pool)
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	var id uuid.UUID
	if err = blocker.QueryRow(ctx, `SELECT id FROM orders WHERE id=$1 FOR UPDATE`, f.id).Scan(&id); err != nil {
		t.Fatal(err)
	}
	workerCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- admintest.OrderStore(pool, admintest.Refunder{}, nil, nil).CorrectDelivery(workerCtx, f.number, proposedDelivery(f.newPostal+"123"))
	}()
	waitForDeliveryLock(t, blocker.Conn().PgConn().PID())
	if _, err = blocker.Exec(ctx, `UPDATE order_private_data SET postal_code=$2 WHERE order_id=$1`, f.id, f.newPostal); err != nil {
		t.Fatal(err)
	}
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatalf("did not read committed predecessor address: %v", err)
	}
}

func TestMalformedDeliveryPostcodeIsAPostcodeError(t *testing.T) {
	f := pricedDeliveryOrder(t, 0, 10000, 0)
	ctx, _ := admintest.StaffContext(t, pool)
	ctx = i18n.WithLocale(ctx, i18n.En)
	w := postDeliveryCorrection(ctx, t, f.number, "12a")
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{`value="12a"`, `value="Proposed street"`, `aria-invalid="true"`, `aria-describedby="d-postal-error"`, html.EscapeString(i18n.T(ctx, i18n.KeyPostalCodeMalformed))} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, html.EscapeString(i18n.T(ctx, i18n.KeyDeliveryZoneChanged))) || strings.Contains(body, html.EscapeString(i18n.T(ctx, i18n.KeyDeliveryZoneUnknown))) {
		t.Error("a malformed postcode was reported as a zone problem")
	}
	if got := streetOf(t, f.number); got != "Saved street" {
		t.Fatal("malformed postcode changed saved address")
	}
}
