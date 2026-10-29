//go:build integration

package returns_test

import (
	"context"
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/i18n"
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

// followUpFacts is what one return's payout has written so far.
type followUpFacts struct {
	Status          string
	Refunds, Events int
	Clawbacks       int
}

// TestHandler_DecideFollowUp: a closing step that fails after the refund went
// back sends staff to the resend button with a notice that does not point to
// Stripe, whether it fails on the approval or again on the resend, and the
// resend that finishes refunds nothing twice.
func TestHandler_DecideFollowUp(t *testing.T) {
	for _, step := range []struct {
		name, query   string
		eventRecorded int
	}{
		{"refunded event", "RecordReturnRefundedEvent", 0},
		{"points clawback", "ReverseReturnPoints", 1},
	} {
		t.Run(step.name, func(t *testing.T) {
			ctx, _ := admintest.StaffContext(t, pool)
			id, orderID, _ := admintest.LoyaltyReturn(t, pool, []int64{1200000}, 0)
			fault := &followUpFault{query: step.query}
			fault.remaining.Store(2)
			cfg := pool.Config().Copy()
			cfg.MaxConns = 2
			cfg.ConnConfig.Tracer = fault
			cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
				_, err := conn.Exec(ctx, "SET ROLE admin")
				return err
			}
			traced, err := pgxpool.NewWithConfig(t.Context(), cfg)
			if err != nil {
				t.Fatalf("open traced admin pool: %v", err)
			}
			t.Cleanup(traced.Close)
			stripe := admintest.NewStripe(t, admintest.StripeBehaviour{})
			mux := http.NewServeMux()
			handlerOver(storeOver(traced, stripe.Gateway())).Routes(mux, admintest.BackOffice)
			serve := func(req *http.Request) *httptest.ResponseRecorder {
				res := httptest.NewRecorder()
				mux.ServeHTTP(res, req)
				return res
			}
			decide := func() *httptest.ResponseRecorder {
				form := url.Values{"decision": {"approved"}, "confirm": {"approved"}, "resolution": {"Recorded approval"}}
				req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/returns/"+id.String()+"/decide", strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				return serve(req)
			}
			read := func() followUpFacts {
				var f followUpFacts
				if err := pool.QueryRow(ctx, `
					SELECT r.status,
					       (SELECT count(*) FROM refunds WHERE return_request_id = r.id AND status = 'succeeded'),
					       (SELECT count(*) FROM order_events WHERE return_request_id = r.id AND kind = 'refunded'),
					       (SELECT count(*) FROM loyalty_entries WHERE order_id = $2 AND kind = 'clawback')
					FROM return_requests r WHERE r.id = $1`, id, orderID).Scan(&f.Status, &f.Refunds, &f.Events, &f.Clawbacks); err != nil {
					t.Fatalf("read the payout facts: %v", err)
				}
				return f
			}
			wantStuck := followUpFacts{Status: "approved", Refunds: 1, Events: step.eventRecorded}

			for _, press := range []string{"approval", "resend"} {
				res := decide()
				if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/admin/returns?refundfollowup=1" {
					t.Fatalf("%s with the %s failing = %d %q, want 303 with refundfollowup=1",
						press, step.name, res.Code, res.Header().Get("Location"))
				}
				if diff := cmp.Diff(wantStuck, read()); diff != "" {
					t.Fatalf("%s moved money or bookkeeping beyond the settled refund (-want +got):\n%s", press, diff)
				}
				for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
					local := i18n.WithLocale(ctx, locale)
					// The queue is shared with every other test's returns, so it is read
					// through the filter that keeps only this one.
					row := res.Header().Get("Location") + "&request=" + id.String()
					page := html.UnescapeString(serve(httptest.NewRequestWithContext(local, http.MethodGet, row, nil)).Body.String())
					for key, want := range map[i18n.Key]bool{
						i18n.KeyAdminNoticeReturnFollowUp: true,
						i18n.KeyAdminRetPayoutFollowUp:    true,
						i18n.KeyAdminRetRetryPayout:       true,
						i18n.KeyAdminNoticeRefundFailed:   false,
						i18n.KeyAdminRetPayoutOutstanding: false,
					} {
						if got := strings.Contains(page, i18n.T(local, key)); got != want {
							t.Errorf("%s queue after the %s shows %s = %t, want %t", locale, press, key, got, want)
						}
					}
				}
			}
			if got := fault.failed.Load(); got != 2 {
				t.Fatalf("%s statements cancelled = %d, want 2, one for the approval and one for the resend", step.query, got)
			}

			finished := decide()
			if finished.Code != http.StatusSeeOther || finished.Header().Get("Location") != "/admin/returns?approved=1" {
				t.Fatalf("finishing resend = %d %q, want 303 with approved=1", finished.Code, finished.Header().Get("Location"))
			}
			if diff := cmp.Diff(followUpFacts{Status: "approved", Refunds: 1, Events: 1, Clawbacks: 1}, read()); diff != "" {
				t.Errorf("after the finishing resend (-want +got):\n%s", diff)
			}
			if got := len(stripe.Refunds()); got != 1 {
				t.Errorf("provider refunds after the approval, the resend and the finishing resend = %d, want 1", got)
			}
		})
	}
}
