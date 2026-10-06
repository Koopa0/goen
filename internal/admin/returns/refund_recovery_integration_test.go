//go:build integration

package returns_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/admin/admintest"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/admin/refunds"
	"github.com/koopa0/goen/internal/refundstate"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web"
)

func TestRefundRecoveryAttributesEveryProviderAttempt(t *testing.T) {
	ctxA, actorA, requestA := admintest.RefundRecoveryStaffContext(t, pool, "actor-a")
	ctxB, actorB, requestB := admintest.RefundRecoveryStaffContext(t, pool, "actor-b")
	returnID, _ := admintest.ReturnedOrder(t, pool, 2)
	calls := &atomic.Int64{}

	stalled := storeOver(pool, admintest.Refunder{
		RefundErr: errors.New("read tcp 1.2.3.4:443: i/o timeout"),
		Sent:      calls,
	})
	if err := stalled.Decide(ctxA, returnID.String(), "approved", "refund recovery", ""); err == nil {
		t.Fatal("a timed-out refund was reported as complete")
	}

	healthy := storeOver(pool, admintest.Refunder{Sent: calls})
	if err := healthy.Decide(ctxB, returnID.String(), "approved", "refund recovery", ""); err != nil {
		t.Fatalf("retry refund: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("provider Refund calls = %d, want one timed-out call and one retry", got)
	}

	var refundRows int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM refunds WHERE return_request_id = $1`, returnID).
		Scan(&refundRows); err != nil {
		t.Fatalf("count refunds: %v", err)
	}
	if refundRows != 1 {
		t.Fatalf("refund rows = %d, want one durable claim across both attempts", refundRows)
	}

	var refundID uuid.UUID
	if err := pool.QueryRow(t.Context(),
		`SELECT id FROM refunds WHERE return_request_id = $1`, returnID).
		Scan(&refundID); err != nil {
		t.Fatalf("read refund id: %v", err)
	}

	type auditAttribution struct {
		action    string
		actor     uuid.UUID
		requestID string
	}
	rows, err := pool.Query(t.Context(), `
		SELECT action, actor_user_id, request_id
		FROM audit_events
		WHERE entity_table = 'refunds' AND entity_id = $1
		  AND action IN ('refund.provider_attempt', 'refund.provider_succeeded')
		ORDER BY occurred_at, id`, refundID)
	if err != nil {
		t.Fatalf("read refund audits: %v", err)
	}
	defer rows.Close()

	var got []auditAttribution
	for rows.Next() {
		var event auditAttribution
		if err := rows.Scan(&event.action, &event.actor, &event.requestID); err != nil {
			t.Fatalf("scan refund audit: %v", err)
		}
		got = append(got, event)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate refund audits: %v", err)
	}
	want := []auditAttribution{
		{action: "refund.provider_attempt", actor: actorA, requestID: requestA},
		{action: "refund.provider_attempt", actor: actorB, requestID: requestB},
		{action: "refund.provider_succeeded", actor: actorB, requestID: requestB},
	}
	if len(got) != len(want) {
		t.Fatalf("refund audits = %#v, want exactly %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("refund audit %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestEveryKnownTerminalRefundOutcomeGetsOneSuccessor(t *testing.T) {
	tests := []struct {
		name       string
		refunder   admintest.Refunder
		firstState string
		firstRef   bool
	}{
		{
			name: "failed provider object", refunder: admintest.Refunder{State: refundstate.Failed},
			firstState: "failed", firstRef: true,
		},
		{
			name: "cancelled provider object", refunder: admintest.Refunder{State: refundstate.Cancelled},
			firstState: "cancelled", firstRef: true,
		},
		{
			name: "create API rejection",
			refunder: admintest.Refunder{RefundErr: fmt.Errorf(
				"%w: provider rejected create", refunds.ErrCreateRejected)},
			firstState: "failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := admintest.StaffContext(t, pool)
			returnID, _ := admintest.ReturnedOrder(t, pool, 1)
			if err := storeOver(pool, tt.refunder).Decide(
				ctx, returnID.String(), "approved", "terminal recovery", "",
			); err == nil {
				t.Fatal("terminal provider outcome was reported as a settled refund")
			}
			if err := storeOver(pool, admintest.Refunder{}).Decide(
				ctx, returnID.String(), "approved", "terminal recovery", "",
			); err != nil {
				t.Fatalf("retry terminal provider outcome: %v", err)
			}

			rows, err := pool.Query(ctx, `
				SELECT id, previous_refund_id, attempt_no, request_key, status,
				       provider_ref IS NOT NULL
				FROM refunds WHERE return_request_id = $1 ORDER BY attempt_no`, returnID)
			if err != nil {
				t.Fatalf("read refund generations: %v", err)
			}
			defer rows.Close()
			type generation struct {
				id, previous uuid.NullUUID
				number       int32
				key, status  string
				hasRef       bool
			}
			var got []generation
			for rows.Next() {
				var g generation
				if err := rows.Scan(&g.id, &g.previous, &g.number, &g.key, &g.status, &g.hasRef); err != nil {
					t.Fatalf("scan refund generation: %v", err)
				}
				got = append(got, g)
			}
			if err := rows.Err(); err != nil {
				t.Fatalf("iterate refund generations: %v", err)
			}
			base := "return:" + returnID.String()
			if len(got) != 2 {
				t.Fatalf("refund generations = %#v, want two", got)
			}
			if got[0].number != 1 || got[0].key != base || got[0].status != tt.firstState ||
				got[0].hasRef != tt.firstRef || got[0].previous.Valid {
				t.Errorf("first generation = %#v, want terminal %q with provider ref=%v",
					got[0], tt.firstState, tt.firstRef)
			}
			if got[1].number != 2 || got[1].key != base+":attempt:2" ||
				got[1].status != "succeeded" || !got[1].hasRef || !got[1].previous.Valid ||
				got[1].previous.UUID != got[0].id.UUID {
				t.Errorf("successor generation = %#v, want linked succeeded attempt 2", got[1])
			}
		})
	}
}

func TestRefundRecoveryRequiresActorAndRequestIDBeforeProviderCall(t *testing.T) {
	tests := []struct {
		name    string
		context func(*testing.T) context.Context
	}{
		{
			name: "missing actor",
			context: func(t *testing.T) context.Context {
				t.Helper()
				requestID := "refund-no-actor-" + uuid.NewString()[:8]
				return web.WithRequestID(t.Context(), requestID)
			},
		},
		{
			name: "missing request id",
			context: func(t *testing.T) context.Context {
				t.Helper()
				_, actor := admintest.StaffContext(t, pool)
				ctx := user.NewContext(t.Context(), user.User{
					ID: actor.String(), Role: user.RoleAdmin,
				})
				return ctx
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := tt.context(t)
			returnID := admintest.PreapprovedReturn(t, pool)
			calls := &atomic.Int64{}
			s := storeOver(pool, admintest.Refunder{Sent: calls})

			if err := s.Decide(ctx, returnID.String(), "approved", "retry", ""); err == nil {
				t.Fatal("refund execution without its durable request identity succeeded")
			}
			if got := calls.Load(); got != 0 {
				t.Errorf("provider Refund calls = %d, want zero", got)
			}

			var refundRows, attempts int
			if err := pool.QueryRow(t.Context(),
				`SELECT count(*) FROM refunds WHERE return_request_id = $1`, returnID).
				Scan(&refundRows); err != nil {
				t.Fatalf("count refunds: %v", err)
			}
			if err := pool.QueryRow(t.Context(), `
				SELECT count(*) FROM audit_events
				WHERE action = 'refund.provider_attempt'
				  AND after ->> 'request_key' = $1`, "return:"+returnID.String()).
				Scan(&attempts); err != nil {
				t.Fatalf("count attempt audits: %v", err)
			}
			if refundRows != 0 || attempts != 0 {
				t.Errorf("missing identity left refunds/audits = %d/%d, want 0/0",
					refundRows, attempts)
			}
		})
	}
}

func TestRefundAttemptAuditFailureRollsBackClaim(t *testing.T) {
	ctx, _, requestID := admintest.RefundRecoveryStaffContext(t, pool, "attempt-audit-failure")
	returnID, _ := admintest.ReturnedOrder(t, pool, 1)
	installRefundAuditRejector(t, "refund.provider_attempt")
	calls := &atomic.Int64{}
	s := storeOver(pool, admintest.Refunder{Sent: calls})

	if err := s.Decide(ctx, returnID.String(), "approved", "audit rollback", ""); err == nil {
		t.Fatal("refund claim succeeded despite its rejected attempt audit")
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("provider Refund calls = %d, want zero before a durable attempt audit", got)
	}

	var refundRows, attempts int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM refunds WHERE return_request_id = $1`, returnID).
		Scan(&refundRows); err != nil {
		t.Fatalf("count refunds: %v", err)
	}
	if err := pool.QueryRow(t.Context(), `
		SELECT count(*) FROM audit_events
		WHERE action = 'refund.provider_attempt' AND request_id = $1`, requestID).
		Scan(&attempts); err != nil {
		t.Fatalf("count attempt audits: %v", err)
	}
	if refundRows != 0 || attempts != 0 {
		t.Errorf("failed claim left refunds/audits = %d/%d, want 0/0", refundRows, attempts)
	}
}

func TestRefundSucceededAuditFailureRollsBackOutcome(t *testing.T) {
	ctx, _, requestID := admintest.RefundRecoveryStaffContext(t, pool, "outcome-audit-failure")
	returnID, _ := admintest.ReturnedOrder(t, pool, 1)
	installRefundAuditRejector(t, "refund.provider_succeeded")
	calls := &atomic.Int64{}
	s := storeOver(pool, admintest.Refunder{Sent: calls})

	if err := s.Decide(ctx, returnID.String(), "approved", "audit rollback", ""); err == nil {
		t.Fatal("refund outcome succeeded despite its rejected outcome audit")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("provider Refund calls = %d, want one call after the durable claim", got)
	}

	var status string
	var providerRef *string
	var succeededAt, failedAt *time.Time
	if err := pool.QueryRow(t.Context(), `
		SELECT status, provider_ref, succeeded_at, failed_at
		FROM refunds WHERE return_request_id = $1`, returnID).
		Scan(&status, &providerRef, &succeededAt, &failedAt); err != nil {
		t.Fatalf("read refund after rejected outcome audit: %v", err)
	}
	if status != "pending" || providerRef != nil || succeededAt != nil || failedAt != nil {
		t.Errorf("refund after outcome rollback = status %q ref %v succeeded %v failed %v; "+
			"want pending with no provider identity or timestamps",
			status, providerRef, succeededAt, failedAt)
	}

	var attempts, outcomes int
	if err := pool.QueryRow(t.Context(), `
		SELECT count(*) FILTER (WHERE action = 'refund.provider_attempt'),
		       count(*) FILTER (WHERE action = 'refund.provider_succeeded')
		FROM audit_events WHERE request_id = $1`, requestID).
		Scan(&attempts, &outcomes); err != nil {
		t.Fatalf("count refund audits: %v", err)
	}
	if attempts != 1 || outcomes != 0 {
		t.Errorf("attempt/outcome audits = %d/%d, want 1/0", attempts, outcomes)
	}
}

func installRefundAuditRejector(t *testing.T, action string) {
	t.Helper()
	ctx := t.Context()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := pgx.Identifier{"test_reject_refund_audit_" + suffix}.Sanitize()
	triggerName := pgx.Identifier{"test_reject_refund_audit_" + suffix}.Sanitize()
	constraintName := "test_" + strings.ReplaceAll(action, ".", "_") + "_audit_rejected"
	actionLiteral := "'" + strings.ReplaceAll(action, "'", "''") + "'"
	constraintLiteral := "'" + strings.ReplaceAll(constraintName, "'", "''") + "'"

	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $body$
		BEGIN
			IF NEW.action = %s THEN
				RAISE EXCEPTION 'forced refund audit rejection'
					USING ERRCODE = 'check_violation', CONSTRAINT = %s;
			END IF;
			RETURN NEW;
		END
		$body$;
		CREATE TRIGGER %s BEFORE INSERT ON audit_events
		FOR EACH ROW EXECUTE FUNCTION %s()`,
		functionName, actionLiteral, constraintLiteral, triggerName, functionName)); err != nil {
		t.Fatalf("install %s audit rejector: %v", action, err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := pool.Exec(cleanupCtx, fmt.Sprintf(
			"DROP TRIGGER IF EXISTS %s ON audit_events; DROP FUNCTION IF EXISTS %s()",
			triggerName, functionName)); err != nil {
			t.Errorf("remove %s audit rejector: %v", action, err)
		}
	})
}
