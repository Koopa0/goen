//go:build integration

package admin_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/admin/admintest"
)

func TestConcurrentTerminalRefundRetriesShareOneSuccessor(t *testing.T) {
	ctxA, actorA, requestA := admintest.RefundRecoveryStaffContext(t, pool, "terminal-race-a")
	ctxB, actorB, requestB := admintest.RefundRecoveryStaffContext(t, pool, "terminal-race-b")
	returnID := admintest.PreapprovedReturn(t, pool)

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
	ctxA, actorA, requestA := admintest.RefundRecoveryStaffContext(t, pool, "outcome-lock-a")
	ctxB, actorB, requestB := admintest.RefundRecoveryStaffContext(t, pool, "outcome-lock-b")
	returnID := admintest.PreapprovedReturn(t, pool)
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
		if constraint := admintest.ConstraintName(result.err); constraint != "refunds_execution_settled" {
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
			ctx, actor, requestID := admintest.RefundRecoveryStaffContext(t, pool, "outcome-"+strings.ReplaceAll(tt.name, " ", "-"))
			returnID := admintest.PreapprovedReturn(t, pool)
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
	ctx, actor, requestID := admintest.RefundRecoveryStaffContext(t, pool, "exact-replay")
	returnID := admintest.PreapprovedReturn(t, pool)
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
	} else if constraint := admintest.ConstraintName(err); constraint != "refunds_provider_ref_immutable" {
		t.Errorf("provider identity conflict hit %q, want refunds_provider_ref_immutable: %v",
			constraint, err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT record_refund_pending($1, $2, $3, $4)`,
		refundID, providerRef, actor, replayRequest).Scan(&changed); err == nil {
		t.Fatal("terminal refund regressed to pending")
	} else if constraint := admintest.ConstraintName(err); constraint != "refunds_no_regression" {
		t.Errorf("terminal regression hit %q, want refunds_no_regression: %v",
			constraint, err)
	}
}
