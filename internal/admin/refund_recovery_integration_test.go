//go:build integration

package admin_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/web"
)

func TestRefundRecoveryAttributesEveryProviderAttempt(t *testing.T) {
	ctxA, actorA, requestA := refundRecoveryStaffContext(t, "actor-a")
	ctxB, actorB, requestB := refundRecoveryStaffContext(t, "actor-b")
	returnID, _ := returnedOrder(t, 2)
	calls := &atomic.Int64{}

	stalled := admin.NewStore(pool, fakeRefunder{
		refundErr: errors.New("read tcp 1.2.3.4:443: i/o timeout"),
		sent:      calls,
	}, nil, nil)
	if err := stalled.Decide(ctxA, returnID.String(), "approved", "refund recovery", uuid.NullUUID{
		UUID: actorA, Valid: true,
	}); err == nil {
		t.Fatal("a timed-out refund was reported as complete")
	}

	healthy := admin.NewStore(pool, fakeRefunder{sent: calls}, nil, nil)
	if err := healthy.Decide(ctxB, returnID.String(), "approved", "refund recovery", uuid.NullUUID{
		UUID: actorB, Valid: true,
	}); err != nil {
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
		refunder   fakeRefunder
		firstState string
		firstRef   bool
	}{
		{
			name: "failed provider object", refunder: fakeRefunder{state: admin.RefundFailed},
			firstState: "failed", firstRef: true,
		},
		{
			name: "cancelled provider object", refunder: fakeRefunder{state: admin.RefundCancelled},
			firstState: "cancelled", firstRef: true,
		},
		{
			name: "create API rejection",
			refunder: fakeRefunder{refundErr: fmt.Errorf(
				"%w: provider rejected create", admin.ErrRefundCreateRejected)},
			firstState: "failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := staffContext(t)
			returnID, _ := returnedOrder(t, 1)
			if err := admin.NewStore(pool, tt.refunder, nil, nil).Decide(
				ctx, returnID.String(), "approved", "terminal recovery", uuid.NullUUID{},
			); err == nil {
				t.Fatal("terminal provider outcome was reported as a settled refund")
			}
			if err := admin.NewStore(pool, fakeRefunder{}, nil, nil).Decide(
				ctx, returnID.String(), "approved", "terminal recovery", uuid.NullUUID{},
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

func TestConcurrentTerminalRefundRetriesShareOneSuccessor(t *testing.T) {
	ctxA, actorA, requestA := refundRecoveryStaffContext(t, "terminal-race-a")
	ctxB, actorB, requestB := refundRecoveryStaffContext(t, "terminal-race-b")
	returnID := preapprovedRefundRecoveryReturn(t)

	var firstID uuid.UUID
	if err := pool.QueryRow(ctxA,
		`SELECT claim_return_refund_execution($1, $2, $3)`,
		returnID, actorA, requestA).Scan(&firstID); err != nil {
		t.Fatalf("claim first generation: %v", err)
	}
	if _, err := pool.Exec(ctxA,
		`SELECT record_refund_failed($1, $2, $3, $4)`,
		firstID, "re_terminal_race_"+uuid.NewString(), actorA, requestA); err != nil {
		t.Fatalf("record first generation failed: %v", err)
	}

	type result struct {
		id  uuid.UUID
		err error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	claim := func(ctx context.Context, actor uuid.UUID, requestID string) {
		<-start
		var id uuid.UUID
		err := pool.QueryRow(ctx,
			`SELECT claim_return_refund_execution($1, $2, $3)`,
			returnID, actor, requestID).Scan(&id)
		results <- result{id: id, err: err}
	}
	go claim(ctxA, actorA, requestA+"-retry")
	go claim(ctxB, actorB, requestB+"-retry")
	close(start)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("concurrent successor claims = %v / %v", first.err, second.err)
	}
	if first.id != second.id || first.id == firstID {
		t.Fatalf("concurrent successor ids = %s/%s after %s, want one new id",
			first.id, second.id, firstID)
	}

	var attempts, successors int
	var number int32
	var previous uuid.NullUUID
	var key string
	if err := pool.QueryRow(t.Context(), `
		SELECT count(*), count(*) FILTER (WHERE previous_refund_id = $2)
		FROM refunds WHERE return_request_id = $1`, returnID, firstID).
		Scan(&attempts, &successors); err != nil {
		t.Fatalf("count concurrent generations: %v", err)
	}
	if err := pool.QueryRow(t.Context(), `
		SELECT attempt_no, previous_refund_id, request_key FROM refunds WHERE id = $1`, first.id).
		Scan(&number, &previous, &key); err != nil {
		t.Fatalf("read concurrent successor: %v", err)
	}
	if attempts != 2 || successors != 1 || number != 2 || !previous.Valid ||
		previous.UUID != firstID || key != "return:"+returnID.String()+":attempt:2" {
		t.Errorf("attempts/successors/generation/previous/key = %d/%d/%d/%v/%q",
			attempts, successors, number, previous, key)
	}
}

// TestProviderOutcomeAndRetrySharePaymentBeforeRefund forces the former
// payment/refund ABBA cycle. The outcome pauses inside its refund UPDATE while a
// retry claim reaches the same aggregate; success must remain durable and the
// retry must observe it as settled, not kill either transaction as a deadlock
// victim.
func TestProviderOutcomeAndRetrySharePaymentBeforeRefund(t *testing.T) {
	ctxA, actorA, requestA := refundRecoveryStaffContext(t, "outcome-lock-a")
	ctxB, actorB, requestB := refundRecoveryStaffContext(t, "outcome-lock-b")
	returnID := preapprovedRefundRecoveryReturn(t)
	var refundID uuid.UUID
	if err := pool.QueryRow(ctxA,
		`SELECT claim_return_refund_execution($1, $2, $3)`,
		returnID, actorA, requestA).Scan(&refundID); err != nil {
		t.Fatalf("claim pending refund: %v", err)
	}

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := pgx.Identifier{"aaa_test_pause_refund_outcome_" + suffix}.Sanitize()
	triggerName := pgx.Identifier{"aaa_test_pause_refund_outcome_" + suffix}.Sanitize()
	const barrierKey int64 = 8_812_233_445_566_782
	if _, err := pool.Exec(t.Context(), fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $body$
		BEGIN
			IF NEW.id = '%s'::uuid THEN
				PERFORM pg_advisory_xact_lock(%d);
			END IF;
			RETURN NEW;
		END
		$body$;
		CREATE TRIGGER %s BEFORE UPDATE ON refunds
		FOR EACH ROW EXECUTE FUNCTION %s()`,
		functionName, refundID, barrierKey, triggerName, functionName)); err != nil {
		t.Fatalf("install refund outcome barrier: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, fmt.Sprintf(
			"DROP TRIGGER IF EXISTS %s ON refunds; DROP FUNCTION IF EXISTS %s()",
			triggerName, functionName))
	})

	blocker, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin refund outcome barrier: %v", err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(t.Context())) }()
	if _, lockErr := blocker.Exec(t.Context(), `SELECT pg_advisory_xact_lock($1)`, barrierKey); lockErr != nil {
		t.Fatalf("hold refund outcome barrier: %v", lockErr)
	}
	outcomeConn, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatalf("acquire outcome connection: %v", err)
	}
	defer outcomeConn.Release()
	retryConn, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatalf("acquire retry connection: %v", err)
	}
	defer retryConn.Release()
	var outcomePID, retryPID int
	if err := outcomeConn.QueryRow(t.Context(), `SELECT pg_backend_pid()`).Scan(&outcomePID); err != nil {
		t.Fatalf("read outcome backend: %v", err)
	}
	if err := retryConn.QueryRow(t.Context(), `SELECT pg_backend_pid()`).Scan(&retryPID); err != nil {
		t.Fatalf("read retry backend: %v", err)
	}

	type outcomeResult struct {
		changed bool
		err     error
	}
	outcomeResultCh := make(chan outcomeResult, 1)
	outcomeDone := make(chan struct{})
	providerRef := "re_outcome_lock_" + uuid.NewString()
	go func() {
		defer close(outcomeDone)
		var changed bool
		err := outcomeConn.QueryRow(context.WithoutCancel(ctxA),
			`SELECT record_refund_succeeded($1, $2, $3, $4)`,
			refundID, providerRef, actorA, requestA+"-settle").Scan(&changed)
		outcomeResultCh <- outcomeResult{changed: changed, err: err}
	}()
	waitForBackendLock(t, outcomePID, outcomeDone)

	type retryResult struct {
		id  uuid.UUID
		err error
	}
	retryResultCh := make(chan retryResult, 1)
	retryDone := make(chan struct{})
	go func() {
		defer close(retryDone)
		var id uuid.UUID
		err := retryConn.QueryRow(context.WithoutCancel(ctxB),
			`SELECT claim_return_refund_execution($1, $2, $3)`,
			returnID, actorB, requestB+"-retry").Scan(&id)
		retryResultCh <- retryResult{id: id, err: err}
	}()
	waitForBackendLock(t, retryPID, retryDone)

	if err := blocker.Commit(t.Context()); err != nil {
		t.Fatalf("release refund outcome barrier: %v", err)
	}
	select {
	case result := <-outcomeResultCh:
		if result.err != nil || !result.changed {
			t.Fatalf("provider outcome = changed %v, error %v; want durable success",
				result.changed, result.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("provider outcome did not finish after its barrier was released")
	}
	select {
	case result := <-retryResultCh:
		if result.err == nil {
			t.Fatalf("retry returned refund %s after the provider outcome settled", result.id)
		}
		if constraint := constraintFrom(result.err); constraint != "refunds_execution_settled" {
			t.Fatalf("concurrent retry = %v (constraint %q), want settled refusal without deadlock",
				result.err, constraint)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("retry claim did not finish after provider outcome committed")
	}

	var status string
	var attempts int
	if err := pool.QueryRow(t.Context(), `
		SELECT max(status), count(*) FROM refunds WHERE return_request_id = $1`, returnID).
		Scan(&status, &attempts); err != nil {
		t.Fatalf("read outcome/retry result: %v", err)
	}
	if status != "succeeded" || attempts != 1 {
		t.Errorf("outcome/retry status/attempts = %s/%d, want succeeded/1", status, attempts)
	}
}

func TestRefundRecoveryRequiresActorAndRequestIDBeforeProviderCall(t *testing.T) {
	tests := []struct {
		name    string
		context func(*testing.T) (context.Context, uuid.NullUUID)
	}{
		{
			name: "missing actor",
			context: func(t *testing.T) (context.Context, uuid.NullUUID) {
				t.Helper()
				requestID := "refund-no-actor-" + uuid.NewString()[:8]
				return web.WithRequestID(t.Context(), requestID), uuid.NullUUID{}
			},
		},
		{
			name: "missing request id",
			context: func(t *testing.T) (context.Context, uuid.NullUUID) {
				t.Helper()
				_, actor := staffContext(t)
				ctx := account.WithUser(t.Context(), account.User{
					ID: actor.String(), Role: "admin",
				})
				return ctx, uuid.NullUUID{UUID: actor, Valid: true}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, actor := tt.context(t)
			returnID := preapprovedRefundRecoveryReturn(t)
			calls := &atomic.Int64{}
			s := admin.NewStore(pool, fakeRefunder{sent: calls}, nil, nil)

			if err := s.Decide(ctx, returnID.String(), "approved", "retry", actor); err == nil {
				t.Fatal("refund execution without its durable request identity succeeded")
			}
			if got := calls.Load(); got != 0 {
				t.Errorf("provider Refund calls = %d, want zero", got)
			}

			var refunds, attempts int
			if err := pool.QueryRow(t.Context(),
				`SELECT count(*) FROM refunds WHERE return_request_id = $1`, returnID).
				Scan(&refunds); err != nil {
				t.Fatalf("count refunds: %v", err)
			}
			if err := pool.QueryRow(t.Context(), `
				SELECT count(*) FROM audit_events
				WHERE action = 'refund.provider_attempt'
				  AND after ->> 'request_key' = $1`, "return:"+returnID.String()).
				Scan(&attempts); err != nil {
				t.Fatalf("count attempt audits: %v", err)
			}
			if refunds != 0 || attempts != 0 {
				t.Errorf("missing identity left refunds/audits = %d/%d, want 0/0",
					refunds, attempts)
			}
		})
	}
}

func TestRefundAttemptAuditFailureRollsBackClaim(t *testing.T) {
	ctx, actor, requestID := refundRecoveryStaffContext(t, "attempt-audit-failure")
	returnID, _ := returnedOrder(t, 1)
	installRefundAuditRejector(t, "refund.provider_attempt")
	calls := &atomic.Int64{}
	s := admin.NewStore(pool, fakeRefunder{sent: calls}, nil, nil)

	if err := s.Decide(ctx, returnID.String(), "approved", "audit rollback", uuid.NullUUID{
		UUID: actor, Valid: true,
	}); err == nil {
		t.Fatal("refund claim succeeded despite its rejected attempt audit")
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("provider Refund calls = %d, want zero before a durable attempt audit", got)
	}

	var refunds, attempts int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM refunds WHERE return_request_id = $1`, returnID).
		Scan(&refunds); err != nil {
		t.Fatalf("count refunds: %v", err)
	}
	if err := pool.QueryRow(t.Context(), `
		SELECT count(*) FROM audit_events
		WHERE action = 'refund.provider_attempt' AND request_id = $1`, requestID).
		Scan(&attempts); err != nil {
		t.Fatalf("count attempt audits: %v", err)
	}
	if refunds != 0 || attempts != 0 {
		t.Errorf("failed claim left refunds/audits = %d/%d, want 0/0", refunds, attempts)
	}
}

func TestRefundSucceededAuditFailureRollsBackOutcome(t *testing.T) {
	ctx, actor, requestID := refundRecoveryStaffContext(t, "outcome-audit-failure")
	returnID, _ := returnedOrder(t, 1)
	installRefundAuditRejector(t, "refund.provider_succeeded")
	calls := &atomic.Int64{}
	s := admin.NewStore(pool, fakeRefunder{sent: calls}, nil, nil)

	if err := s.Decide(ctx, returnID.String(), "approved", "audit rollback", uuid.NullUUID{
		UUID: actor, Valid: true,
	}); err == nil {
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

func TestEveryRefundOutcomeDoorRecordsItsProviderFact(t *testing.T) {
	tests := []struct {
		name          string
		query         string
		status        string
		action        string
		providerEntry bool
		wantSucceeded bool
		wantFailed    bool
	}{
		{
			name: "pending", query: `SELECT record_refund_pending($1, $2, $3, $4)`,
			status: "pending", action: "refund.provider_pending", providerEntry: true,
		},
		{
			name: "requires action", query: `SELECT record_refund_requires_action($1, $2, $3, $4)`,
			status: "requires_action", action: "refund.provider_requires_action", providerEntry: true,
		},
		{
			name: "succeeded", query: `SELECT record_refund_succeeded($1, $2, $3, $4)`,
			status: "succeeded", action: "refund.provider_succeeded", providerEntry: true,
			wantSucceeded: true,
		},
		{
			name: "failed provider object", query: `SELECT record_refund_failed($1, $2, $3, $4)`,
			status: "failed", action: "refund.provider_failed", providerEntry: true,
			wantFailed: true,
		},
		{
			name: "cancelled", query: `SELECT record_refund_cancelled($1, $2, $3, $4)`,
			status: "cancelled", action: "refund.provider_cancelled", providerEntry: true,
		},
		{
			name: "create API rejection", query: `SELECT record_refund_api_rejection($1, $2, $3)`,
			status: "failed", action: "refund.provider_rejected", wantFailed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, actor, requestID := refundRecoveryStaffContext(t, "outcome-"+strings.ReplaceAll(tt.name, " ", "-"))
			returnID := preapprovedRefundRecoveryReturn(t)
			var refundID uuid.UUID
			if err := pool.QueryRow(ctx,
				`SELECT claim_return_refund_execution($1, $2, $3)`,
				returnID, actor, requestID).Scan(&refundID); err != nil {
				t.Fatalf("claim refund execution: %v", err)
			}

			providerRef := "re_outcome_" + uuid.NewString()
			var changed bool
			var err error
			if tt.providerEntry {
				err = pool.QueryRow(ctx, tt.query, refundID, providerRef, actor, requestID).
					Scan(&changed)
			} else {
				err = pool.QueryRow(ctx, tt.query, refundID, actor, requestID).
					Scan(&changed)
			}
			if err != nil {
				t.Fatalf("record provider outcome: %v", err)
			}
			if !changed {
				t.Fatal("first provider outcome was reported as an exact replay")
			}

			var status string
			var gotRef *string
			var succeededAt, failedAt *time.Time
			if err := pool.QueryRow(ctx, `
				SELECT status, provider_ref, succeeded_at, failed_at
				FROM refunds WHERE id = $1`, refundID).
				Scan(&status, &gotRef, &succeededAt, &failedAt); err != nil {
				t.Fatalf("read recorded provider outcome: %v", err)
			}
			if status != tt.status {
				t.Errorf("status = %q, want %q", status, tt.status)
			}
			if tt.providerEntry {
				if gotRef == nil || *gotRef != providerRef {
					t.Errorf("provider ref = %v, want %q", gotRef, providerRef)
				}
			} else if gotRef != nil {
				t.Errorf("explicit API rejection has provider ref %q, want none", *gotRef)
			}
			if (succeededAt != nil) != tt.wantSucceeded {
				t.Errorf("succeeded_at present = %v, want %v", succeededAt != nil, tt.wantSucceeded)
			}
			if (failedAt != nil) != tt.wantFailed {
				t.Errorf("failed_at present = %v, want %v", failedAt != nil, tt.wantFailed)
			}

			var audits int
			if err := pool.QueryRow(ctx, `
				SELECT count(*) FROM audit_events
				WHERE entity_table = 'refunds' AND entity_id = $1 AND action = $2`,
				refundID, tt.action).Scan(&audits); err != nil {
				t.Fatalf("count provider outcome audit: %v", err)
			}
			if audits != 1 {
				t.Errorf("%s audits = %d, want one", tt.action, audits)
			}
		})
	}
}

func TestRefundOutcomeExactReplayPreservesHistoryAndContradictionsFailClosed(t *testing.T) {
	ctx, actor, requestID := refundRecoveryStaffContext(t, "exact-replay")
	returnID := preapprovedRefundRecoveryReturn(t)
	var refundID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT claim_return_refund_execution($1, $2, $3)`,
		returnID, actor, requestID).Scan(&refundID); err != nil {
		t.Fatalf("claim refund execution: %v", err)
	}
	providerRef := "re_exact_" + uuid.NewString()
	var changed bool
	if err := pool.QueryRow(ctx,
		`SELECT record_refund_succeeded($1, $2, $3, $4)`,
		refundID, providerRef, actor, requestID).Scan(&changed); err != nil {
		t.Fatalf("record first success: %v", err)
	}
	if !changed {
		t.Fatal("first success was reported as a replay")
	}

	var firstSucceeded time.Time
	if err := pool.QueryRow(ctx,
		`SELECT succeeded_at FROM refunds WHERE id = $1`, refundID).
		Scan(&firstSucceeded); err != nil {
		t.Fatalf("read first succeeded_at: %v", err)
	}
	replayRequest := requestID + "-retry"
	if err := pool.QueryRow(ctx,
		`SELECT record_refund_succeeded($1, $2, $3, $4)`,
		refundID, providerRef, actor, replayRequest).Scan(&changed); err != nil {
		t.Fatalf("replay exact success: %v", err)
	}
	if changed {
		t.Error("exact replay claimed to change durable state")
	}

	var replaySucceeded time.Time
	var successAudits int
	if err := pool.QueryRow(ctx, `
		SELECT r.succeeded_at,
		       count(a.id) FILTER (WHERE a.action = 'refund.provider_succeeded')
		FROM refunds r
		LEFT JOIN audit_events a ON a.entity_table = 'refunds' AND a.entity_id = r.id
		WHERE r.id = $1
		GROUP BY r.succeeded_at`, refundID).Scan(&replaySucceeded, &successAudits); err != nil {
		t.Fatalf("read replay history: %v", err)
	}
	if !replaySucceeded.Equal(firstSucceeded) || successAudits != 1 {
		t.Errorf("exact replay changed time/audits to %v/%d, want %v/1",
			replaySucceeded, successAudits, firstSucceeded)
	}

	if err := pool.QueryRow(ctx,
		`SELECT record_refund_succeeded($1, $2, $3, $4)`,
		refundID, "re_conflict_"+uuid.NewString(), actor, replayRequest).
		Scan(&changed); err == nil {
		t.Fatal("terminal refund accepted a conflicting provider identity")
	} else if constraint := constraintFrom(err); constraint != "refunds_provider_ref_immutable" {
		t.Errorf("provider identity conflict hit %q, want refunds_provider_ref_immutable: %v",
			constraint, err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT record_refund_pending($1, $2, $3, $4)`,
		refundID, providerRef, actor, replayRequest).Scan(&changed); err == nil {
		t.Fatal("terminal refund regressed to pending")
	} else if constraint := constraintFrom(err); constraint != "refunds_no_regression" {
		t.Errorf("terminal regression hit %q, want refunds_no_regression: %v",
			constraint, err)
	}
}

func refundRecoveryStaffContext(
	t *testing.T, label string,
) (context.Context, uuid.UUID, string) {
	t.Helper()
	ctx, actor := staffContext(t)
	requestID := "refund-" + label + "-" + uuid.NewString()[:8]
	return web.WithRequestID(ctx, requestID), actor, requestID
}

func preapprovedRefundRecoveryReturn(t *testing.T) uuid.UUID {
	t.Helper()
	returnID, _ := returnedOrder(t, 1)
	if _, err := pool.Exec(t.Context(), `
		UPDATE return_requests
		SET status = 'approved', decided_at = now(), resolution = 'refund recovery'
		WHERE id = $1`, returnID); err != nil {
		t.Fatalf("pre-approve return: %v", err)
	}
	return returnID
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
