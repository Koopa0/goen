//go:build integration

package invoice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func cancelInvoiceOrder(t *testing.T, number, role string) error {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE `+pgx.Identifier{role}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now() WHERE order_number = $1`, number); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func TestCancellationRequiresResolvedInvoiceAmounts(t *testing.T) {
	for _, role := range []string{"store", "admin"} {
		for _, tc := range []struct {
			name                                string
			allowance                           int64
			voidInvoice, voidAllowance, allowed bool
		}{
			{name: "live invoice"},
			{name: "partial allowance", allowance: 50000},
			{name: "full allowance", allowance: 100000, allowed: true},
			{name: "voided allowance", allowance: 100000, voidAllowance: true},
			{name: "voided invoice", voidInvoice: true, allowed: true},
		} {
			t.Run(role+"/"+tc.name, func(t *testing.T) {
				number := invoicedOrderWithRefund(t, 100000)
				if tc.allowance > 0 {
					_, err := pool.Exec(t.Context(), `INSERT INTO invoice_documents (order_id, kind, original_id, number, amount_cents) SELECT d.order_id, 'allowance', d.id, $2, $3 FROM invoice_documents d JOIN orders o ON o.id = d.order_id WHERE o.order_number = $1 AND d.kind = 'invoice'`, number, uuid.NewString()[:32], tc.allowance)
					if err != nil {
						t.Fatal(err)
					}
				}
				if tc.voidInvoice || tc.voidAllowance {
					kind := "invoice"
					if tc.voidAllowance {
						kind = "allowance"
					}
					_, err := pool.Exec(t.Context(), `UPDATE invoice_documents SET status = 'voided', voided_at = now() WHERE order_id = (SELECT id FROM orders WHERE order_number = $1) AND kind = $2`, number, kind)
					if err != nil {
						t.Fatal(err)
					}
				}
				err := cancelInvoiceOrder(t, number, role)
				if tc.allowed {
					if err != nil {
						t.Fatalf("resolved cancellation: %v", err)
					}
				} else if constraintOf(err) != "orders_cancel_invoice_resolved" {
					t.Fatalf("unresolved cancellation: %v", err)
				}
			})
		}
	}
}

func TestUnresolvedInvoiceOperationsBlockCancellationEvenAfterFullAllowance(t *testing.T) {
	for _, kind := range []string{"issue", "void", "allowance"} {
		for _, status := range []string{"pending", "attention", "rejected"} {
			t.Run(kind+"/"+status, func(t *testing.T) {
				number := invoicedOrderWithRefund(t, 100000)
				_, err := pool.Exec(t.Context(), `INSERT INTO invoice_documents (order_id, kind, original_id, number, amount_cents) SELECT d.order_id, 'allowance', d.id, $2, d.amount_cents FROM invoice_documents d JOIN orders o ON o.id = d.order_id WHERE o.order_number = $1 AND d.kind = 'invoice'`, number, uuid.NewString()[:32])
				if err != nil {
					t.Fatal(err)
				}
				_, err = pool.Exec(t.Context(), `INSERT INTO invoice_operations (order_id, kind, target_document_id, provider_key, amount_cents, request_payload, actor_user_id, actor_id_snapshot, request_id, status) SELECT d.order_id, $2, CASE WHEN $2 = 'issue' THEN NULL ELSE d.id END, replace(o.order_number, '-', ''), d.amount_cents, '{}', $3, $3, $4, $5 FROM invoice_documents d JOIN orders o ON o.id = d.order_id WHERE o.order_number = $1 AND d.kind = 'invoice'`, number, kind, filingActor, uuid.NewString(), status)
				if err != nil {
					t.Fatal(err)
				}
				err = cancelInvoiceOrder(t, number, "store")
				if status == "rejected" {
					if err != nil {
						t.Fatalf("rejected operation blocked full allowance: %v", err)
					}
				} else if constraintOf(err) != "orders_cancel_invoice_resolved" {
					t.Fatalf("unresolved %s %s cancelled: %v", kind, status, err)
				}
			})
		}
	}
}

func TestCancellationInvoiceGuardExposesNoExecutionAuthority(t *testing.T) {
	for _, role := range []string{"store", "admin", "reporting"} {
		var allowed bool
		if err := pool.QueryRow(t.Context(), `SELECT has_function_privilege($1, 'orders_check_invoice_cancellation()', 'EXECUTE')`, role).Scan(&allowed); err != nil {
			t.Fatal(err)
		}
		if allowed {
			t.Fatalf("%s can execute private cancellation guard", role)
		}
	}
	var publicExec bool
	if err := pool.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM pg_proc p CROSS JOIN LATERAL aclexplode(coalesce(p.proacl, acldefault('f', p.proowner))) a WHERE p.oid = 'orders_check_invoice_cancellation()'::regprocedure AND a.grantee = 0 AND a.privilege_type = 'EXECUTE')`).Scan(&publicExec); err != nil {
		t.Fatal(err)
	}
	if publicExec {
		t.Fatal("PUBLIC can execute cancellation guard")
	}
	number := invoicedOrderWithRefund(t, 100000)
	err := cancelInvoiceOrder(t, number, "store")
	if constraintOf(err) != "orders_cancel_invoice_resolved" {
		t.Fatalf("expected guarded refusal: %v", err)
	}
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.Code != "23514" || pgErr.Detail != "" || pgErr.TableName != "" || pgErr.ColumnName != "" {
		t.Fatalf("refusal exposes invoice details: %+v", pgErr)
	}
}

func TestInvoiceClaimAndCancellationSerialize(t *testing.T) {
	for _, claimFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("claim-first-%t", claimFirst), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			number := orderToInvoice(t, 100000, 0, 0)
			first, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = first.Rollback(context.WithoutCancel(ctx)) }()
			if _, roleErr := first.Exec(ctx, `SET LOCAL ROLE admin`); roleErr != nil {
				t.Fatal(roleErr)
			}
			var firstPID int32
			if queryErr := first.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&firstPID); queryErr != nil {
				t.Fatal(queryErr)
			}
			if claimFirst {
				_, err = first.Exec(ctx, `SELECT claim_invoice_issue($1, $2, $3)`, number, filingActor, uuid.NewString())
			} else {
				_, err = first.Exec(ctx, `UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now() WHERE order_number = $1`, number)
			}
			if err != nil {
				t.Fatal(err)
			}
			second, err := pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer second.Release()
			if _, err := second.Exec(ctx, `SET ROLE admin`); err != nil {
				t.Fatal(err)
			}
			defer func() { _, _ = second.Exec(context.WithoutCancel(ctx), `RESET ROLE`) }()
			var secondPID int32
			if err := second.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&secondPID); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				if claimFirst {
					_, err := second.Exec(ctx, `UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now() WHERE order_number = $1`, number)
					done <- err
				} else {
					_, err := second.Exec(ctx, `SELECT claim_invoice_issue($1, $2, $3)`, number, filingActor, uuid.NewString())
					done <- err
				}
			}()
			finished := false
			defer func() {
				_ = first.Rollback(context.WithoutCancel(ctx))
				cancel()
				if !finished {
					<-done
				}
			}()
			waitInvoiceWriterBlocked(t, ctx, firstPID, secondPID)
			if err := first.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			want := "invoice_issue_not_cancelled"
			if claimFirst {
				want = "orders_cancel_invoice_resolved"
			}
			result := <-done
			finished = true
			if err := result; constraintOf(err) != want {
				t.Fatalf("second writer = %v, want %s", err, want)
			}
		})
	}
}

func waitInvoiceWriterBlocked(t *testing.T, ctx context.Context, firstPID, secondPID int32) {
	t.Helper()
	for {
		var blocked bool
		if err := pool.QueryRow(ctx, `SELECT $1::integer = ANY(pg_blocking_pids($2))`, firstPID, secondPID).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("invoice writer did not wait on the order lock")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestCancelledOrderCannotReceiveAnInvoice(t *testing.T) {
	number := orderToInvoice(t, 100000, 0, 0)
	if err := cancelInvoiceOrder(t, number, "store"); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(t.Context(), `INSERT INTO invoice_documents (order_id, kind, number, amount_cents) SELECT id, 'invoice', $2, 100000 FROM orders WHERE order_number = $1`, number, uuid.NewString()[:32])
	if constraintOf(err) != "invoice_issue_not_cancelled" {
		t.Fatalf("direct cancelled invoice insert = %v", err)
	}
}

func TestSettledIssueStillBlocksCancellation(t *testing.T) {
	number := orderToInvoice(t, 100000, 0, 0)
	owner := uuid.New()
	var operation uuid.UUID
	if err := pool.QueryRow(t.Context(), `SELECT claim_invoice_issue($1, $2, $3)`, number, filingActor, uuid.NewString()).Scan(&operation); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `SELECT lease_invoice_operation($1, $2, interval '2 minutes')`, operation, owner); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := pool.QueryRow(t.Context(), `SELECT request_payload FROM invoice_operations WHERE id = $1`, operation).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var frozen frozenRequest
	if err := json.Unmarshal(raw, &frozen); err != nil {
		t.Fatal(err)
	}
	descriptions, quantities, prices, amounts := invoiceLineArrays(frozen.Lines)
	_, err := pool.Exec(t.Context(), `SELECT settle_invoice_issue($1,$2,'JC00000103','1234',now(),$3,$4,$5,$6)`, operation, owner, descriptions, quantities, prices, amounts)
	if err != nil {
		t.Fatal(err)
	}
	if err := cancelInvoiceOrder(t, number, "admin"); constraintOf(err) != "orders_cancel_invoice_resolved" {
		t.Fatalf("settled invoice cancellation: %v", err)
	}
}

func TestInvoiceSettlementLocksOrderBeforeOperation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	number := orderToInvoice(t, 100000, 0, 0)
	owner := uuid.New()
	var operation uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT claim_invoice_issue($1, $2, $3)`, number, filingActor, uuid.NewString()).Scan(&operation); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `SELECT lease_invoice_operation($1, $2, interval '2 minutes')`, operation, owner); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := pool.QueryRow(ctx, `SELECT request_payload FROM invoice_operations WHERE id = $1`, operation).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var frozen frozenRequest
	if err := json.Unmarshal(raw, &frozen); err != nil {
		t.Fatal(err)
	}
	descriptions, quantities, prices, amounts := invoiceLineArrays(frozen.Lines)
	first, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Rollback(context.WithoutCancel(ctx)) }()
	var firstPID int32
	if queryErr := first.QueryRow(ctx, `SELECT pg_backend_pid() FROM orders WHERE order_number = $1 FOR UPDATE`, number).Scan(&firstPID); queryErr != nil {
		t.Fatal(queryErr)
	}
	second, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Release()
	if _, roleErr := second.Exec(ctx, `SET ROLE admin`); roleErr != nil {
		t.Fatal(roleErr)
	}
	defer func() { _, _ = second.Exec(context.WithoutCancel(ctx), `RESET ROLE`) }()
	var secondPID int32
	if queryErr := second.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&secondPID); queryErr != nil {
		t.Fatal(queryErr)
	}
	done := make(chan error, 1)
	go func() {
		_, settleErr := second.Exec(ctx, `SELECT settle_invoice_issue($1,$2,'JC00000104','1234',now(),$3,$4,$5,$6)`, operation, owner, descriptions, quantities, prices, amounts)
		done <- settleErr
	}()
	finished := false
	defer func() {
		_ = first.Rollback(context.WithoutCancel(ctx))
		cancel()
		if !finished {
			<-done
		}
	}()
	waitInvoiceWriterBlocked(t, ctx, firstPID, secondPID)
	// If settlement locked its operation first, cancellation/claim lock ordering
	// could form a cycle. A third transaction must still acquire that row now.
	probe, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = probe.Rollback(context.WithoutCancel(ctx)) }()
	if _, probeErr := probe.Exec(ctx, `SELECT id FROM invoice_operations WHERE id = $1 FOR UPDATE NOWAIT`, operation); probeErr != nil {
		t.Fatalf("settlement took operation before order: %v", probeErr)
	}
	if rollbackErr := probe.Rollback(ctx); rollbackErr != nil {
		t.Fatal(rollbackErr)
	}
	if _, err := first.Exec(ctx, `SAVEPOINT cancellation`); err != nil {
		t.Fatal(err)
	}
	_, err = first.Exec(ctx, `UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now() WHERE order_number = $1`, number)
	if constraintOf(err) != "orders_cancel_invoice_resolved" {
		t.Fatalf("in-flight settlement allowed cancellation: %v", err)
	}
	if _, err := first.Exec(ctx, `ROLLBACK TO SAVEPOINT cancellation`); err != nil {
		t.Fatal(err)
	}
	if err := first.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	result := <-done
	finished = true
	if result != nil {
		t.Fatalf("settlement after cancellation refusal: %v", result)
	}
}
