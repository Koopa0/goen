//go:build integration

package orders_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/invoicing"
	"github.com/koopa0/goen/internal/invoice"
)

func TestAZeroGrossInvoicePostIsRefusedBeforeTheProviderIsCalled(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	number := admintest.PlaceUnpaidOrder(t, pool)
	if _, err := pool.Exec(ctx, `UPDATE orders SET discount_cents = 500000 WHERE order_number = $1`, number); err != nil {
		t.Fatalf("discount the complete sale: %v", err)
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("zero-gross invoice reached the provider")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(provider.Close)
	gateway, err := invoice.NewGateway("2000132", "ejCk326UnaZWKisg", "q9jcZX8Ib9LM8wYk", provider.URL)
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}
	adminPool := admintest.AdminRolePool(t, pool)
	writer := invoice.NewStore(adminPool, gateway)
	store := invoicing.NewStore(adminPool, writer, writer)
	mux := http.NewServeMux()
	invoicing.NewHandler(store, slog.New(slog.DiscardHandler)).Routes(mux, admintest.BackOffice)
	for range 2 {
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/orders/"+number+"/invoice", nil)
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, req)
		if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/orders/"+number+"?refused=1" {
			t.Errorf("zero-gross Issue = %d %q, want 303 refused notice", res.Code, res.Header().Get("Location"))
		}
	}
	view, err := admintest.OrderStore(adminPool, admintest.Refunder{}, writer, writer).Order(ctx, number)
	if err != nil {
		t.Fatalf("Order: %v", err)
	}
	if view.CanIssueInvoice() {
		t.Error("zero-gross order still offers invoice issuance")
	}
	var operations int
	if countErr := pool.QueryRow(ctx, `SELECT count(*) FROM invoice_operations op JOIN orders o ON o.id = op.order_id WHERE o.order_number = $1`, number).Scan(&operations); countErr != nil {
		t.Fatalf("count invoice operations: %v", countErr)
	}
	if operations != 0 {
		t.Errorf("zero-gross order has %d invoice operations, want 0", operations)
	}
	creditNumber, _ := placeOrderPaidByCredit(t, pool, 0)
	credit, err := admintest.OrderStore(adminPool, admintest.Refunder{}, writer, writer).Order(ctx, creditNumber)
	if err != nil {
		t.Fatalf("read credit-funded order: %v", err)
	}
	if !credit.CanIssueInvoice() || credit.OwedCents != 0 || credit.CreditCents != 5000 {
		t.Errorf("positive credit-funded order lost invoice issuance: offered=%t credit=%d owed=%d", credit.CanIssueInvoice(), credit.CreditCents, credit.OwedCents)
	}
}
