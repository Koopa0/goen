//go:build integration

package orders_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/orders"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
)

// TestHandler_CorrectDeliveryCancelled: a cancelled order cannot have its
// delivery corrected, through the page, the POST, the store or the SQL.
func TestHandler_CorrectDeliveryCancelled(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	writer := admintest.AdminRolePool(t, pool)
	store := admintest.OrderStore(writer, admintest.HealthyGateway(t), nil)
	mux := http.NewServeMux()
	admintest.OrderDesk(store).Routes(mux, admintest.BackOffice)

	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			localeCtx := i18n.WithLocale(ctx, locale)
			number := admintest.PlaceUnpaidOrder(t, pool)
			var orderID uuid.UUID
			if err := pool.QueryRow(localeCtx, `SELECT id FROM orders WHERE order_number = $1`, number).Scan(&orderID); err != nil {
				t.Fatal(err)
			}
			values := url.Values{
				"email": {"corrected@example.com"}, "recipient": {"Corrected recipient"}, "phone": {"0922333444"},
				"postal_code": {"110"}, "city": {"台北市"}, "district": {"信義區"}, "street": {"Corrected street 2"},
			}
			positive := postDeliveryWithStore(localeCtx, t, store, number, values)
			if positive.Code != http.StatusSeeOther {
				t.Fatalf("pending delivery correction = %d, want 303", positive.Code)
			}
			if got := streetOf(t, number); got != values.Get("street") {
				t.Fatalf("pending correction saved street = %q, want %q", got, values.Get("street"))
			}
			if got := deliveryCorrectionAuditCount(t, orderID); got != 1 {
				t.Fatalf("pending correction audit count = %d, want 1", got)
			}
			if _, err := store.Advance(localeCtx, number, order.FulfillmentCancelled, uuid.NullUUID{UUID: staff, Valid: true}); err != nil {
				t.Fatal(err)
			}
			before := deliveryPrivateSnapshot(t, orderID)
			audits := deliveryCorrectionAuditCount(t, orderID)
			get := httptest.NewRecorder()
			mux.ServeHTTP(get, httptest.NewRequestWithContext(localeCtx, http.MethodGet, "/admin/orders/"+number, nil))
			if get.Code != http.StatusOK || strings.Contains(get.Body.String(), `action="/admin/orders/`+number+`/delivery"`) {
				t.Fatalf("cancelled order GET = %d, want its page without a delivery form", get.Code)
			}
			values.Set("street", "A change after cancellation")
			rec := postDeliveryWithStore(localeCtx, t, store, number, values)
			if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/orders/"+number+"?refused=1" {
				t.Fatalf("cancelled delivery correction = %d %q, want 303 with the existing refusal", rec.Code, rec.Header().Get("Location"))
			}
			get = httptest.NewRecorder()
			mux.ServeHTTP(get, httptest.NewRequestWithContext(localeCtx, http.MethodGet, rec.Header().Get("Location"), nil))
			if !strings.Contains(get.Body.String(), i18n.T(localeCtx, i18n.KeyAdminNoticeRefused)) {
				t.Error("cancelled correction did not show the existing refusal notice")
			}
			correction := &orders.DeliveryCorrection{Email: values.Get("email"), Recipient: values.Get("recipient"), Phone: values.Get("phone"),
				PostalCode: values.Get("postal_code"), City: values.Get("city"), District: values.Get("district"), Street: values.Get("street")}
			if err := store.CorrectDelivery(localeCtx, number, correction); !errors.Is(err, orders.ErrRefused) {
				t.Errorf("store correction after cancellation = %v, want refusal", err)
			}
			changed, err := db.New(writer).UpdateOrderDelivery(localeCtx, db.UpdateOrderDeliveryParams{
				OrderNumber:   number,
				Email:         pgtype.Text{String: values.Get("email"), Valid: true},
				RecipientName: pgtype.Text{String: values.Get("recipient"), Valid: true},
				Phone:         pgtype.Text{String: values.Get("phone"), Valid: true},
				PostalCode:    values.Get("postal_code"), City: values.Get("city"), District: values.Get("district"), Street: values.Get("street"),
			})
			if err != nil || changed != 0 {
				t.Errorf("cancelled SQL delivery correction = %d, %v, want no changed row", changed, err)
			}
			if after := deliveryPrivateSnapshot(t, orderID); after != before {
				t.Error("cancelled delivery correction changed saved private data")
			}
			if after := deliveryCorrectionAuditCount(t, orderID); after != audits {
				t.Errorf("cancelled delivery correction audit count = %d, want %d", after, audits)
			}
		})
	}
}
