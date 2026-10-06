//go:build integration

package orders_test

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
	"github.com/koopa0/goen/internal/admin/health"
	"github.com/koopa0/goen/internal/admin/orders"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages/admin"
)

// The dashboard lists work that waits for a person: a return approved and not
// yet inspected comes from the desk's own counts, a stranded invoice claim from
// the health desk, so the list and /admin/health name the same things.
func TestTheDashboardListsAnUninspectedReturnAndAStrandedClaim(t *testing.T) {
	isolated := admintest.Pool(t)
	s := admintest.OrderStore(isolated, admintest.Refunder{}, nil, nil)
	count := func(tasks []admin.Task, label i18n.Key) int64 {
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
	if got := count(before.Tasks, i18n.KeyAdminQueueTaskUninspected) + count(healthTasksBefore, i18n.KeyAdminQueueTaskClaims); got != 0 {
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
	if got := count(healthTasks, i18n.KeyAdminQueueTaskClaims); got != 1 {
		t.Errorf("stranded invoice claims = %d, want 1", got)
	}
}

// A question counts until the SHOP has answered it, and a customer's reply is
// not the shop's. It is the queue's own predicate: the tile and /admin/questions
// must agree.
func TestTheDashboardCountsQuestionsTheShopHasNotAnswered(t *testing.T) {
	isolated := admintest.Pool(t)
	s := admintest.OrderStore(isolated, admintest.Refunder{}, nil, nil)
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
		desk orders.Health
		want bool
	}{
		{"unreadable", failing, true},
		{"readable", healthDesk{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := admintest.OrderDesk(admintest.OrderStoreWithHealth(pool, admintest.Refunder{}, nil, nil, tc.desk))
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
	tasks []admin.Task
	err   error
}

func (d healthDesk) Tasks(context.Context) ([]admin.Task, error) { return d.tasks, d.err }

// The first row is the one customers wait on: what the order desk counts is
// listed before what the health desk reports.
func TestTheDashboardListsDeskTasksBeforeHealthTasks(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	if _, err := pool.Exec(ctx, `
		INSERT INTO product_questions (product_id, body)
		VALUES ((SELECT id FROM products ORDER BY id LIMIT 1), 'Does it fit?')`); err != nil {
		t.Fatalf("ask: %v", err)
	}
	desk := healthDesk{tasks: []admin.Task{{
		Label: i18n.KeyAdminQueueTaskPayments, Count: 1, Href: "/admin/health#events-heading", Alert: true,
	}}}
	h := admintest.OrderDesk(admintest.OrderStoreWithHealth(pool, admintest.Refunder{}, nil, nil, desk))
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
	s := admintest.OrderStore(isolated, admintest.Refunder{}, nil, nil)
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
	creditNumber, creditPlaced := placeOrderPaidByCredit(t, isolated, 2*24*time.Hour)
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
}

// The age of an approved return that nobody has opened starts at the decision,
// not the request.
func TestTheDashboardAgesAnUninspectedReturnFromItsDecision(t *testing.T) {
	isolated := admintest.Pool(t)
	s := admintest.OrderStore(isolated, admintest.Refunder{}, nil, nil)
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
	defer pgtx.Rollback(t.Context(), tx)
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

// placeOrderPaidByCredit places a pending order whose store credit covers all
// of it, placed this long ago.
func placeOrderPaidByCredit(t *testing.T, pool *pgxpool.Pool, ago time.Duration) (number string, placedAt time.Time) {
	t.Helper()
	ctx := t.Context()
	const cents = 5000
	userID := admintest.CreditedAccount(t, pool, cents)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer pgtx.Rollback(ctx, tx)
	var orderID uuid.UUID
	if err = tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, user_id, shipping_version_id,
		                    shipping_method_code, shipping_method_name, shipping_cents, placed_at)
		SELECT next_order_number(), $1, v.id, sm.code, v.name, 0, now() - make_interval(secs => $2)
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number, placed_at`, userID, ago.Seconds()).Scan(&orderID, &number, &placedAt); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'c@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity)
		VALUES ($1, 'AGE-SKU', '帳齡測試', $2, 1)`, orderID, cents); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err = tx.Exec(ctx, `SELECT post_store_credit($1, $2, '訂單折抵', $3, $4, NULL)`,
		userID, -cents, orderID, "spend:"+orderID.String()); err != nil {
		t.Fatalf("spend credit: %v", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return number, placedAt
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
// the latest; an order still awaiting payment is neither, though it is newer.
func TestTheDashboardWeekCountsPaidOrdersAndNamesTheLatest(t *testing.T) {
	isolated := admintest.Pool(t)
	s := admintest.OrderStore(isolated, admintest.Refunder{}, nil, nil)
	read := func() admin.DashboardView {
		t.Helper()
		var view admin.DashboardView
		if err := s.FillWeek(t.Context(), &view, time.Now()); err != nil {
			t.Fatalf("FillWeek: %v", err)
		}
		return view
	}

	empty := read()
	if empty.Latest != nil || empty.LatestUnavailable || empty.Week.Orders != 0 {
		t.Fatalf("a shop with no order: Latest = %v, orders = %d, want none", empty.Latest, empty.Week.Orders)
	}
	if got := len(empty.Week.OrderDays.Current.Buckets) + len(empty.Week.OrderDays.Previous.Buckets); got != 14 {
		t.Errorf("days drawn = %d, want 14, a day without orders included", got)
	}

	number, _ := admintest.PaidPickingOrderForUser(t, isolated, admintest.Customer(t, isolated), 100000)
	admintest.PlaceUnpaidOrder(t, isolated)

	view := read()
	if view.Week.Orders != 1 || view.Week.RevenueCents != 100000 {
		t.Errorf("week = %d orders, %d cents, want 1 and 100000: the unpaid order is not a sale", view.Week.Orders, view.Week.RevenueCents)
	}
	today := view.Week.RevenueDays.Current.Buckets
	if got := today[len(today)-1].Value; got != 100000 {
		t.Errorf("today's revenue = %d, want 100000", got)
	}
	if view.Latest == nil || view.Latest.Number != number || view.Latest.TotalCents != 100000 {
		t.Fatalf("Latest = %+v, want %s for 100000, not the newer unpaid order", view.Latest, number)
	}
	if view.Latest.Elapsed < 0 || view.Latest.Elapsed > time.Minute {
		t.Errorf("Elapsed = %v, want under a minute", view.Latest.Elapsed)
	}
}
