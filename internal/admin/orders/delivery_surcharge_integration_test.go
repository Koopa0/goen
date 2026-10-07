//go:build integration

package orders_test

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/orders"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/pickup"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/ui/pages/admin"
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
	return postDeliveryValues(ctx, t, number, values)
}

func postDeliveryValues(ctx context.Context, t *testing.T, number string, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	s := admintest.OrderStore(admintest.AdminRolePool(t, pool), admintest.Refunder{}, nil, nil)
	return postDeliveryWithStore(ctx, t, s, number, values)
}

func postDeliveryWithStore(ctx context.Context, t *testing.T, s *orders.Store, number string, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/orders/"+number+"/delivery", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux := http.NewServeMux()
	admintest.OrderDesk(s).Routes(mux, access.New(slog.New(slog.DiscardHandler), nil))
	mux.ServeHTTP(w, r)
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

func TestDeliveryCorrectionNonPostalRefusalKeepsDraftAndSavedSummary(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
		for _, tc := range []struct {
			name    string
			pickup  bool
			errorID string
			key     i18n.Key
		}{
			{name: "short phone", errorID: "d-phone", key: i18n.KeyPhoneMalformed},
			{name: "pickup code without name", pickup: true, errorID: "d-store-name", key: i18n.KeyAddressIncomplete},
			{name: "pickup name without code", pickup: true, errorID: "d-store-code", key: i18n.KeyStoreCodeMalformed},
		} {
			t.Run(string(locale)+"/"+tc.name, func(t *testing.T) {
				f := pricedDeliveryOrder(t, 0, 0, 0)
				ctx, _ := admintest.StaffContext(t, pool)
				ctx = i18n.WithLocale(ctx, locale)
				values := url.Values{
					"email": {" proposed@example.com "}, "recipient": {" Proposed recipient "}, "phone": {"0922333444"},
					"postal_code": {f.oldPostal}, "city": {"New city"}, "district": {"New district"}, "street": {" Proposed <street> "},
				}
				controls := map[string]string{"d-email": values.Get("email"), "d-recipient": values.Get("recipient")}
				if tc.pickup {
					if _, err := pool.Exec(ctx, `UPDATE shipping_methods SET destination_kind='pickup_point' WHERE id=$1`, f.method); err != nil {
						t.Fatal(err)
					}
					if _, err := pool.Exec(ctx, `UPDATE order_private_data SET postal_code=NULL,city=NULL,district=NULL,street=NULL,pickup_chain='family_mart',pickup_store_code='SAVED1',pickup_store_name='Saved store' WHERE order_id=$1`, f.id); err != nil {
						t.Fatal(err)
					}
					values.Set("pickup_chain", string(pickup.SevenEleven))
					if tc.errorID == "d-store-name" {
						values.Set("pickup_store_code", " a123 ")
					} else {
						values.Set("pickup_store_name", " Proposed store ")
					}
					controls["d-store-code"], controls["d-store-name"] = values.Get("pickup_store_code"), values.Get("pickup_store_name")
				} else {
					values.Set("phone", " 123 ")
					for id, field := range map[string]string{"d-postal": "postal_code", "d-city": "city", "d-district": "district", "d-street": "street"} {
						controls[id] = values.Get(field)
					}
				}
				controls["d-phone"] = values.Get("phone")
				store := admintest.OrderStore(admintest.AdminRolePool(t, pool), admintest.Refunder{}, nil, nil)
				saved, err := store.Order(ctx, f.number)
				if err != nil {
					t.Fatal(err)
				}
				before := deliveryPrivateSnapshot(t, f.id)
				w := postDeliveryWithStore(ctx, t, store, f.number, values)
				if w.Code != http.StatusUnprocessableEntity || w.Header().Get("Location") != "" {
					t.Fatalf("refused correction status=%d location=%q, want 422 without redirect", w.Code, w.Header().Get("Location"))
				}
				body := w.Body.String()
				for id, value := range controls {
					tag := deliveryControlTag(t, body, id)
					if !strings.Contains(tag, `value="`+html.EscapeString(value)+`"`) {
						t.Errorf("%s lost draft %q: %s", id, value, tag)
					}
					if id == tc.errorID {
						if !strings.Contains(tag, `aria-invalid="true"`) || !strings.Contains(tag, `aria-describedby="`+id+`-error"`) {
							t.Errorf("refused %s has no own error association: %s", id, tag)
						}
					} else if strings.Contains(tag, "aria-invalid") || strings.Contains(tag, "aria-describedby") {
						t.Errorf("valid %s is marked refused: %s", id, tag)
					}
				}
				if !strings.Contains(body, `<p id="`+tc.errorID+`-error" class="ui-error-text" role="alert">`+html.EscapeString(i18n.T(ctx, tc.key))+`</p>`) {
					t.Error("refused control lost its localized reason")
				}
				if tc.pickup && !strings.Contains(body, `<option value="seven_eleven" selected`) {
					t.Error("refusal lost submitted pickup chain")
				}
				for _, value := range []string{saved.Recipient, saved.Phone, saved.Email, saved.Address} {
					if !strings.Contains(body, `<dd class="ui-dl__desc">`+html.EscapeString(value)+`</dd>`) {
						t.Errorf("refused correction changed saved summary %q", value)
					}
				}
				if after := deliveryPrivateSnapshot(t, f.id); after != before {
					t.Fatalf("refused correction changed saved private data: before=%s after=%s", before, after)
				}
				values.Set("phone", "0922333444")
				if tc.pickup {
					values.Set("pickup_store_code", "a123")
					values.Set("pickup_store_name", " Proposed store ")
				}
				w = postDeliveryWithStore(ctx, t, store, f.number, values)
				if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/orders/"+f.number+"?ok=1" {
					t.Fatalf("corrected delivery status=%d location=%q", w.Code, w.Header().Get("Location"))
				}
				updated, err := store.Order(ctx, f.number)
				if err != nil {
					t.Fatal(err)
				}
				want := admin.Delivery{Recipient: "Proposed recipient", Email: "proposed@example.com", Phone: "0922333444"}
				if tc.pickup {
					want.PickupChain, want.PickupStoreCode, want.PickupStoreName = pickup.SevenEleven, "A123", "Proposed store"
				} else {
					want.PostalCode, want.City, want.District, want.Street = f.oldPostal, "New city", "New district", "Proposed <street>"
				}
				if diff := cmp.Diff(want, updated.Delivery); diff != "" {
					t.Fatalf("successful normalized delivery mismatch (-want +got):\n%s", diff)
				}
			})
		}
	}
}

func deliveryPrivateSnapshot(t *testing.T, id uuid.UUID) string {
	t.Helper()
	var snapshot string
	if err := pool.QueryRow(t.Context(), `SELECT to_jsonb(pd)::text FROM order_private_data pd WHERE order_id=$1`, id).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestDeliveryCorrectionStoreFailureDoesNotRefuseAField(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
		t.Run(string(locale), func(t *testing.T) {
			f := pricedDeliveryOrder(t, 0, 0, 0)
			ctx, _ := admintest.StaffContext(t, pool)
			ctx = i18n.WithLocale(ctx, locale)
			adminPool := admintest.AdminRolePool(t, pool)
			cfg := adminPool.Config().Copy()
			cfg.ConnConfig.RuntimeParams["lock_timeout"] = "100ms"
			lockedPool, err := pgxpool.NewWithConfig(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(lockedPool.Close)
			var role, timeout string
			if err := lockedPool.QueryRow(ctx, `SELECT current_user, current_setting('lock_timeout')`).Scan(&role, &timeout); err != nil || role != "admin" || timeout != "100ms" {
				t.Fatalf("lock-fault pool role=%q timeout=%q, want admin/100ms: %v", role, timeout, err)
			}
			store := admintest.OrderStore(lockedPool, admintest.Refunder{}, nil, nil)
			before := deliveryPrivateSnapshot(t, f.id)
			countAudits := func() int {
				t.Helper()
				var count int
				if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE after->>'order_number'=$1 AND action=$2`, f.number, string(audit.ActionCorrectDelivery)).Scan(&count); err != nil {
					t.Fatal(err)
				}
				return count
			}
			auditsBefore := countAudits()
			lock, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer pgtx.Rollback(ctx, lock)
			if _, err := lock.Exec(ctx, `SELECT id FROM orders WHERE id=$1 FOR UPDATE`, f.id); err != nil {
				t.Fatal(err)
			}
			values := url.Values{
				"email": {"proposed@example.com"}, "recipient": {"Proposed recipient"}, "phone": {"0922333444"},
				"postal_code": {f.oldPostal}, "city": {"New city"}, "district": {"New district"}, "street": {"Proposed street"},
			}
			err = store.CorrectDelivery(ctx, f.number, &orders.DeliveryCorrection{
				Email: "proposed@example.com", Recipient: "Proposed recipient", Phone: "0922333444",
				PostalCode: f.oldPostal, City: "New city", District: "New district", Street: "Proposed street",
			})
			fault, ok := errors.AsType[*pgconn.PgError](err)
			if !ok || fault.Code != "55P03" || ctx.Err() != nil {
				t.Fatalf("locked delivery error=%v context=%v, want PostgreSQL 55P03 with live request context", err, ctx.Err())
			}
			w := postDeliveryWithStore(ctx, t, store, f.number, values)
			if ctx.Err() != nil {
				t.Fatalf("lock-fault POST canceled request context: %v", ctx.Err())
			}
			if w.Code != http.StatusInternalServerError || w.Header().Get("Location") != "" {
				t.Fatalf("store failure status=%d location=%q, want 500 without redirect", w.Code, w.Header().Get("Location"))
			}
			if strings.Contains(w.Body.String(), `aria-invalid="true"`) || strings.Contains(w.Body.String(), `id="d-phone-error"`) {
				t.Fatal("store failure was presented as a field refusal")
			}
			if after := deliveryPrivateSnapshot(t, f.id); after != before || countAudits() != auditsBefore {
				t.Fatalf("store failure changed private data or correction audit: before=%s after=%s", before, after)
			}
			if err := lock.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			w = postDeliveryWithStore(ctx, t, store, f.number, values)
			if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/orders/"+f.number+"?ok=1" {
				t.Fatalf("recovery status=%d location=%q, want successful correction", w.Code, w.Header().Get("Location"))
			}
			if deliveryPrivateSnapshot(t, f.id) == before || countAudits() != auditsBefore+1 {
				t.Fatal("recovery did not persist exactly one audited correction")
			}
		})
	}
}

func deliveryControlTag(t *testing.T, body, id string) string {
	t.Helper()
	at := strings.Index(body, `id="`+id+`"`)
	if at < 0 {
		t.Fatalf("no delivery control %q", id)
	}
	start := strings.LastIndex(body[:at], "<")
	end := strings.Index(body[at:], ">")
	if start < 0 || end < 0 {
		t.Fatalf("delivery control %q has no complete opening tag", id)
	}
	return body[start : at+end+1]
}
