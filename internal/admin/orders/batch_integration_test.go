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

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/orders"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/order"
)

func postPicking(t *testing.T, s *orders.Store, locale i18n.Locale, numbers ...string) *httptest.ResponseRecorder {
	t.Helper()
	ctx, _ := admintest.StaffContext(t, pool)
	form := url.Values{"order": numbers}
	r := httptest.NewRequestWithContext(i18n.WithLocale(ctx, locale), http.MethodPost, "/admin/orders/picking", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	mux := http.NewServeMux()
	admintest.OrderDesk(s).Routes(mux, admintest.BackOffice)
	mux.ServeHTTP(w, r)
	return w
}

func readyOrder(t *testing.T) (number string, id uuid.UUID) {
	t.Helper()
	number, id, _ = admintest.PaidUnshippedOrder(t, pool, 20000, 0, false)
	return number, id
}

func eventsOf(t *testing.T, id uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM order_events WHERE order_id = $1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestHandler_StartPickingBatch: the ticked orders all start packing, each
// through Advance, and the answer is a redirect to the packing tab.
// Mutation left to CI: skip the Advance call, or answer the tab it came from.
func TestHandler_StartPickingBatch(t *testing.T) {
	s := admintest.OrderStore(pool, admintest.HealthyGateway(t), nil, nil)
	a, aID := readyOrder(t)
	b, bID := readyOrder(t)
	c, cID := readyOrder(t)

	before := eventsOf(t, aID)
	w := postPicking(t, s, i18n.En, c, a, b, a)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/orders?status=picking&started=1" {
		t.Fatalf("batch = %d %q, want 303 to the packing tab", w.Code, w.Header().Get("Location"))
	}
	for _, id := range []uuid.UUID{aID, bID, cID} {
		if got := admintest.FulfillmentOf(t, pool, id); got != string(order.FulfillmentPicking) {
			t.Errorf("order %s = %q, want picking", id, got)
		}
	}
	if got := eventsOf(t, aID) - before; got != 1 {
		t.Errorf("an order listed twice gained %d events, want 1", got)
	}
}

// TestHandler_StartPickingRefusals: an order the database refuses is named with
// its reason on a 422 of the ready tab, and the orders around it still start.
// Mutation left to CI: let an order in any other status through, or answer 303
// whatever was refused.
func TestHandler_StartPickingRefusals(t *testing.T) {
	s := admintest.OrderStore(pool, admintest.HealthyGateway(t), nil, nil)
	ctx, _ := admintest.StaffContext(t, pool)
	for _, tt := range []struct {
		locale                           i18n.Locale
		cancelled, unpaid, gone, started string
	}{
		{i18n.ZhHant, "已取消", "還沒付清", "狀態已經變了", "其他 2 筆已開始備貨。"},
		{i18n.En, "Cancelled", "Not paid yet", "Its status has changed", "The other 2 orders have started packing."},
	} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			a, aID := readyOrder(t)
			b, bID := readyOrder(t)
			cancelled := admintest.PlaceUnpaidOrder(t, pool)
			if _, err := s.Advance(ctx, cancelled, order.FulfillmentCancelled, uuid.NullUUID{}); err != nil {
				t.Fatalf("cancel: %v", err)
			}
			unpaid := admintest.PlaceUnpaidOrder(t, pool)
			const missing = "GO-200101-AAAAAA"

			w := postPicking(t, s, tt.locale, a, cancelled, unpaid, b, missing)
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("batch with refusals = %d, want 422", w.Code)
			}
			body := w.Body.String()
			for _, want := range []string{
				cancelled + sep(tt.locale) + tt.cancelled,
				unpaid + sep(tt.locale) + tt.unpaid,
				missing + sep(tt.locale) + tt.gone,
				tt.started,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("the page lacks %q", want)
				}
			}
			for _, id := range []uuid.UUID{aID, bID} {
				if got := admintest.FulfillmentOf(t, pool, id); got != string(order.FulfillmentPicking) {
					t.Errorf("the order beside the refused ones = %q, want picking", got)
				}
			}
		})
	}
}

// TestHandler_StartPickingLockTimeout: an order whose row another desk holds
// stops the batch with a 503 of the ready tab, not a bare 500. The order before
// it has started, the held one is named with the try-again reason, and the one
// after it is named as not tried and is still waiting.
// Mutation left to CI: answer admin.ServerError when Advance fails.
func TestHandler_StartPickingLockTimeout(t *testing.T) {
	s := admintest.OrderStore(admintest.LockTimeoutPool(t, pool), admintest.HealthyGateway(t), nil, nil)
	a, aID := readyOrder(t)
	held, heldID := readyOrder(t)
	c, cID := readyOrder(t)
	admintest.HoldRow(t, pool, `SELECT 1 FROM orders WHERE order_number = $1 FOR UPDATE`, held)

	w := postPicking(t, s, i18n.En, a, held, c)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("batch behind a held row = %d, want 503", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		held + ": No answer; press Start packing again.",
		c + ": Not tried yet",
		"The other 1 order has started packing.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	if got := admintest.FulfillmentOf(t, pool, aID); got != string(order.FulfillmentPicking) {
		t.Errorf("the order before the held one = %q, want picking", got)
	}
	if got := admintest.FulfillmentOf(t, pool, cID); got == string(order.FulfillmentPicking) {
		t.Errorf("the order after the held one = %q, want it untried", got)
	}
	if got := admintest.FulfillmentOf(t, pool, heldID); got == string(order.FulfillmentPicking) {
		t.Errorf("the held order = %q, want it unchanged", got)
	}
}

func sep(l i18n.Locale) string {
	if l == i18n.ZhHant {
		return "："
	}
	return ": "
}

// TestHandler_StartPickingTwice: pressing the button again for the same orders
// is not a failure, and writes nothing more. The decision is made under the
// order's lock, in Advance.
// Mutation left to CI: map ErrAlreadyThere to a refusal.
func TestHandler_StartPickingTwice(t *testing.T) {
	s := admintest.OrderStore(pool, admintest.HealthyGateway(t), nil, nil)
	a, aID := readyOrder(t)
	b, bID := readyOrder(t)

	if w := postPicking(t, s, i18n.En, a, b); w.Code != http.StatusSeeOther {
		t.Fatalf("first press = %d", w.Code)
	}
	events := eventsOf(t, aID) + eventsOf(t, bID)
	w := postPicking(t, s, i18n.En, a, b)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/orders?status=picking&started=1" {
		t.Fatalf("second press = %d %q, want the same 303", w.Code, w.Header().Get("Location"))
	}
	if got := eventsOf(t, aID) + eventsOf(t, bID); got != events {
		t.Errorf("second press wrote %d events", got-events)
	}
}

// TestStore_AdvanceAlreadyThere: a move to the status the order holds says so
// under the lock, and still is a refusal for the order's own form.
func TestStore_AdvanceAlreadyThere(t *testing.T) {
	s := admintest.OrderStore(pool, admintest.HealthyGateway(t), nil, nil)
	ctx, _ := admintest.StaffContext(t, pool)
	number, _ := readyOrder(t)
	if _, err := s.Advance(ctx, number, order.FulfillmentPicking, uuid.NullUUID{}); err != nil {
		t.Fatal(err)
	}
	_, err := s.Advance(ctx, number, order.FulfillmentPicking, uuid.NullUUID{})
	if !errors.Is(err, orders.ErrAlreadyThere) || !errors.Is(err, orders.ErrRefused) {
		t.Errorf("second move = %v, want ErrAlreadyThere and ErrRefused", err)
	}
	_, err = s.Advance(ctx, admintest.PlaceUnpaidOrder(t, pool), order.FulfillmentDelivered, uuid.NullUUID{})
	if !errors.Is(err, orders.ErrRefused) || !db.IsConstraint(err, "orders_legal_transition") ||
		errors.Is(err, orders.ErrAlreadyThere) || errors.Is(err, orders.ErrCancelled) {
		t.Errorf("an illegal move out of a live order = %v, want a refusal by orders_legal_transition, not already there or cancelled", err)
	}
	cancelled := admintest.PlaceUnpaidOrder(t, pool)
	if _, cancelErr := s.Advance(ctx, cancelled, order.FulfillmentCancelled, uuid.NullUUID{}); cancelErr != nil {
		t.Fatal(cancelErr)
	}
	_, err = s.Advance(ctx, cancelled, order.FulfillmentPicking, uuid.NullUUID{})
	if !errors.Is(err, orders.ErrCancelled) || !errors.Is(err, orders.ErrRefused) {
		t.Errorf("a move out of a cancelled order = %v, want ErrCancelled and ErrRefused", err)
	}
}

// TestStore_PickingOf: the slips and totals are of the named
// orders only; a cursor in the request does nothing, and there is no next page.
// Mutation left to CI: drop the numbers condition from PickingTotals.
func TestStore_PickingOf(t *testing.T) {
	s := admintest.OrderStore(pool, admintest.HealthyGateway(t), nil, nil)
	ctx, _ := admintest.StaffContext(t, pool)
	var picking [3]string
	for i := range picking {
		picking[i], _, _ = admintest.PaidUnshippedOrder(t, pool, 20000, 0, true)
	}
	const missing = "GO-200101-AAAAAA"

	view, err := s.PickingOf(ctx, []string{picking[0], missing, picking[2]})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Slips) != 2 || view.Slips[0].Number != picking[0] || view.Slips[1].Number != picking[2] {
		t.Fatalf("slips = %d, want the first and third picking order", len(view.Slips))
	}
	if sel := view.Selection; sel == nil || sel.Asked != 3 || sel.Listed != 2 {
		t.Errorf("selection = %+v, want 3 asked, 2 listed", sel)
	}
	var units int64
	for _, l := range view.Totals {
		units += l.Remaining
	}
	if units != 2 {
		t.Errorf("totals add up to %d units, want the 2 of the two orders", units)
	}
	if view.Next != "" || view.First != "" || view.PastEnd {
		t.Errorf("a selection has paging: next %q, first %q", view.Next, view.First)
	}

	mux := http.NewServeMux()
	admintest.OrderDesk(s).Routes(mux, admintest.BackOffice)
	q := url.Values{"order": {picking[1]}, "picked": {"1"}, keyset.Param: {"not-a-position"}}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequestWithContext(i18n.WithLocale(ctx, i18n.En), http.MethodGet, "/admin/orders/picking/slips?"+q.Encode(), nil))
	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(body, picking[1]) || strings.Contains(body, picking[0]) || strings.Contains(body, picking[2]) {
		t.Fatalf("GET = %d, want only %s", w.Code, picking[1])
	}
}
