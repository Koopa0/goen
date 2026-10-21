//go:build integration

package health_test

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/health"
	"github.com/koopa0/goen/internal/admin/worklist"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice/issuer"
	"github.com/koopa0/goen/internal/outbox"
)

// TestARefundWithNoAllowanceFiledNeedsAPerson: a return paid back to the card
// or as store credit leaves the order's 統一發票 at the amount before it until a
// 折讓 is filed, and nothing files one for a return. /admin/health lists the
// invoice once the money has been back for UnrelievedAfter, with the allowance
// the order page would file, and the dashboard counts it. A return not yet paid
// back, or paid back within the margin, owes nothing yet; an allowance in
// flight belongs to the stranded-claims list; a settled one relieves it.
func TestARefundWithNoAllowanceFiledNeedsAPerson(t *testing.T) {
	// One database of its own, so the dashboard's count is this test's alone.
	// Each case leaves nothing owed, so the next starts from an empty list.
	owner := dbtest.Pool(t)
	if err := admintest.LoadCatalogue(t.Context(), owner); err != nil {
		t.Fatalf("load isolated catalogue: %v", err)
	}
	// The page's own role: a grant it lacks fails here, not only in production.
	store := health.NewStore(admintest.AdminRolePool(t, owner))
	for _, tt := range []struct {
		name  string
		order func(t *testing.T, p *pgxpool.Pool) (uuid.UUID, string)
		// age moves the money that went back ($1 the order) earlier by $2.
		age string
	}{
		{"card", func(t *testing.T, p *pgxpool.Pool) (uuid.UUID, string) {
			t.Helper()
			return admintest.ReturnedOrder(t, p, 1)
		}, `UPDATE refunds SET created_at = created_at - $2::interval,
		                       succeeded_at = succeeded_at - $2::interval
		    WHERE payment_id IN (SELECT id FROM payments WHERE order_id = $1)`},
		{"store credit", func(t *testing.T, p *pgxpool.Pool) (uuid.UUID, string) {
			t.Helper()
			requestID, number, _ := admintest.CreditFundedReturn(t, p, 1, 200000)
			return requestID, number
		}, `UPDATE store_credit_entries SET created_at = created_at - $2::interval
		    WHERE order_id = $1 AND amount_cents > 0`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := admintest.StaffContext(t, owner)
			fake, err := issuer.NewFakeECPay(time.Now().Add(-2 * time.Hour))
			if err != nil {
				t.Fatalf("fake ECPay: %v", err)
			}
			t.Cleanup(fake.Close)
			invoices := issuer.NewStore(owner, fake.Gateway())

			requestID, number := tt.order(t, owner)
			orderID := issueTheInvoice(t, ctx, owner, invoices, number)
			if listed := unrelieved(t, ctx, store, 0); len(listed) != 0 {
				t.Fatalf("a return not yet paid back lists %v, want nothing owed", listed)
			}

			if err = admintest.ReturnDesk(owner, admintest.Refunder{}).Decide(
				ctx, requestID.String(), "approved", "已收到退貨", ""); err != nil {
				t.Fatalf("approve the return: %v", err)
			}
			if listed := unrelieved(t, ctx, store, health.UnrelievedAfter); len(listed) != 0 {
				t.Errorf("a refund paid back just now lists %v, want it inside the margin", listed)
			}
			want := health.UnrelievedInvoice{OrderNumber: number, Number: invoiceNumber(t, ctx, owner, orderID), AmountCents: 100000}
			if listed := unrelieved(t, ctx, store, 0); len(listed) != 1 || withoutSince(listed[0]) != want {
				t.Fatalf("with no margin the page lists %v, want %+v", listed, want)
			}
			shown := health.UnrelievedRefund{OrderNumber: want.OrderNumber, Number: want.Number, AmountCents: want.AmountCents}

			backdate(t, ctx, owner, tt.age, orderID, time.Hour)
			view, err := store.WorkerHealth(ctx, outbox.NewStore(owner, slog.New(slog.DiscardHandler)))
			if err != nil {
				t.Fatalf("health: %v", err)
			}
			if view.UnrelievedRefundCount != 1 || len(view.UnrelievedRefunds) != 1 || view.RefundsRelieved() {
				t.Fatalf("health lists %d refunds %v, want %+v", view.UnrelievedRefundCount, view.UnrelievedRefunds, shown)
			}
			if got := view.UnrelievedRefunds[0]; got.Since == "" {
				t.Errorf("health shows %+v with no time it became owed", got)
			} else if got.Since = ""; got != shown {
				t.Errorf("health shows %+v, want %+v", got, shown)
			}
			if tasks := taskFor(t, ctx, store); len(tasks) != 1 || tasks[0].Count != 1 ||
				tasks[0].Href != "/admin/health#unrelieved-refunds-heading" || tasks[0].AgeSeconds < 3600 {
				t.Errorf("dashboard tasks %+v, want one, an hour old, linking the table", tasks)
			}

			operationID := uuid.New()
			err = admintest.OrderStore(owner, admintest.Refunder{}, invoices, invoices).FileAllowance(ctx, number, operationID)
			if err != nil && !errors.Is(err, issuer.ErrAwaitingBuyer) {
				t.Fatalf("file the allowance from the order page: %v", err)
			}
			if listed := unrelieved(t, ctx, store, 0); len(listed) != 0 {
				t.Errorf("an allowance waiting for the buyer still lists %v here, want only the claims list", listed)
			}
			fake.BuyerAgrees()
			if _, err := owner.Exec(ctx,
				`UPDATE invoice_operations SET available_at = now() WHERE id = $1`, operationID); err != nil {
				t.Fatalf("make the allowance due: %v", err)
			}
			if err := invoices.ProcessOperation(ctx, operationID); err != nil {
				t.Fatalf("settle the agreed allowance: %v", err)
			}
			var settled int
			if err := owner.QueryRow(ctx, `
				SELECT count(*) FROM invoice_documents
				WHERE order_id = $1 AND kind = 'allowance' AND amount_cents = 100000`, orderID).Scan(&settled); err != nil {
				t.Fatalf("read the allowance: %v", err)
			}
			if settled != 1 {
				t.Fatalf("%d settled allowances of 100000, want one", settled)
			}
			if listed := unrelieved(t, ctx, store, 0); len(listed) != 0 {
				t.Errorf("a relieved refund still lists %v", listed)
			}
			if tasks := taskFor(t, ctx, store); len(tasks) != 0 {
				t.Errorf("dashboard tasks %+v after the allowance settled, want none", tasks)
			}
		})
	}
}

// issueTheInvoice files the order's 統一發票 through the system's claim, as the
// invoice worker does once the money is in.
func issueTheInvoice(
	t *testing.T, ctx context.Context, owner *pgxpool.Pool, invoices *issuer.Store, number string,
) uuid.UUID {
	t.Helper()
	var orderID uuid.UUID
	if err := owner.QueryRow(ctx,
		`SELECT id FROM orders WHERE order_number = $1`, number).Scan(&orderID); err != nil {
		t.Fatalf("read order %s: %v", number, err)
	}
	if _, err := owner.Exec(ctx, `
		INSERT INTO invoice_preferences (order_id, invoice_type, customer_name, customer_email)
		VALUES ($1, 'member_carrier', '買受人', 'buyer@example.com')`, orderID); err != nil {
		t.Fatalf("record invoice preference: %v", err)
	}
	if err := invoices.ClaimDue(ctx, &outbox.InvoiceDue{OrderNumber: number, Trigger: "evt_" + number}); err != nil {
		t.Fatalf("claim the invoice due: %v", err)
	}
	var operationID uuid.UUID
	if err := owner.QueryRow(ctx, `
		SELECT id FROM invoice_operations WHERE order_id = $1 AND kind = 'issue'`, orderID).Scan(&operationID); err != nil {
		t.Fatalf("read the system issue: %v", err)
	}
	if err := invoices.ProcessOperation(ctx, operationID); err != nil {
		t.Fatalf("issue the invoice: %v", err)
	}
	return orderID
}

func invoiceNumber(t *testing.T, ctx context.Context, owner *pgxpool.Pool, orderID uuid.UUID) string {
	t.Helper()
	var number string
	if err := owner.QueryRow(ctx, `
		SELECT number FROM invoice_documents
		WHERE order_id = $1 AND kind = 'invoice' AND status = 'issued'`, orderID).Scan(&number); err != nil {
		t.Fatalf("read the live invoice: %v", err)
	}
	return number
}

// backdate runs one of the cases' age statements. Triggers are off only
// because a settled refund and a ledger entry are history, which a test cannot
// otherwise make older.
func backdate(t *testing.T, ctx context.Context, owner *pgxpool.Pool, stmt string, orderID uuid.UUID, age time.Duration) {
	t.Helper()
	tx, err := owner.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer db.Rollback(ctx, tx)
	if _, err = tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
		t.Fatalf("disable triggers: %v", err)
	}
	tag, err := tx.Exec(ctx, stmt, orderID, age.String())
	if err != nil || tag.RowsAffected() == 0 {
		t.Fatalf("backdate: %d rows, %v", tag.RowsAffected(), err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func unrelieved(t *testing.T, ctx context.Context, store *health.Store, olderThan time.Duration) []health.UnrelievedInvoice {
	t.Helper()
	listed, total, err := store.UnrelievedRefunds(ctx, olderThan)
	if err != nil {
		t.Fatalf("unrelieved refunds: %v", err)
	}
	if total != int64(len(listed)) {
		t.Errorf("total %d for %d rows", total, len(listed))
	}
	return listed
}

func withoutSince(r health.UnrelievedInvoice) health.UnrelievedInvoice {
	r.Since = time.Time{}
	return r
}

// taskFor is the dashboard's entry for this row, read through Tasks as the
// dashboard reads it.
func taskFor(t *testing.T, ctx context.Context, store *health.Store) []worklist.Task {
	t.Helper()
	tasks, err := store.Tasks(ctx)
	if err != nil {
		t.Fatalf("tasks: %v", err)
	}
	return slices.DeleteFunc(tasks, func(task worklist.Task) bool {
		return task.Label != i18n.KeyAdminHPUnrelievedRefundsHeading
	})
}
