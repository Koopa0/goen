//go:build integration

package returns_test

import (
	"context"
	"errors"
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
	"github.com/koopa0/goen/internal/i18n"
)

type failedDecisionAuditKey struct{}

type failedDecisionAudit struct {
	returnID uuid.UUID
	fired    atomic.Bool
	failed   atomic.Bool
}

func (f *failedDecisionAudit) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !strings.HasPrefix(data.SQL, "-- name: RecordAuditEvent :one\n") || len(data.Args) < 4 {
		return ctx
	}
	id, ok := data.Args[3].(uuid.NullUUID)
	if !ok || !id.Valid || id.UUID != f.returnID || !f.fired.CompareAndSwap(false, true) {
		return ctx
	}
	// Cancel only the actual audit query, after the decision write. The request
	// remains live so the handler can render its failure and the transaction can roll back.
	query, cancel := context.WithCancel(context.WithValue(ctx, failedDecisionAuditKey{}, true))
	cancel()
	return query
}

func (f *failedDecisionAudit) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if marked, _ := ctx.Value(failedDecisionAuditKey{}).(bool); marked && errors.Is(data.Err, context.Canceled) {
		f.failed.Store(true)
	}
}

func TestReturnDecisionAuditFailureDoesNotClaimApproval(t *testing.T) {
	for _, locale := range []struct {
		locale       i18n.Locale
		title, retry string
		committed    string
	}{
		{locale: i18n.ZhHant, title: "暫時無法處理", retry: "請稍後再試。", committed: "這筆退貨已核准"},
		{locale: i18n.En, title: "Temporarily unavailable", retry: "Please try again shortly.", committed: "This return is approved"},
	} {
		for _, decision := range []string{"approved", "rejected"} {
			t.Run(string(locale.locale)+"/"+decision, func(t *testing.T) {
				staff, _ := admintest.StaffContext(t, pool)
				ctx := i18n.WithLocale(staff, locale.locale)
				id, _ := admintest.ReturnedOrder(t, pool, 1)
				fault := &failedDecisionAudit{returnID: id}
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
				var role string
				if err := traced.QueryRow(ctx, "SELECT current_user").Scan(&role); err != nil || role != "admin" {
					t.Fatalf("current_user = %q, want admin: %v", role, err)
				}
				var paid atomic.Int64
				h := handlerOver(storeOver(traced, admintest.Refunder{Sent: &paid}))
				mux := http.NewServeMux()
				h.Routes(mux, admintest.BackOffice)
				form := url.Values{"decision": {decision}, "confirm": {decision}, "resolution": {"Recorded decision"}}
				req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/returns/"+id.String()+"/decide", strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				res := httptest.NewRecorder()
				mux.ServeHTTP(res, req)
				if !fault.fired.Load() || !fault.failed.Load() || ctx.Err() != nil {
					t.Fatalf("decision audit fault fired = %t, failed = %t, request error = %v; want the audit query cancelled with a live request", fault.fired.Load(), fault.failed.Load(), ctx.Err())
				}
				type response struct {
					Status   int
					Location string
				}
				if diff := cmp.Diff(response{Status: http.StatusInternalServerError}, response{Status: res.Code, Location: res.Header().Get("Location")}); diff != "" {
					t.Errorf("failed decision response (-want +got):\n%s", diff)
				}
				body := res.Body.String()
				if !strings.Contains(body, locale.title) || !strings.Contains(body, locale.retry) || strings.Contains(body, locale.committed) || strings.Contains(body, "Stripe") || strings.Contains(body, "context canceled") {
					t.Errorf("failed decision must show %q and %q without a committed approval or audit cause; body=%s", locale.title, locale.retry, body)
				}
				type decisionState struct {
					Status, Resolution string
					Decided            bool
					Refunds, Audits    int
					ProviderPayments   int64
				}
				var got decisionState
				if err := pool.QueryRow(ctx, `
					SELECT r.status, coalesce(r.resolution, ''), r.decided_at IS NOT NULL,
					       (SELECT count(*) FROM refunds WHERE return_request_id = r.id),
					       (SELECT count(*) FROM audit_events WHERE entity_table = 'return_requests' AND entity_id = r.id)
					FROM return_requests r WHERE r.id = $1`, id).Scan(&got.Status, &got.Resolution, &got.Decided, &got.Refunds, &got.Audits); err != nil {
					t.Fatalf("read failed decision: %v", err)
				}
				got.ProviderPayments = paid.Load()
				if diff := cmp.Diff(decisionState{Status: "requested"}, got); diff != "" {
					t.Errorf("failed decision state (-want +got):\n%s", diff)
				}
			})
		}
	}
}
