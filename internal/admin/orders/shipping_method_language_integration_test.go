//go:build integration

package orders_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/i18n"
)

// TestHandler_OrderDeliveryMethodLanguage: the order header and the picking
// slip read the purchased delivery method in the reader's language.
func TestHandler_OrderDeliveryMethodLanguage(t *testing.T) {
	for _, tt := range []struct {
		name        string
		english     *string
		wantEnglish string
	}{
		{name: "translated version", english: new("Original delivery"), wantEnglish: "Original delivery"},
		{name: "snapshot fallback", wantEnglish: "下單時的配送快照"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			owner := dbtest.Pool(t)
			staff, _ := admintest.StaffContext(t, owner)
			tx, beginErr := owner.Begin(t.Context())
			if beginErr != nil {
				t.Fatal(beginErr)
			}
			defer db.Rollback(t.Context(), tx)
			code := "language_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			var methodID, versionID, orderID uuid.UUID
			if err := tx.QueryRow(t.Context(), `INSERT INTO shipping_methods(code) VALUES ($1) RETURNING id`, code).Scan(&methodID); err != nil {
				t.Fatal(err)
			}
			if err := tx.QueryRow(t.Context(), `INSERT INTO shipping_method_versions(method_id,name,name_en,fee_cents,effective_at)
				VALUES ($1,'原版本配送',$2,0,now()-interval '1 hour') RETURNING id`, methodID, tt.english).Scan(&versionID); err != nil {
				t.Fatal(err)
			}
			var number string
			if err := tx.QueryRow(t.Context(), `INSERT INTO orders(order_number,shipping_version_id,shipping_method_code,shipping_method_name,shipping_cents)
				VALUES (next_order_number(),$1,$2,'下單時的配送快照',0) RETURNING id,order_number`, versionID, code).Scan(&orderID, &number); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(t.Context(), `INSERT INTO order_lines(order_id,sku,product_name,unit_price_cents,quantity)
				VALUES ($1,'LANGUAGE-ITEM','Delivery language item',0,1)`, orderID); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(t.Context(), `INSERT INTO order_private_data(order_id,email,recipient_name,phone,postal_code,city,district,street)
				VALUES ($1,'delivery@example.test','Recipient','0912345678','110','臺北市','信義區','測試路一號')`, orderID); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(t.Context(), `UPDATE orders SET fulfillment_status='picking' WHERE id=$1`, orderID); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(t.Context(), `INSERT INTO shipping_method_versions(method_id,name,name_en,fee_cents)
				VALUES ($1,'新版本配送','New delivery',0)`, methodID); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
			store := admintest.OrderStore(admintest.AdminRolePool(t, owner), admintest.HealthyGateway(t), nil, nil)
			mux := http.NewServeMux()
			admintest.OrderDesk(store).Routes(mux, admintest.BackOffice)
			for _, locale := range []struct {
				locale i18n.Locale
				want   string
			}{
				{locale: i18n.ZhHant, want: "原版本配送"},
				{locale: i18n.En, want: tt.wantEnglish},
			} {
				ctx := i18n.WithLocale(staff, locale.locale)
				order, err := store.Order(ctx, number)
				if err != nil {
					t.Fatal(err)
				}
				picking, err := store.Picking(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if len(picking.Slips) != 1 || picking.Slips[0].Number != number {
					t.Fatalf("Picking() = %+v, want only %s", picking.Slips, number)
				}
				got := []string{order.ShippingName, picking.Slips[0].ShippingName}
				if diff := cmp.Diff([]string{locale.want, locale.want}, got); diff != "" {
					t.Errorf("%s delivery names (-want +got):\n%s", locale.locale.Tag(), diff)
				}
				for _, path := range []string{"/admin/orders/" + number, "/admin/orders/picking/slips"} {
					response := httptest.NewRecorder()
					mux.ServeHTTP(response, httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil))
					if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), locale.want) || strings.Contains(response.Body.String(), "New delivery") || strings.Contains(response.Body.String(), "新版本配送") {
						t.Errorf("%s %s returned %d without the purchased delivery name %q", locale.locale.Tag(), path, response.Code, locale.want)
					}
				}
			}
			var savedVersion uuid.UUID
			var snapshot string
			if err := owner.QueryRow(t.Context(), `SELECT shipping_version_id,shipping_method_name FROM orders WHERE id=$1`, orderID).Scan(&savedVersion, &snapshot); err != nil {
				t.Fatal(err)
			}
			if savedVersion != versionID || snapshot != "下單時的配送快照" {
				t.Error("localized reads changed the purchased delivery record")
			}
		})
	}
}
