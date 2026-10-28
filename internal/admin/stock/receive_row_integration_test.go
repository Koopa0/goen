//go:build integration

package stock_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/stock"
	"github.com/koopa0/goen/internal/ui/uitest"
)

// TestHandler_ReceiveFromRow: a row of the stock list books a goods receipt,
// once however often the form is posted, and answers on the list at that row.
func TestHandler_ReceiveFromRow(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	mux := http.NewServeMux()
	handlerOver(stock.NewStore(admintest.AdminRolePool(t, pool))).Routes(mux, admintest.BackOffice)
	sku, variant := receiptVariant(t, pool)
	list := "/admin/stock?q=" + url.QueryEscape(sku)

	get := httptest.NewRecorder()
	mux.ServeHTTP(get, httptest.NewRequestWithContext(ctx, http.MethodGet, list, nil))
	if get.Code != http.StatusOK {
		t.Fatalf("stock list = %d, want 200", get.Code)
	}
	key := receiptKey(t, get.Body.String())
	before := receiptState(t, pool, variant)

	zero := postReceipt(ctx, mux, url.Values{"sku": {sku}, "quantity": {"0"}, "idempotency": {uuid.NewString()}, "return": {list}})
	if zero.Code != http.StatusUnprocessableEntity || zero.Header().Get("Location") != "" {
		t.Fatalf("zero receipt = %d %q, want 422 without redirect", zero.Code, zero.Header().Get("Location"))
	}
	uitest.AssertRefusedInput(t, zero.Body.String(), "rcv-"+sku, "0")
	if diff := cmp.Diff(before, receiptState(t, pool, variant)); diff != "" {
		t.Errorf("refused row receipt changed inventory or audit (-want +got):\n%s", diff)
	}

	form := url.Values{"sku": {sku}, "quantity": {"4"}, "idempotency": {key}, "return": {list}}
	for attempt := range 2 {
		accepted := postReceipt(ctx, mux, form)
		location, err := url.Parse(accepted.Header().Get("Location"))
		if accepted.Code != http.StatusSeeOther || err != nil || location.Path != "/admin/stock" ||
			location.Query().Get("received") != "1" || location.Query().Get("q") != sku || location.Fragment != "row-"+sku {
			t.Fatalf("row receipt attempt %d = %d %q, want 303 to the list at row-%s", attempt+1, accepted.Code, accepted.Header().Get("Location"), sku)
		}
	}
	want := receiptSnapshot{Stock: 4, Movements: 1, Receipts: 1, Audits: 1}
	if diff := cmp.Diff(want, receiptState(t, pool, variant)); diff != "" {
		t.Errorf("row receipt posted twice (-want +got):\n%s", diff)
	}
	if strings.Contains(zero.Body.String(), "/admin/stock/adjust") || strings.Contains(zero.Body.String(), "/admin/stock/active") {
		t.Error("the stock list still posts to a write that moved to the SKU page")
	}
}
