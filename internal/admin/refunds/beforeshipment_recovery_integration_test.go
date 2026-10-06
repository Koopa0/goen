//go:build integration

package refunds_test

import (
	"context"
	"errors"
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/refunds"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/pgerr"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/refundstate"
)

func TestRefundCompletionLockFailureOffersResumeAfterSettlement(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	number, orderID, variantID := admintest.PaidUnshippedOrder(t, pool, 900000, 300000, true)
	var stockHeld int32
	if err := pool.QueryRow(ctx, `SELECT stock_quantity FROM product_variants WHERE id = $1`, variantID).
		Scan(&stockHeld); err != nil {
		t.Fatalf("read held stock: %v", err)
	}
	var awarded int64
	if err := pool.QueryRow(ctx, `SELECT points FROM loyalty_entries WHERE order_id = $1 AND kind = 'award'`, orderID).
		Scan(&awarded); err != nil || awarded <= 0 {
		t.Fatalf("read award = %d, %v, want a positive lot to claw back", awarded, err)
	}

	// An earlier row lock would stop opening the refund, before any payout.
	// This transaction acquires its row only at the finisher's real query.
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin finishing barrier: %v", err)
	}
	defer pgtx.Rollback(ctx, blocker)
	var sent atomic.Int64
	wantSettled := refundCompletionFacts{
		OrderStatus: "picking", ReturnStatus: "approved", CardCents: 900000, CardRows: 1,
		CreditCents: 300000, CreditRows: 1, ClawedPoints: -awarded, Clawbacks: 1,
		FrozenCardCents: 900000, FrozenCreditCents: 300000,
		RefundedEvents: 1, Held: 1, Stock: stockHeld,
	}
	barrier := &refundFinisherBarrier{number: number, before: func() {
		got := readRefundCompletionFacts(t, orderID, variantID)
		if diff := cmp.Diff(wantSettled, got); diff != "" {
			t.Fatalf("finisher entered before settlement (-want +got):\n%s", diff)
		}
		if got := sent.Load(); got != 1 {
			t.Fatalf("provider calls before finishing = %d, want 1", got)
		}
		if _, lockErr := blocker.Exec(ctx, `SELECT id FROM orders WHERE id = $1 FOR UPDATE`, orderID); lockErr != nil {
			t.Fatalf("hold the settled order: %v", lockErr)
		}
	}}
	adminPool := refundCompletionPool(t, barrier)
	store := refunds.NewStore(adminPool, admintest.Refunder{Sent: &sent}, nil)
	diagnostic := &refundCompletionLog{}
	handler := refunds.NewHandler(store, nil, slog.New(diagnostic))

	first := postRefundCompletion(ctx, handler, number, true)
	if requestErr := ctx.Err(); requestErr != nil {
		t.Fatalf("caller context after finishing = %v, want live after the database lock timeout", requestErr)
	}
	if !barrier.entered.Load() || !admintest.LockTimedOut(barrier.queryErr) {
		t.Fatalf("finishing query = entered %v, error %v, want the post-settlement lock timeout",
			barrier.entered.Load(), barrier.queryErr)
	}
	if first.Code != http.StatusSeeOther || first.Header().Get("Location") != "/admin/orders/"+number+"?cancelretry=1" {
		t.Errorf("settled finishing failure = %d %q, want 303 with cancelretry=1",
			first.Code, first.Header().Get("Location"))
	}
	if !errors.Is(diagnostic.err, refundstate.ErrIncomplete) || !errors.Is(diagnostic.err, refunds.ErrCancellationIncomplete) || !admintest.LockTimedOut(diagnostic.err) {
		t.Errorf("finishing diagnostic = %v, want cancellation incomplete with the original PostgreSQL lock failure", diagnostic.err)
	}
	if diff := cmp.Diff(wantSettled, readRefundCompletionFacts(t, orderID, variantID)); diff != "" {
		t.Fatalf("failed finishing changed settled facts (-want +got):\n%s", diff)
	}
	assertRefundRecoveryPage(t, ctx, adminPool, admintest.Refunder{}, number, first.Header().Get("Location"), true)
	if releaseErr := blocker.Commit(ctx); releaseErr != nil {
		t.Fatalf("release finishing barrier: %v", releaseErr)
	}

	preview, err := store.RefundPreview(ctx, number)
	if err != nil || !preview.Resume || preview.TotalCents != 1200000 || preview.CardCents != 900000 || preview.CreditCents != 300000 {
		t.Fatalf("refund preview = %+v, %v, want Resume for the settled 900000/300000 split", preview, err)
	}
	confirmation := postRefundCompletion(ctx, handler, number, false)
	if confirmation.Code != http.StatusOK || !strings.Contains(confirmation.Body.String(), `name="confirm"`) || !strings.Contains(confirmation.Body.String(), `value="refund"`) {
		t.Fatalf("Resume confirmation = %d, want the real refund form: %s", confirmation.Code, confirmation.Body.String())
	}
	resumed := postRefundCompletion(ctx, handler, number, true)
	if resumed.Code != http.StatusSeeOther || resumed.Header().Get("Location") != "/admin/orders/"+number+"?refunded=1" {
		t.Fatalf("Resume = %d %q, want 303 with refunded=1", resumed.Code, resumed.Header().Get("Location"))
	}
	wantFinished := wantSettled
	wantFinished.OrderStatus, wantFinished.ReturnStatus = "cancelled", "completed"
	wantFinished.CancelledEvents, wantFinished.CancelAudits, wantFinished.TerminalNotices = 1, 1, 1
	wantFinished.Held, wantFinished.Released, wantFinished.ReleaseMoves = 0, 1, 1
	wantFinished.Stock++
	if diff := cmp.Diff(wantFinished, readRefundCompletionFacts(t, orderID, variantID)); diff != "" {
		t.Fatalf("Resume result (-want +got):\n%s", diff)
	}
	admintest.AssertTerminalNotice(t, pool, orderID, email.TerminalCancelledByStaff, true)

	terminal := postRefundCompletion(ctx, handler, number, true)
	if terminal.Code != http.StatusSeeOther || terminal.Header().Get("Location") != "/admin/orders/"+number+"?refused=1" {
		t.Errorf("terminal press = %d %q, want the existing unavailable-refund refusal",
			terminal.Code, terminal.Header().Get("Location"))
	}
	if diff := cmp.Diff(wantFinished, readRefundCompletionFacts(t, orderID, variantID)); diff != "" {
		t.Errorf("terminal press repeated effects (-want +got):\n%s", diff)
	}
	if got := sent.Load(); got != 1 {
		t.Errorf("provider calls after Resume and terminal press = %d, want 1", got)
	}
}

func TestUnpaidRefundBeforeShipmentKeepsThePayoutRecoveryNotice(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	number, orderID, variantID := admintest.PaidUnshippedOrder(t, pool, 900000, 300000, true)
	var stockHeld int32
	if err := pool.QueryRow(ctx, `SELECT stock_quantity FROM product_variants WHERE id = $1`, variantID).
		Scan(&stockHeld); err != nil {
		t.Fatalf("read held stock: %v", err)
	}
	var sent atomic.Int64
	refunder := admintest.Refunder{State: refundstate.Failed, Sent: &sent}
	adminPool := admintest.AdminRolePool(t, pool)
	store := refunds.NewStore(adminPool, refunder, nil)
	diagnostic := &refundCompletionLog{}
	handler := refunds.NewHandler(store, nil, slog.New(diagnostic))
	response := postRefundCompletion(ctx, handler, number, true)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin/orders/"+number+"?refundretry=1" {
		t.Errorf("unpaid refund = %d %q, want 303 with refundretry=1", response.Code, response.Header().Get("Location"))
	}
	if !errors.Is(diagnostic.err, refundstate.ErrIncomplete) || errors.Is(diagnostic.err, refunds.ErrCancellationIncomplete) {
		t.Errorf("unpaid diagnostic = %v, want payout incomplete without cancellation incomplete", diagnostic.err)
	}
	want := refundCompletionFacts{
		OrderStatus: "picking", ReturnStatus: "approved", CardRows: 1,
		CreditCents: 300000, CreditRows: 1,
		FrozenCardCents: 900000, FrozenCreditCents: 300000,
		Held: 1, Stock: stockHeld,
	}
	if diff := cmp.Diff(want, readRefundCompletionFacts(t, orderID, variantID)); diff != "" {
		t.Fatalf("unpaid refund changed cancellation or settled facts (-want +got):\n%s", diff)
	}
	if got := sent.Load(); got != 1 {
		t.Errorf("provider calls = %d, want 1", got)
	}
	assertRefundRecoveryPage(t, ctx, adminPool, refunder, number, response.Header().Get("Location"), false)
}

func assertRefundRecoveryPage(t *testing.T, ctx context.Context, adminPool *pgxpool.Pool, refunder admintest.Refunder, number, location string, settled bool) {
	t.Helper()
	handler := admintest.OrderDesk(admintest.OrderStore(adminPool, refunder, nil, nil))
	mux := http.NewServeMux()
	handler.Routes(mux, admintest.BackOffice)
	want, wrong := i18n.KeyAdminNoticeRefundRetry, i18n.KeyAdminNoticeCancelRetry
	if settled {
		want, wrong = wrong, want
	}
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		local := i18n.WithLocale(ctx, locale)
		request := httptest.NewRequestWithContext(local, http.MethodGet, location, nil)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		body := html.UnescapeString(response.Body.String())
		if response.Code != http.StatusOK || !strings.Contains(body, i18n.T(local, want)) ||
			strings.Contains(body, i18n.T(local, wrong)) || !strings.Contains(body, i18n.T(local, i18n.KeyAdminRefundResume)) {
			t.Errorf("%s recovery page of %s = %d, want the matching notice and Resume without the other notice", locale, number, response.Code)
		}
	}
}

func TestRefundCompletionPreservesTheInvoiceRecoveryCause(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	number, orderID, _ := admintest.PaidUnshippedOrder(t, pool, 900000, 300000, true)
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoice_documents (order_id, kind, number, amount_cents)
		VALUES ($1, 'invoice', $2, 1200000)`, orderID, uuid.NewString()[:32]); err != nil {
		t.Fatalf("issue invoice: %v", err)
	}
	diagnostic := &refundCompletionLog{}
	store := refunds.NewStore(admintest.AdminRolePool(t, pool), admintest.Refunder{}, nil)
	handler := refunds.NewHandler(store, nil, slog.New(diagnostic))
	response := postRefundCompletion(ctx, handler, number, true)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin/orders/"+number+"?cancelinvoice=1" {
		t.Errorf("live invoice after settlement = %d %q, want 303 with cancelinvoice=1",
			response.Code, response.Header().Get("Location"))
	}
	if !errors.Is(diagnostic.err, refundstate.ErrIncomplete) || !errors.Is(diagnostic.err, refunds.ErrCancellationIncomplete) || !pgerr.IsConstraint(diagnostic.err, "orders_cancel_invoice_resolved") {
		t.Errorf("invoice diagnostic = %v, want incomplete retaining orders_cancel_invoice_resolved", diagnostic.err)
	}
	if got := admintest.FulfillmentOf(t, pool, orderID); got != "picking" {
		t.Errorf("live-invoice order = %q, want picking", got)
	}
}

func postRefundCompletion(ctx context.Context, handler *refunds.Handler, number string, confirmed bool) *httptest.ResponseRecorder {
	form := url.Values{"reason": {"Customer requested cancellation"}}
	if confirmed {
		form.Set("confirm", "refund")
		form.Set("total", "1200000")
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/orders/"+number+"/refund", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	mux := http.NewServeMux()
	handler.Routes(mux, admintest.BackOffice)
	mux.ServeHTTP(response, req)
	return response
}

type refundFinisherBarrier struct {
	number   string
	before   func()
	entered  atomic.Bool
	queryErr error
}

type refundFinisherQuery struct{}

func (b *refundFinisherBarrier) TraceQueryStart(ctx context.Context, _ *pgx.Conn, query pgx.TraceQueryStartData) context.Context {
	if !strings.HasPrefix(query.SQL, "-- name: LockOrderForAdvance :one\n") || len(query.Args) != 1 || query.Args[0] != b.number || !b.entered.CompareAndSwap(false, true) {
		return ctx
	}
	b.before()
	return context.WithValue(ctx, refundFinisherQuery{}, true)
}

func (b *refundFinisherBarrier) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, query pgx.TraceQueryEndData) {
	if ctx.Value(refundFinisherQuery{}) == true {
		b.queryErr = query.Err
	}
}

func refundCompletionPool(t *testing.T, barrier *refundFinisherBarrier) *pgxpool.Pool {
	t.Helper()
	cfg := pool.Config().Copy()
	cfg.MaxConns = 2
	cfg.ConnConfig.Tracer = barrier
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "200ms"
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SET ROLE admin`)
		return err
	}
	p, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open admin finishing pool: %v", err)
	}
	t.Cleanup(p.Close)
	var role string
	if err := p.QueryRow(t.Context(), `SELECT current_user`).Scan(&role); err != nil || role != "admin" {
		t.Fatalf("current_user = %q, %v, want admin", role, err)
	}
	return p
}

type refundCompletionLog struct{ err error }

func (*refundCompletionLog) Enabled(context.Context, slog.Level) bool { return true }

func (l *refundCompletionLog) Handle(_ context.Context, record slog.Record) error { //nolint:gocritic // slog.Handler requires a record value.
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == "error" {
			l.err, _ = attr.Value.Any().(error)
		}
		return true
	})
	return nil
}

func (l *refundCompletionLog) WithAttrs([]slog.Attr) slog.Handler { return l }
func (l *refundCompletionLog) WithGroup(string) slog.Handler      { return l }

type refundCompletionFacts struct {
	FrozenCardCents, FrozenCreditCents                             int64
	OrderStatus, ReturnStatus                                      string
	CardCents, CardRows, CreditCents, CreditRows                   int64
	ClawedPoints, Clawbacks, CancelClawbacks, CreditReversals      int64
	RefundedEvents, CancelledEvents, CancelAudits, TerminalNotices int64
	Held, Released, ReleaseMoves, Restocks                         int64
	Stock                                                          int32
}

func readRefundCompletionFacts(t *testing.T, orderID, variantID uuid.UUID) refundCompletionFacts {
	t.Helper()
	var facts refundCompletionFacts
	if err := pool.QueryRow(t.Context(), `
		SELECT o.fulfillment_status, r.status, r.card_refund_cents, r.credit_refund_cents,
		  (SELECT coalesce(sum(amount_cents), 0) FROM refunds WHERE return_request_id = r.id AND status = 'succeeded'),
		  (SELECT count(*) FROM refunds WHERE return_request_id = r.id),
		  (SELECT coalesce(sum(amount_cents), 0) FROM store_credit_entries WHERE idempotency_key = 'return-credit:' || r.id::text),
		  (SELECT count(*) FROM store_credit_entries WHERE idempotency_key = 'return-credit:' || r.id::text),
		  (SELECT coalesce(sum(points), 0) FROM loyalty_entries WHERE return_request_id = r.id AND kind = 'clawback'),
		  (SELECT count(*) FROM loyalty_entries WHERE order_id = o.id AND kind = 'clawback'),
		  (SELECT count(*) FROM loyalty_entries WHERE idempotency_key = 'cancel:' || o.id::text),
		  (SELECT count(*) FROM store_credit_entries reversed JOIN store_credit_entries spent ON spent.id = reversed.reverses_id WHERE spent.order_id = o.id),
		  (SELECT count(*) FROM order_events WHERE order_id = o.id AND kind = 'refunded' AND return_request_id = r.id),
		  (SELECT count(*) FROM order_events WHERE order_id = o.id AND kind = 'cancelled'),
		  (SELECT count(*) FROM audit_events WHERE entity_table = 'orders' AND entity_id = o.id AND action = 'order.advance'),
		  (SELECT count(*) FROM outbox_messages WHERE topic = 'order.terminal' AND dedupe_key = o.id::text || ':cancelled_by_staff'),
		  (SELECT count(*) FROM inventory_reservations WHERE order_id = o.id AND state = 'held'),
		  (SELECT count(*) FROM inventory_reservations WHERE order_id = o.id AND state = 'released'),
		  (SELECT count(*) FROM inventory_movements m JOIN inventory_reservations held ON m.source_type = 'reservation' AND m.source_id = held.id WHERE held.order_id = o.id AND m.reason = 'release'),
		  (SELECT count(*) FROM inventory_movements WHERE source_type = 'return_request' AND source_id = r.id),
		  (SELECT stock_quantity FROM product_variants WHERE id = $2)
		FROM orders o JOIN return_requests r ON r.order_id = o.id AND r.before_shipment
		WHERE o.id = $1`, orderID, variantID).Scan(
		&facts.OrderStatus, &facts.ReturnStatus, &facts.FrozenCardCents, &facts.FrozenCreditCents,
		&facts.CardCents, &facts.CardRows, &facts.CreditCents, &facts.CreditRows,
		&facts.ClawedPoints, &facts.Clawbacks, &facts.CancelClawbacks, &facts.CreditReversals,
		&facts.RefundedEvents, &facts.CancelledEvents, &facts.CancelAudits, &facts.TerminalNotices,
		&facts.Held, &facts.Released, &facts.ReleaseMoves, &facts.Restocks, &facts.Stock); err != nil {
		t.Fatalf("read refund completion facts: %v", err)
	}
	return facts
}
