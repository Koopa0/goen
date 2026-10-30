//go:build integration

package buyer_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/order/buyer"
)

// TestStore_OrderReturnRequests: Store.Order reports whether the order has a return request
// nobody has decided and whether it has any, through the queries the return form uses, so the
// page's link and the POST agree about an open request.
func TestStore_OrderReturnRequests(t *testing.T) {
	ctx := t.Context()
	s := buyer.NewStore(pool)

	none, _ := shippedOrder(t, 2, 2)
	open, openLine := shippedOrder(t, 2, 2)
	decided, decidedLine := shippedOrder(t, 2, 2)
	for number, line := range map[string]uuid.UUID{open: openLine, decided: decidedLine} {
		if err := s.OpenReturn(ctx, number, uuid.NullUUID{}, &buyer.ReturnRequest{
			Reason: "不合用", Lines: map[string]int32{line.String(): 1},
		}); err != nil {
			t.Fatalf("open a return on %s: %v", number, err)
		}
	}
	if _, err := pool.Exec(ctx, `
		UPDATE return_requests SET status = 'rejected', decided_at = now()
		WHERE order_id = (SELECT id FROM orders WHERE order_number = $1)`, decided); err != nil {
		t.Fatalf("decide the request: %v", err)
	}

	for _, tt := range []struct {
		name         string
		number       string
		wantOpen     bool
		wantHasAny   bool
		wantCanAsk   bool
		wantViewLink bool
	}{
		{"no request", none, false, false, true, false},
		{"open request", open, true, true, false, true},
		{"decided request", decided, false, true, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			view, err := s.Order(ctx, tt.number)
			if err != nil {
				t.Fatalf("Order: %v", err)
			}
			if view.OpenReturn != tt.wantOpen || view.HasReturn != tt.wantHasAny {
				t.Errorf("OpenReturn, HasReturn = %v, %v, want %v, %v", view.OpenReturn, view.HasReturn, tt.wantOpen, tt.wantHasAny)
			}
			if got := view.CanRequestReturn(); got != tt.wantCanAsk {
				t.Errorf("CanRequestReturn() = %v, want %v", got, tt.wantCanAsk)
			}
			if got := view.ViewsReturn(); got != tt.wantViewLink {
				t.Errorf("ViewsReturn() = %v, want %v", got, tt.wantViewLink)
			}
		})
	}
}

// TestHandler_OrderPageOpenReturn: the order page of a shipped order with an open return request says
// it is awaiting review and links to the request instead of offering to file another.
func TestHandler_OrderPageOpenReturn(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	asStore := storeRolePool(t)
	h := buyer.NewHandler(buyer.NewStore(asStore), access.NewStore(asStore, false), slog.New(slog.DiscardHandler), testLimiter(), true)
	number, line := shippedOrder(t, 2, 2)
	if err := buyer.NewStore(pool).OpenReturn(ctx, number, uuid.NullUUID{}, &buyer.ReturnRequest{
		Reason: "不合用", Lines: map[string]int32{line.String(): 1},
	}); err != nil {
		t.Fatalf("open a return: %v", err)
	}

	r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+number, http.NoBody)
	r.SetPathValue("number", number)
	r.AddCookie(placedCookie(t, number))
	w := httptest.NewRecorder()
	h.OrderPage(w, r)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("order page status = %d, want %d", w.Code, http.StatusOK)
	}
	for _, want := range []string{i18n.T(ctx, i18n.KeyOrderReturnOpen), i18n.T(ctx, i18n.KeyOrderReturnView), `href="/orders/` + number + `/return"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the order page of an open return lacks %q", want)
		}
	}
	if strings.Contains(body, ">"+i18n.T(ctx, i18n.KeyOrderReturn)+"<") {
		t.Error("the order page of an open return still offers to request one")
	}
}
