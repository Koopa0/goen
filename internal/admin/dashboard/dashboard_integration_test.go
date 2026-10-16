//go:build integration

package dashboard_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/dashboard"
	"github.com/koopa0/goen/internal/admin/health"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/sales"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/worklist"
)

// The dashboard lists work that waits for a person: a return approved and not
// yet inspected comes from the desk's own counts, a stranded invoice claim from
// the health desk, so the list and /admin/health name the same things.
func TestTheDashboardListsAnUninspectedReturnAndAStrandedClaim(t *testing.T) {
	isolated := admintest.Pool(t)
	s := storeOver(isolated, health.NewStore(isolated))
	count := func(tasks []admin.Task, label i18n.Key) int64 {
		for _, task := range tasks {
			if task.Label == label {
				return task.Count
			}
		}
		return 0
	}
	healthCount := func(tasks []worklist.Task, label i18n.Key) int64 {
		for _, task := range tasks {
			if task.Label == label {
				return task.Count
			}
		}
		return 0
	}

	before, err := s.Dashboard(t.Context())
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	healthTasksBefore, err := s.HealthTasks(t.Context())
	if err != nil {
		t.Fatalf("HealthTasks: %v", err)
	}
	if got := count(before.Tasks, i18n.KeyAdminQueueTaskUninspected) + healthCount(healthTasksBefore, i18n.KeyAdminQueueTaskClaims); got != 0 {
		t.Fatalf("a shop with no return or claim lists %d of them", got)
	}

	admintest.PreapprovedReturn(t, isolated)
	number, orderID := admintest.PaidPickingOrderForUser(t, isolated, admintest.Customer(t, isolated), 100000)
	if _, execErr := isolated.Exec(t.Context(), `
		INSERT INTO invoice_operations
		    (order_id, kind, provider_key, amount_cents, request_payload,
		     actor_kind, request_id, status, last_error, created_at)
		VALUES ($1, 'issue', replace($2, '-', ''), 100000, '{}', 'system',
		        'refused:' || $2, 'rejected', 'issue_provider_rejected_2000006',
		        now() - interval '1 minute')`, orderID, number); execErr != nil {
		t.Fatalf("record a refused automatic issue: %v", execErr)
	}

	view, err := s.Dashboard(t.Context())
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if got := count(view.Tasks, i18n.KeyAdminQueueTaskUninspected); got != 1 {
		t.Errorf("returns awaiting inspection = %d, want 1", got)
	}
	healthTasks, err := s.HealthTasks(t.Context())
	if err != nil {
		t.Fatalf("HealthTasks: %v", err)
	}
	if got := healthCount(healthTasks, i18n.KeyAdminQueueTaskClaims); got != 1 {
		t.Errorf("stranded invoice claims = %d, want 1", got)
	}
}

// A question counts until the SHOP has answered it, and a customer's reply is
// not the shop's. It is the queue's own predicate: the tile and /admin/questions
// must agree.
func TestTheDashboardCountsQuestionsTheShopHasNotAnswered(t *testing.T) {
	isolated := admintest.Pool(t)
	s := storeOver(isolated, health.NewStore(isolated))
	waiting := func() int64 {
		t.Helper()
		v, err := s.Dashboard(t.Context())
		if err != nil {
			t.Fatalf("Dashboard: %v", err)
		}
		return v.UnansweredQuestions
	}
	ask := func(hidden bool) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := isolated.QueryRow(t.Context(), `
			INSERT INTO product_questions (product_id, body, hidden_at)
			VALUES ((SELECT id FROM products ORDER BY id LIMIT 1), 'Does it fit?',
			        CASE WHEN $1 THEN now() END)
			RETURNING id`, hidden).Scan(&id); err != nil {
			t.Fatalf("ask: %v", err)
		}
		return id
	}
	answer := func(p *pgxpool.Pool, question uuid.UUID, staff bool) {
		t.Helper()
		if _, err := p.Exec(t.Context(),
			`INSERT INTO product_answers (question_id, body, is_staff) VALUES ($1, 'Yes.', $2)`,
			question, staff); err != nil {
			t.Fatalf("answer: %v", err)
		}
	}

	base := waiting()
	open := ask(false)
	ask(true)
	if got := waiting() - base; got != 1 {
		t.Fatalf("an open and a hidden question add %d, want 1: a hidden question is not waiting", got)
	}
	answer(isolated, open, false)
	if got := waiting() - base; got != 1 {
		t.Errorf("a customer's reply left %d waiting, want 1: only the shop's answer settles it", got)
	}
	answer(isolated, open, true)
	if got := waiting() - base; got != 0 {
		t.Errorf("a shop answer left %d waiting, want 0", got)
	}
}

// A health desk that cannot be read must not look like one with nothing to
// report: the dashboard says so instead of dropping the payment and invoice tasks.
func TestTheDashboardSaysWhenTheHealthDeskCannotBeRead(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	failing := healthDesk{err: errors.New("health desk down")}
	notice := i18n.T(ctx, i18n.KeyAdminQueueHealthUnavailable)
	for _, tc := range []struct {
		name string
		desk dashboard.Health
		want bool
	}{
		{"unreadable", failing, true},
		{"readable", healthDesk{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := handlerOver(storeOver(pool, tc.desk))
			req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin", nil)
			w := httptest.NewRecorder()
			admintest.BackOffice.RequireStaff(h.Dashboard)(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("Dashboard answered %d, want 200", w.Code)
			}
			if got := strings.Contains(w.Body.String(), notice); got != tc.want {
				t.Errorf("dashboard shows the health notice = %v, want %v", got, tc.want)
			}
		})
	}
}

type healthDesk struct {
	tasks []worklist.Task
	err   error
}

func (d healthDesk) Tasks(context.Context) ([]worklist.Task, error) { return d.tasks, d.err }

// The first row is the one customers wait on: what the order desk counts is
// listed before what the health desk reports.
func TestTheDashboardListsDeskTasksBeforeHealthTasks(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	if _, err := pool.Exec(ctx, `
		INSERT INTO product_questions (product_id, body)
		VALUES ((SELECT id FROM products ORDER BY id LIMIT 1), 'Does it fit?')`); err != nil {
		t.Fatalf("ask: %v", err)
	}
	desk := healthDesk{tasks: []worklist.Task{{
		Label: i18n.KeyAdminQueueTaskPayments, Count: 1, Href: "/admin/health#events-heading", Alert: true,
	}}}
	h := handlerOver(storeOver(pool, desk))
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin", nil)
	w := httptest.NewRecorder()
	admintest.BackOffice.RequireStaff(h.Dashboard)(w, req)
	body := w.Body.String()
	deskAt := strings.Index(body, i18n.T(ctx, i18n.KeyAdminQueueStatQuestions))
	healthAt := strings.Index(body, i18n.T(ctx, i18n.KeyAdminQueueTaskPayments))
	if deskAt < 0 || healthAt < 0 {
		t.Fatalf("dashboard lists the desk task at %d and the health task at %d, want both", deskAt, healthAt)
	}
	if deskAt > healthAt {
		t.Error("the health task is listed before the desk task, want the desk's work first")
	}
}

// The dashboard ages a ready order from the moment it was funded and the health
// desk lists the same moment for the same order. The two read it through
// separate queries, so each case asserts both against one known instant.
func TestTheDashboardAgesAReadyOrderFromWhenItWasFunded(t *testing.T) {
	isolated := admintest.Pool(t)
	s := storeOver(isolated, health.NewStore(isolated))
	desk := health.NewStore(isolated)

	readyAge := func() (admin.Task, bool) {
		t.Helper()
		v, err := s.Dashboard(t.Context())
		if err != nil {
			t.Fatalf("Dashboard: %v", err)
		}
		for _, task := range v.Tasks {
			if task.Label == i18n.KeyAdminStatusReadyToPick {
				return task, true
			}
		}
		return admin.Task{}, false
	}
	fundedAtOnHealthDesk := func(number string) string {
		t.Helper()
		rows, _, err := desk.UninvoicedOrders(t.Context(), 0)
		if err != nil {
			t.Fatalf("UninvoicedOrders: %v", err)
		}
		for _, r := range rows {
			if r.OrderNumber == number {
				return r.Since
			}
		}
		t.Fatalf("the health desk does not list paid order %s", number)
		return ""
	}
	assertAge := func(since time.Time, number string) {
		t.Helper()
		task, ok := readyAge()
		if !ok || !task.HasAge {
			t.Fatalf("the ready-orders task = %+v, want one carrying an age", task)
		}
		if want := int64(time.Since(since).Seconds()); task.AgeSeconds < want-60 || task.AgeSeconds > want+60 {
			t.Errorf("ready orders have waited %d s, want about %d s (funded %s)", task.AgeSeconds, want, since)
		}
		if got, want := fundedAtOnHealthDesk(number), shoptime.Minute(since); got != want {
			t.Errorf("health desk funded %s at %s, dashboard ages it from %s", number, got, want)
		}
	}

	// Store credit paid it in full: no payment and no paid event, so the age
	// starts at placed_at.
	creditNumber, creditPlaced := admintest.PlaceOrderPaidByCredit(t, isolated, 2*24*time.Hour)
	assertAge(creditPlaced, creditNumber)
	if task, _ := readyAge(); task.AgeSeconds/86400 != 2 {
		t.Errorf("an owes-nothing order placed two days ago is %d days old, want 2", task.AgeSeconds/86400)
	}

	// A card payment the provider confirmed three days ago, though recorded now.
	cardNumber, cardPaid := captureCardPaidAgo(t, isolated, 3*24*time.Hour)
	assertAge(cardPaid, cardNumber)
	if task, _ := readyAge(); task.AgeSeconds/86400 != 3 {
		t.Errorf("a card order paid three days ago is %d days old, want 3", task.AgeSeconds/86400)
	}

	// The latest paid order is funded when the ready-orders age says: it is the
	// only committed order, so the two read one funded_at through two queries.
	recent, err := s.RecentSales(t.Context(), admin.WeekDays, admintest.DatabaseNow(t, isolated))
	if err != nil {
		t.Fatalf("RecentSales: %v", err)
	}
	task, _ := readyAge()
	if recent.Latest == nil || recent.Latest.Number != cardNumber {
		t.Fatalf("Latest = %+v, want %s", recent.Latest, cardNumber)
	}
	if got, want := int64(recent.Latest.Elapsed.Seconds()), task.AgeSeconds; got < want-60 || got > want+60 {
		t.Errorf("the latest paid order was funded %d s ago, the ready-orders age says %d s", got, want)
	}
}

// The age of an approved return that nobody has opened starts at the decision,
// not the request.
func TestTheDashboardAgesAnUninspectedReturnFromItsDecision(t *testing.T) {
	isolated := admintest.Pool(t)
	s := storeOver(isolated, health.NewStore(isolated))
	requestID := admintest.PreapprovedReturn(t, isolated)
	var decided time.Time
	if err := isolated.QueryRow(t.Context(),
		`SELECT decided_at FROM return_requests WHERE id = $1`, requestID).Scan(&decided); err != nil {
		t.Fatalf("read decided_at: %v", err)
	}
	tx, err := isolated.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer db.Rollback(t.Context(), tx)
	// Triggers off only to write a request date older than its own decision.
	if _, err = tx.Exec(t.Context(), `SET LOCAL session_replication_role = replica`); err != nil {
		t.Fatalf("disable triggers: %v", err)
	}
	if _, err = tx.Exec(t.Context(),
		`UPDATE return_requests SET created_at = decided_at - interval '5 days' WHERE id = $1`, requestID); err != nil {
		t.Fatalf("backdate the request: %v", err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatalf("commit: %v", err)
	}
	v, err := s.Dashboard(t.Context())
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	for _, task := range v.Tasks {
		if task.Label != i18n.KeyAdminQueueTaskUninspected {
			continue
		}
		if want := int64(time.Since(decided).Seconds()); task.AgeSeconds < want-60 || task.AgeSeconds > want+60 {
			t.Errorf("uninspected return has waited %d s, want about %d s since the decision", task.AgeSeconds, want)
		}
		return
	}
	t.Fatal("the dashboard lists no uninspected return")
}

// captureCardPaidAgo captures a card payment whose confirming webhook event
// says Stripe took the money this long ago, which is where capture_payment
// takes paid_at from. The event is recorded after the payment opens: opening
// refuses a provider reference that already has webhook history.
func captureCardPaidAgo(t *testing.T, pool *pgxpool.Pool, ago time.Duration) (number string, paidAt time.Time) {
	t.Helper()
	ctx := t.Context()
	const cents = 8000
	orderID := admintest.OrderForCustomer(t, pool, admintest.Customer(t, pool), cents, false)
	paidAt = time.Now().Add(-ago).Truncate(time.Second)
	ref := "cs_age_" + orderID.String()
	if _, err := pool.Exec(ctx, `SELECT open_payment($1, $2, $3::bigint)`, orderID, ref, int64(cents)); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_webhook_events (provider, event_id, type, object_ref, payload)
		VALUES ('stripe', $1, 'checkout.session.completed', $2,
		        jsonb_build_object('created', $3::bigint,
		                           'data', jsonb_build_object('object',
		                               jsonb_build_object('payment_status', 'paid'))))`,
		"evt_age_"+orderID.String(), ref, paidAt.Unix()); err != nil {
		t.Fatalf("record the confirming event: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT capture_payment($1, $2::bigint, NULL, NULL)`, ref, int64(cents)); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT order_number FROM orders WHERE id = $1`, orderID).Scan(&number); err != nil {
		t.Fatalf("read order number: %v", err)
	}
	return number, paidAt
}

// The last week is the report's figures: a paid order counts on its day and is
// the latest, and not in the week before; an order still awaiting payment is
// neither, though it is newer.
func TestTheDashboardWeekCountsPaidOrdersAndNamesTheLatest(t *testing.T) {
	isolated := admintest.Pool(t)
	s := storeOver(isolated, health.NewStore(isolated))
	read := func() sales.Recent {
		t.Helper()
		recent, err := s.RecentSales(t.Context(), admin.WeekDays, admintest.DatabaseNow(t, isolated))
		if err != nil {
			t.Fatalf("RecentSales: %v", err)
		}
		return recent
	}

	empty := read()
	if empty.Latest != nil || empty.Current.Orders != 0 {
		t.Fatalf("a shop with no order: Latest = %v, orders = %d, want none", empty.Latest, empty.Current.Orders)
	}
	if got := len(empty.Current.Days) + len(empty.Previous.Days); got != 14 {
		t.Errorf("days drawn = %d, want 14, a day without orders included", got)
	}

	number, _ := admintest.PaidPickingOrderForUser(t, isolated, admintest.Customer(t, isolated), 100000)
	admintest.PlaceUnpaidOrder(t, isolated)

	recent := read()
	if recent.Current.Orders != 1 || recent.Current.RevenueCents != 100000 {
		t.Errorf("week = %d orders, %d cents, want 1 and 100000: the unpaid order is not a sale", recent.Current.Orders, recent.Current.RevenueCents)
	}
	if recent.Previous.Orders != 0 || recent.Previous.RevenueCents != 0 {
		t.Errorf("the week before = %d orders, %d cents, want none: today's sale is not in it", recent.Previous.Orders, recent.Previous.RevenueCents)
	}
	today := recent.Current.Days
	if got := today[len(today)-1].RevenueCents; got != 100000 {
		t.Errorf("today's revenue = %d, want 100000", got)
	}
	if recent.Latest == nil || recent.Latest.Number != number || recent.Latest.TotalCents != 100000 {
		t.Fatalf("Latest = %+v, want %s for 100000, not the newer unpaid order", recent.Latest, number)
	}
	if recent.Latest.Elapsed < 0 || recent.Latest.Elapsed > time.Minute {
		t.Errorf("Elapsed = %v, want under a minute", recent.Latest.Elapsed)
	}
}

// The latest paid order is the one whose money came in last, which is not the
// one placed last, and an order refunded before shipment is no sale.
func TestTheLatestPaidOrderFollowsWhenMoneyCameInAndSkipsBeforeShipmentRefunds(t *testing.T) {
	isolated := admintest.Pool(t)
	s := storeOver(isolated, health.NewStore(isolated))
	latest := func(store *dashboard.Store) *sales.PaidOrder {
		t.Helper()
		recent, err := store.RecentSales(t.Context(), admin.WeekDays, admintest.DatabaseNow(t, isolated))
		if err != nil {
			t.Fatalf("RecentSales: %v", err)
		}
		return recent.Latest
	}
	place := func(pool *pgxpool.Pool, number string, ago time.Duration) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), `UPDATE orders SET placed_at = now() - make_interval(secs => $2) WHERE order_number = $1`,
			number, ago.Seconds()); err != nil {
			t.Fatalf("place %s: %v", number, err)
		}
	}

	// Placed first and paid last, against one placed later and paid earlier.
	paidLast, _ := captureCardPaidAgo(t, isolated, time.Hour)
	place(isolated, paidLast, 5*time.Hour)
	paidFirst, _ := captureCardPaidAgo(t, isolated, 3*time.Hour)
	place(isolated, paidFirst, 2*time.Hour)
	if got := latest(s); got == nil || got.Number != paidLast {
		t.Errorf("Latest = %+v, want %s: paid last, though placed first", got, paidLast)
	}

	// A newer payment that was refunded before it shipped.
	refunded, _ := captureCardPaidAgo(t, isolated, 10*time.Minute)
	if _, err := isolated.Exec(t.Context(), `
		INSERT INTO return_requests (order_id, reason, before_shipment)
		SELECT id, '', true FROM orders WHERE order_number = $1`, refunded); err != nil {
		t.Fatalf("refund before shipment: %v", err)
	}
	if got := latest(s); got == nil || got.Number != paidLast {
		t.Errorf("Latest = %+v, want %s: %s was refunded before shipment", got, paidLast, refunded)
	}

	// Nothing paid within the two periods read: the whole history is searched.
	alone := admintest.Pool(t)
	old, _ := captureCardPaidAgo(t, alone, 40*24*time.Hour)
	place(alone, old, 40*24*time.Hour)
	got := latest(storeOver(alone, health.NewStore(alone)))
	if got == nil || got.Number != old || got.Elapsed < 39*24*time.Hour {
		t.Errorf("Latest = %+v, want %s paid 40 days ago", got, old)
	}
}

// A picking order is as old as the moment a person took it into picking.
func TestTheDashboardAgesAPickingOrderFromWhenItWasTaken(t *testing.T) {
	isolated := admintest.Pool(t)
	s := storeOver(isolated, health.NewStore(isolated))
	_, orderID := admintest.PaidPickingOrderForUser(t, isolated, admintest.Customer(t, isolated), 100000)
	if _, err := isolated.Exec(t.Context(), `
		INSERT INTO order_events (order_id, kind, occurred_at) VALUES ($1, 'picking', now() - interval '2 days')`,
		orderID); err != nil {
		t.Fatalf("record picking: %v", err)
	}
	view, err := s.Dashboard(t.Context())
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	for _, task := range view.Tasks {
		if task.Label == i18n.KeyAdminStatusPicking {
			if !task.HasAge || task.AgeSeconds/86400 != 2 {
				t.Errorf("picking task = %+v, want an age of 2 days", task)
			}
			return
		}
	}
	t.Error("the dashboard lists no picking task")
}
