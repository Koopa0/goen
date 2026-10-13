//go:build integration

package orders_test

import (
	"html"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/pickup"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

func TestDeliveryCorrectionShowsAbsentControlRefusals(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
		for _, tc := range []struct {
			name   string
			pickup bool
			field  string
		}{
			{name: "address with pickup code", field: "pickup_store_code"},
			{name: "pickup with postcode", pickup: true, field: "postal_code"},
		} {
			t.Run(string(locale)+"/"+tc.name, func(t *testing.T) {
				f := pricedDeliveryOrder(t, 0, 0, 0)
				ctx, _ := admintest.StaffContext(t, pool)
				ctx = i18n.WithLocale(ctx, locale)
				values := url.Values{
					"email": {" proposed@example.com "}, "recipient": {" Proposed <recipient> "}, "phone": {"0922333444"},
					"postal_code": {f.oldPostal}, "city": {" New city "}, "district": {" New district "}, "street": {" Proposed <street> "},
				}
				controls := map[string]string{
					"d-email": values.Get("email"), "d-recipient": values.Get("recipient"), "d-phone": values.Get("phone"),
				}
				want := admin.Delivery{Email: "proposed@example.com", Recipient: "Proposed <recipient>", Phone: "0922333444"}
				if tc.pickup {
					setPickupDeliveryDraft(t, ctx, f, true, values)
					for _, field := range []string{"postal_code", "city", "district", "street"} {
						values.Del(field)
					}
					values.Set("pickup_store_name", " Proposed <store> ")
					controls["d-store-code"], controls["d-store-name"] = values.Get("pickup_store_code"), values.Get("pickup_store_name")
					want.PickupChain, want.PickupStoreCode, want.PickupStoreName = pickup.SevenEleven, "A123", "Proposed <store>"
				} else {
					for id, field := range map[string]string{"d-postal": "postal_code", "d-city": "city", "d-district": "district", "d-street": "street"} {
						controls[id] = values.Get(field)
					}
					want.PostalCode, want.City, want.District, want.Street = f.oldPostal, "New city", "New district", "Proposed <street>"
				}
				values.Set(tc.field, "\x01")
				writer := admintest.AdminRolePool(t, pool)
				var role string
				if err := writer.QueryRow(ctx, `SELECT current_user`).Scan(&role); err != nil || role != "admin" {
					t.Fatalf("delivery writer role = %q, want admin: %v", role, err)
				}
				store := admintest.OrderStore(writer, admintest.Refunder{}, nil, nil)
				saved, err := store.Order(ctx, f.number)
				if err != nil {
					t.Fatal(err)
				}
				before := deliveryPrivateSnapshot(t, f.id)
				auditsBefore := deliveryCorrectionAuditCount(t, f.id)
				w := postDeliveryWithStore(ctx, t, store, f.number, values)
				if w.Code != http.StatusUnprocessableEntity || w.Header().Get("Location") != "" {
					t.Fatalf("crafted delivery refusal status = %d location = %q, want 422 without redirect", w.Code, w.Header().Get("Location"))
				}
				body := w.Body.String()
				reason := html.EscapeString(i18n.T(ctx, i18n.KeyFieldHasControlChars))
				if !strings.Contains(body, reason) || !strings.Contains(body, `class="goen-notice__lead"`) || !strings.Contains(body, `role="alert"`) {
					t.Errorf("absent control %q has no localized refusal alert %q", tc.field, reason)
				}
				if strings.Contains(body, `name="`+tc.field+`"`) {
					t.Errorf("refusal introduced opposite destination control %q", tc.field)
				}
				for id, value := range controls {
					tag := deliveryControlTag(t, body, id)
					if !strings.Contains(tag, `value="`+html.EscapeString(value)+`"`) || strings.Contains(tag, "aria-invalid") || strings.Contains(tag, "aria-describedby") {
						t.Errorf("valid control %q lost draft %q or was marked refused: %s", id, value, tag)
					}
				}
				if tc.pickup && !strings.Contains(body, `<option value="seven_eleven" selected`) {
					t.Error("refusal lost submitted pickup chain")
				}
				for _, value := range []string{saved.Recipient, saved.Phone, saved.Email, saved.Address} {
					if !strings.Contains(body, `<dd class="ui-dl__desc">`+html.EscapeString(value)+`</dd>`) {
						t.Errorf("refusal changed saved delivery summary %q", value)
					}
				}
				if after := deliveryPrivateSnapshot(t, f.id); after != before {
					t.Errorf("refusal changed saved private data: before=%s after=%s", before, after)
				}
				if after := deliveryCorrectionAuditCount(t, f.id); after != auditsBefore {
					t.Errorf("refusal wrote correction audits = %d, want %d", after, auditsBefore)
				}
				values.Del(tc.field)
				w = postDeliveryWithStore(ctx, t, store, f.number, values)
				if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/orders/"+f.number+"?ok=1" {
					t.Fatalf("valid recovery status = %d location = %q, want 303 to updated order", w.Code, w.Header().Get("Location"))
				}
				updated, err := store.Order(ctx, f.number)
				if err != nil {
					t.Fatal(err)
				}
				if diff := cmp.Diff(want, updated.Delivery); diff != "" {
					t.Errorf("valid recovery delivery mismatch (-want +got):\n%s", diff)
				}
				if after := deliveryCorrectionAuditCount(t, f.id); after != auditsBefore+1 {
					t.Errorf("valid recovery correction audits = %d, want %d", after, auditsBefore+1)
				}
			})
		}
	}
}

func deliveryCorrectionAuditCount(t *testing.T, orderID uuid.UUID) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE action=$1 AND after->>'order_number'=(SELECT order_number FROM orders WHERE id=$2)`, string(audit.ActionCorrectDelivery), orderID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
