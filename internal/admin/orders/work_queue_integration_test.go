//go:build integration

package orders_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/orders"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
)

// afterOf is the position token of the page that follows next.
func afterOf(t *testing.T, next string) string {
	t.Helper()
	u, err := url.Parse(next)
	if err != nil {
		t.Fatalf("parse %q: %v", next, err)
	}
	return u.Query().Get(keyset.Param)
}

// workTab reads every page of a work tab, in the order the desk shows it.
func workTab(t *testing.T, s *orders.Store, tab orders.QueueFilter) []orders.WorkRow {
	t.Helper()
	ctx, _ := admintest.StaffContext(t, pool)
	var all []orders.WorkRow
	after := ""
	for range 1000 {
		view, err := s.List(ctx, tab, "", after)
		if err != nil {
			t.Fatalf("List(%q): %v", tab, err)
		}
		all = append(all, view.Work...)
		if view.Next == "" {
			return all
		}
		after = afterOf(t, view.Next)
	}
	t.Fatalf("the %q tab has no last page", tab)
	return nil
}

func positionOf(rows []orders.WorkRow, number string) int {
	return slices.IndexFunc(rows, func(w orders.WorkRow) bool { return w.Number == number })
}

// TestStore_ListWorkTabsOldestFirst: the work tabs list the order that has waited longest first, and
// an order waits from its payment, not from when it was placed.
// Mutation left to CI: ORDER BY w.since DESC in AdminWorkQueue, o.placed_at in its place, or the @funding condition dropped.
func TestStore_ListWorkTabsOldestFirst(t *testing.T) {
	s := admintest.OrderStore(pool, admintest.HealthyGateway(t), nil, nil)
	credit, _ := admintest.PlaceOrderPaidByCredit(t, pool, 10*24*time.Hour)
	early, _, _ := admintest.PaidUnshippedOrder(t, pool, 20000, 0, false)
	late, lateID, _ := admintest.PaidUnshippedOrder(t, pool, 20000, 0, false)
	// Placed first, paid last: by placing it would lead the two.
	if _, err := pool.Exec(t.Context(), `UPDATE orders SET placed_at = now() - interval '2 days' WHERE id = $1`, lateID); err != nil {
		t.Fatalf("backdate placing: %v", err)
	}
	packing, _, _ := admintest.PaidUnshippedOrder(t, pool, 20000, 0, true)
	unpaid := admintest.PlaceUnpaidOrder(t, pool)

	ready := workTab(t, s, orders.QueueReady)
	at := [...]int{positionOf(ready, credit), positionOf(ready, early), positionOf(ready, late)}
	if slices.Contains(at[:], -1) || !slices.IsSorted(at[:]) {
		t.Errorf("positions of the credit-paid, the early-paid and the late-paid order = %v, want all present and in that order", at)
	}
	if got := positionOf(ready, unpaid); got != -1 {
		t.Errorf("the order that is still unpaid is listed as awaiting packing at %d", got)
	}
	if got := positionOf(ready, packing); got != -1 {
		t.Errorf("an order being packed is listed as awaiting packing at %d", got)
	}
	for i := 1; i < len(ready); i++ {
		if ready[i-1].WaitDays < ready[i].WaitDays {
			t.Fatalf("%s has waited %d days and is listed before %s, which has waited %d", ready[i-1].Number, ready[i-1].WaitDays, ready[i].Number, ready[i].WaitDays)
		}
	}
	if w := ready[at[0]]; w.WaitDays < 10 || !w.Overdue {
		t.Errorf("the order placed 10 days ago and paid by credit waited %d days, overdue %t; want 10 or more, overdue", w.WaitDays, w.Overdue)
	}
	if w := ready[at[1]]; w.WaitDays != 0 || w.Overdue {
		t.Errorf("the order paid just now waited %d days, overdue %t; want 0, not overdue", w.WaitDays, w.Overdue)
	}

	if got := positionOf(workTab(t, s, orders.QueuePicking), packing); got == -1 {
		t.Error("the order being packed is missing from the packing tab")
	}
}

// TestStore_AdminWorkQueueKeysetWalk: reading the work queue two rows at a time from each page's last
// row gives the whole queue once, in order, and the cursor it hands out is the position List reads back.
// Two orders waiting since the same instant sit side by side, so a one-row walk puts its page boundary between them.
// Mutation left to CI: compare (w.since, o.id) with < instead of > (red), or drop o.id from the ORDER BY (likely red:
// the tied pair is then in no fixed order).
func TestStore_AdminWorkQueueKeysetWalk(t *testing.T) {
	s := admintest.OrderStore(pool, admintest.HealthyGateway(t), nil, nil)
	for range 3 {
		admintest.PaidUnshippedOrder(t, pool, 20000, 0, false)
	}
	// Paid by credit alone, an order waits from its placing, so one UPDATE ties the two.
	tiedA, _ := admintest.PlaceOrderPaidByCredit(t, pool, 0)
	tiedB, _ := admintest.PlaceOrderPaidByCredit(t, pool, 0)
	if _, err := pool.Exec(t.Context(), `UPDATE orders SET placed_at = now() - interval '40 days' WHERE order_number = ANY($1)`, []string{tiedA, tiedB}); err != nil {
		t.Fatalf("tie the two orders' wait: %v", err)
	}
	q := db.New(pool)
	read := func(limit int32, after *db.AdminWorkQueueRow) []db.AdminWorkQueueRow {
		p := db.AdminWorkQueueParams{Status: "pending", Funding: "funded", RowLimit: limit}
		if after != nil {
			p.HasCursor, p.AfterSince, p.AfterID = true, after.WaitingSince, after.ID
		}
		rows, err := q.AdminWorkQueue(t.Context(), p)
		if err != nil {
			t.Fatalf("AdminWorkQueue: %v", err)
		}
		return rows
	}
	ids := func(rows []db.AdminWorkQueueRow) []uuid.UUID {
		out := make([]uuid.UUID, len(rows))
		for i := range rows {
			out[i] = rows[i].ID
		}
		return out
	}
	whole := read(10000, nil)
	if len(whole) < 5 {
		t.Fatalf("the queue holds %d orders, want the 5 just paid at least", len(whole))
	}
	inOrder := func(a, b db.AdminWorkQueueRow) int {
		if c := a.WaitingSince.Compare(b.WaitingSince); c != 0 {
			return c
		}
		return slices.Compare(a.ID[:], b.ID[:])
	}
	if !slices.IsSortedFunc(whole, inOrder) {
		t.Error("the queue is not in (waiting since, id) order")
	}
	indexOf := func(number string) int {
		return slices.IndexFunc(whole, func(r db.AdminWorkQueueRow) bool { return r.OrderNumber == number })
	}
	if a, b := indexOf(tiedA), indexOf(tiedB); a < 0 || b < 0 || a-b != 1 && b-a != 1 || !whole[a].WaitingSince.Equal(whole[b].WaitingSince) {
		t.Fatalf("the two orders paid at the same instant are at %d and %d, want next to each other with one wait", a, b)
	}
	for _, size := range []int32{1, 2} {
		var walked []db.AdminWorkQueueRow
		var last *db.AdminWorkQueueRow
		for range len(whole) + 1 {
			page := read(size, last)
			if len(page) == 0 {
				break
			}
			walked = append(walked, page...)
			last = &page[len(page)-1]
		}
		if !slices.Equal(ids(walked), ids(whole)) {
			t.Fatalf("a %d-row walk lists %d orders in another order than the whole read's %d", size, len(walked), len(whole))
		}
	}

	ctx, _ := admintest.StaffContext(t, pool)
	next, ok := keyset.NextURL("/admin/orders?status=ready", whole[0].PageCursor)
	if !ok {
		t.Fatal("no token for the first row's cursor")
	}
	view, err := s.List(ctx, orders.QueueReady, "", afterOf(t, next))
	if err != nil {
		t.Fatalf("List after the first row: %v", err)
	}
	if !view.Resumed() || len(view.Work) == 0 || view.Work[0].Number != whole[1].OrderNumber {
		t.Errorf("List after the first row's cursor resumed = %t and starts at %v, want the second order %s", view.Resumed(), view.Work, whole[1].OrderNumber)
	}
}

// TestHandler_ListSearchAndStalePage: a complete order number goes to its order, a search says how many it found, and a
// position that is no position says the page is gone.
// Mutation left to CI: drop the after != "" && !view.Resumed() check, or the single-hit redirect.
func TestHandler_ListSearchAndStalePage(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	s := admintest.OrderStore(pool, admintest.HealthyGateway(t), nil, nil)
	number := admintest.PlaceUnpaidOrder(t, pool)
	get := func(target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		admintest.OrderDesk(s).List(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody))
		return rec
	}

	for _, term := range []string{number, strings.ToLower(number)} {
		if rec := get("/admin/orders?q=" + term); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/orders/"+number {
			t.Errorf("searching %q = %d to %q, want 303 to the order", term, rec.Code, rec.Header().Get("Location"))
		}
	}
	if rec := get("/admin/orders?q=" + number[:10]); rec.Code != http.StatusOK {
		t.Errorf("searching for the start of a number = %d, want the list", rec.Code)
	}
	view, err := s.List(ctx, orders.QueueAll, number, "")
	if err != nil || view.SearchCount != 1 {
		t.Errorf("searching a number found %d (%v), want 1", view.SearchCount, err)
	}

	gone := i18n.T(ctx, i18n.KeyAdminQueuePageGone)
	// A position from when the work tabs ran newest first.
	old, _ := keyset.NextURL("/admin/orders?status=ready", `{"At":"2026-10-01T04:00:00Z","ID":"`+uuid.NewString()+`"}`)
	for _, target := range []string{
		"/admin/orders?status=ready&after=" + afterOf(t, old),
		"/admin/orders?after=not-a-token",
	} {
		rec := get(target)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), gone) {
			t.Errorf("GET %s = %d, want 200 saying the page is gone", target, rec.Code)
		}
	}
	if rec := get("/admin/orders?status=ready"); strings.Contains(rec.Body.String(), gone) {
		t.Error("the first page of a tab says its page is gone")
	}
}
