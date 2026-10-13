//go:build integration

package orders_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
)

func TestOpenedRefundSummaryDoesNotOfferPicking(t *testing.T) {
	owner := admintest.Pool(t)
	ctx, actor := admintest.StaffContext(t, owner)
	number, _, _ := admintest.PaidUnshippedOrder(t, owner, 100000, 0, false)
	reader := admintest.AdminRolePool(t, owner)
	store := admintest.OrderStore(reader, admintest.Refunder{}, nil, nil)
	if _, err := db.New(reader).OpenRefundBeforeShipment(ctx, db.OpenRefundBeforeShipmentParams{OrderNumber: number, Reason: "Customer requested cancellation before shipment", ActorUserID: actor, RequestID: "order-summary-refund"}); err != nil {
		t.Fatal(err)
	}
	view, err := store.Order(ctx, number)
	if err != nil {
		t.Fatal(err)
	}
	if !view.RefundOpen || view.Status != order.FulfillmentPending || view.Unpaid {
		t.Fatalf("funded refund projection = status %s open %v unpaid %v", view.Status, view.RefundOpen, view.Unpaid)
	}
	for _, step := range view.Next {
		if step.Value == order.FulfillmentPicking {
			t.Fatal("opened refund still offers picking")
		}
	}
	handler := admintest.OrderDesk(store)
	mux := http.NewServeMux()
	handler.Routes(mux, admintest.BackOffice)
	for _, locale := range i18n.Locales() {
		request := httptest.NewRequestWithContext(i18n.WithLocale(ctx, locale), http.MethodGet, "/admin/orders/"+number, nil)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("opened refund GET = %d, want200", response.Code)
		}
		want := "The refund before shipment is unfinished; choose “Resume the refund” in the refund section."
		if locale == i18n.ZhHant {
			want = "出貨前退款還沒完成，請在退款區按「繼續退款」。"
		}
		if !strings.Contains(response.Body.String(), want) || strings.Contains(response.Body.String(), `id="order-primary-step"`) {
			t.Errorf("%s opened refund summary or pruned primary action is wrong", locale)
		}
	}
}
