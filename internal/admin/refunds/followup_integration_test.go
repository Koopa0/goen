//go:build integration

package refunds_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/refunds"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/payment/refund"
)

// followUpFault fails the first n statements of one sqlc query by handing pgx
// a context that is already cancelled. The query is one of the two closing
// steps of a payout, so by the time it fails every source has settled.
type followUpFault struct {
	query     string
	remaining atomic.Int32
	failed    atomic.Int32
}

type followUpFaultKey struct{}

func newFollowUpFault(query string, n int32) *followUpFault {
	f := &followUpFault{query: query}
	f.remaining.Store(n)
	return f
}

func (f *followUpFault) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !strings.HasPrefix(data.SQL, "-- name: "+f.query+" :") || f.remaining.Add(-1) < 0 {
		return ctx
	}
	// The caller's context stays live so the handler can answer after the failure.
	cancelled, cancel := context.WithCancel(context.WithValue(ctx, followUpFaultKey{}, true))
	cancel()
	return cancelled
}

func (f *followUpFault) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if marked, _ := ctx.Value(followUpFaultKey{}).(bool); marked && errors.Is(data.Err, context.Canceled) {
		f.failed.Add(1)
	}
}

// followUpSteps are the two closing steps of a payout, each named by the query
// that does it and by the facts it leaves undone.
var followUpSteps = []struct {
	name, query               string
	eventRecorded, clawedBack bool
}{
	{"refunded event", "RecordReturnRefundedEvent", false, false},
	{"points clawback", "ReverseReturnPoints", true, false},
}

// TestHandler_RefundBeforeShipmentFollowUp: when the money has gone back and a
// closing step fails, the order page says so instead of sending staff to Stripe,
// whether the step fails on the press or again on Resume, and the Resume that
// finishes pays nothing twice.
func TestHandler_RefundBeforeShipmentFollowUp(t *testing.T) {
	for _, step := range followUpSteps {
		t.Run(step.name, func(t *testing.T) {
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

			fault := newFollowUpFault(step.query, 2)
			stripe := admintest.NewStripe(t, admintest.StripeBehaviour{})
			adminPool := refundCompletionPool(t, fault)
			gateway := stripe.Gateway()
			diagnostic := &refundCompletionLog{}
			handler := refunds.NewHandler(refunds.NewStore(adminPool, gateway, nil), nil, slog.New(diagnostic))

			wantStuck := refundCompletionFacts{
				OrderStatus: "picking", ReturnStatus: "approved", CardCents: 900000, CardRows: 1,
				CreditCents: 300000, CreditRows: 1, FrozenCardCents: 900000, FrozenCreditCents: 300000,
				Held: 1, Stock: stockHeld,
			}
			if step.eventRecorded {
				wantStuck.RefundedEvents = 1
			}
			for _, press := range []string{"press", "Resume"} {
				response := postRefundCompletion(ctx, handler, number, true)
				if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin/orders/"+number+"?refundfollowup=1" {
					t.Fatalf("%s with the %s failing = %d %q, want 303 with refundfollowup=1",
						press, step.name, response.Code, response.Header().Get("Location"))
				}
				if !errors.Is(diagnostic.err, refund.ErrFollowUpIncomplete) || !errors.Is(diagnostic.err, refund.ErrIncomplete) ||
					errors.Is(diagnostic.err, refunds.ErrCancellationIncomplete) {
					t.Errorf("%s diagnostic = %v, want a closing step incomplete without cancellation incomplete", press, diagnostic.err)
				}
				if diff := cmp.Diff(wantStuck, readRefundCompletionFacts(t, orderID, variantID)); diff != "" {
					t.Fatalf("%s moved money or bookkeeping beyond the settled refund (-want +got):\n%s", press, diff)
				}
				assertRefundRecoveryPage(t, ctx, adminPool, gateway, number, response.Header().Get("Location"),
					i18n.KeyAdminNoticeRefundFollowUp, i18n.KeyAdminNoticeRefundRetry, i18n.KeyAdminNoticeCancelRetry)
			}
			if got := fault.failed.Load(); got != 2 {
				t.Fatalf("%s statements cancelled = %d, want 2, one for the press and one for Resume", step.query, got)
			}

			finished := postRefundCompletion(ctx, handler, number, true)
			if finished.Code != http.StatusSeeOther || finished.Header().Get("Location") != "/admin/orders/"+number+"?refunded=1" {
				t.Fatalf("Resume = %d %q, want 303 with refunded=1", finished.Code, finished.Header().Get("Location"))
			}
			wantFinished := wantStuck
			wantFinished.OrderStatus, wantFinished.ReturnStatus = "cancelled", "completed"
			wantFinished.RefundedEvents, wantFinished.ClawedPoints, wantFinished.Clawbacks = 1, -awarded, 1
			wantFinished.CancelledEvents, wantFinished.CancelAudits, wantFinished.TerminalNotices = 1, 1, 1
			wantFinished.Held, wantFinished.Released, wantFinished.ReleaseMoves = 0, 1, 1
			wantFinished.Stock++
			if diff := cmp.Diff(wantFinished, readRefundCompletionFacts(t, orderID, variantID)); diff != "" {
				t.Errorf("Resume result (-want +got):\n%s", diff)
			}
			if got := len(stripe.Refunds()); got != 1 {
				t.Errorf("provider refunds after the press, Resume and the finishing Resume = %d, want 1", got)
			}
		})
	}
}

// TestHandler_RefundUnsentFollowUp: a refund of unsent units answers a closing
// step that fails after the money went back with the same notice, and the
// Resume that finishes pays and writes nothing twice.
func TestHandler_RefundUnsentFollowUp(t *testing.T) {
	for _, step := range followUpSteps {
		t.Run(step.name, func(t *testing.T) {
			ctx, _ := admintest.StaffContext(t, pool)
			o := admintest.PartlyShippedOrder(t, pool, admintest.TwoLineOrderTotal)
			fault := newFollowUpFault(step.query, 2)
			stripe := admintest.NewStripe(t, admintest.StripeBehaviour{})
			adminPool := refundCompletionPool(t, fault)
			gateway := stripe.Gateway()
			diagnostic := &refundCompletionLog{}
			handler := refunds.NewHandler(refunds.NewStore(adminPool, gateway, nil), nil, slog.New(diagnostic))
			form := url.Values{
				"qty_" + o.LineB.String(): {"2"}, "reason": {"customer_withdrew"},
				"confirm": {"refund-unsent"}, "total": {"92600"}, "note": {unsentNote},
			}
			press := func() *httptest.ResponseRecorder {
				req := httptest.NewRequestWithContext(ctx, http.MethodPost,
					"/admin/orders/"+o.Number+"/refund-unsent", strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				response := httptest.NewRecorder()
				mux := http.NewServeMux()
				handler.Routes(mux, admintest.BackOffice)
				mux.ServeHTTP(response, req)
				return response
			}

			for _, which := range []string{"press", "Resume"} {
				response := press()
				if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin/orders/"+o.Number+"?refundfollowup=1" {
					t.Fatalf("%s with the %s failing = %d %q, want 303 with refundfollowup=1",
						which, step.name, response.Code, response.Header().Get("Location"))
				}
				if !errors.Is(diagnostic.err, refund.ErrFollowUpIncomplete) || !errors.Is(diagnostic.err, refund.ErrIncomplete) {
					t.Errorf("%s diagnostic = %v, want a closing step incomplete", which, diagnostic.err)
				}
				stuck := readUnsent(t, &o)
				wantEvents := int64(0)
				if step.eventRecorded {
					wantEvents = 1
				}
				if stuck.status != "approved" || stuck.cardPaid != unsentBRefund || stuck.providerRows != 1 ||
					stuck.refundedEvents != wantEvents || stuck.clawbacks != 0 || stuck.mails != 0 || stuck.releasedB != 0 {
					t.Fatalf("%s left status %s, %d paid on the card in %d rows, %d refunded events, %d clawbacks, %d letters, %d units released; "+
						"want an approved refund, %d paid once, %d refunded events and nothing after them",
						which, stuck.status, stuck.cardPaid, stuck.providerRows, stuck.refundedEvents, stuck.clawbacks,
						stuck.mails, stuck.releasedB, unsentBRefund, wantEvents)
				}
				assertRefundRecoveryPage(t, ctx, adminPool, gateway, o.Number, response.Header().Get("Location"),
					i18n.KeyAdminNoticeRefundFollowUp, i18n.KeyAdminNoticeRefundRetry, i18n.KeyAdminNoticeCancelRetry)
			}
			if got := fault.failed.Load(); got != 2 {
				t.Fatalf("%s statements cancelled = %d, want 2, one for the press and one for Resume", step.query, got)
			}

			finished := press()
			if finished.Code != http.StatusSeeOther || finished.Header().Get("Location") != "/admin/orders/"+o.Number+"?unsentrefunded=1" {
				t.Fatalf("finishing Resume = %d %q, want 303 with unsentrefunded=1", finished.Code, finished.Header().Get("Location"))
			}
			f := readUnsent(t, &o)
			if f.status != "completed" || f.cardPaid != unsentBRefund || f.providerRows != 1 || len(stripe.Refunds()) != 1 ||
				f.refundedEvents != 1 || f.clawbacks != 1 || f.clawed >= 0 || f.mails != 1 || f.releasedB != 2 ||
				f.openAudits != 1 || f.completeAudits != 1 {
				t.Errorf("after Resume: %+v with %d provider refunds, want a completed refund paid once with one event, one clawback and one letter",
					f, len(stripe.Refunds()))
			}
		})
	}
}
