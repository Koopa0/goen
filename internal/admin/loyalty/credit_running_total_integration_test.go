//go:build integration

package loyalty_test

import (
	"context"
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/loyalty"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/pgtx"
)

func TestLedgerRunningTotalsFollowRecordedOrderWhenTransactionsCommitInReverse(t *testing.T) {
	owner := admintest.Pool(t)
	staff, actor := admintest.StaffContext(t, owner)
	ctx, cancel := context.WithTimeout(staff, 20*time.Second)
	defer cancel()
	customer := admintest.CreditedAccount(t, owner, 0)
	orderID := admintest.OrderForCustomer(t, owner, customer, 1000, false)
	adminPool := admintest.AdminRolePool(t, owner)
	config := owner.Config().Copy()
	config.MaxConns = 2
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SET ROLE store`)
		return err
	}
	checkout, poolErr := pgxpool.NewWithConfig(ctx, config)
	if poolErr != nil {
		t.Fatal(poolErr)
	}
	t.Cleanup(checkout.Close)
	var role string
	if err := checkout.QueryRow(ctx, `SELECT current_user`).Scan(&role); err != nil || role != "store" {
		t.Fatalf("checkout role=%q,%v,want store", role, err)
	}
	earlier, spendBeginErr := checkout.Begin(ctx)
	if spendBeginErr != nil {
		t.Fatal(spendBeginErr)
	}
	defer pgtx.Rollback(ctx, earlier)
	var earlierAt, laterAt time.Time
	var earlierPID, laterPID int
	if err := earlier.QueryRow(ctx, `SELECT now(),pg_backend_pid()`).Scan(&earlierAt, &earlierPID); err != nil {
		t.Fatal(err)
	}
	later, grantBeginErr := adminPool.Begin(ctx)
	if grantBeginErr != nil {
		t.Fatal(grantBeginErr)
	}
	defer pgtx.Rollback(ctx, later)
	if err := later.QueryRow(ctx, `SELECT now(),pg_backend_pid()`).Scan(&laterAt, &laterPID); err != nil {
		t.Fatal(err)
	}
	if !earlierAt.Before(laterAt) {
		t.Fatal("fixture transactions have no distinct recorded-time order")
	}
	if _, err := later.Exec(ctx, `SELECT grant_store_credit($1,1000,'later transaction grant',$2,$3)`, customer, actor, uuid.New()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, writeErr := earlier.Exec(ctx, `SELECT spend_store_credit($1,-700)`, orderID)
		if writeErr == nil {
			writeErr = earlier.Commit(ctx)
		}
		done <- writeErr
		close(done)
	}()
	// Cancel and join before deferred rollbacks touch a transaction used by the writer.
	defer func() { cancel(); <-done }()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	blocked := false
	for !blocked {
		select {
		case writeErr := <-done:
			t.Fatalf("earlier spend finished before the grant committed: %v", writeErr)
		case <-ctx.Done():
			t.Fatal("earlier spend did not reach the later grant's account lock")
		case <-ticker.C:
			if err := owner.QueryRow(ctx, `SELECT $2=ANY(pg_blocking_pids($1))`, earlierPID, laterPID).Scan(&blocked); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := later.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var spendAt time.Time
	if err := owner.QueryRow(ctx, `SELECT created_at FROM store_credit_entries WHERE order_id=$1 AND amount_cents=-700`, orderID).Scan(&spendAt); err != nil {
		t.Fatal(err)
	}
	if !spendAt.Equal(earlierAt) {
		t.Errorf("spend recorded at %v,want earlier transaction %v", spendAt, earlierAt)
	}
	var canonical int64
	if err := adminPool.QueryRow(ctx, `SELECT balance_cents FROM store_credit_balances WHERE user_id=$1`, customer).Scan(&canonical); err != nil {
		t.Fatal(err)
	}
	if canonical != 300 {
		t.Fatalf("actual credit balance=%d,want 300", canonical)
	}
	store := loyalty.NewStore(adminPool)
	desk := http.NewServeMux()
	loyalty.NewHandler(store, slog.New(slog.DiscardHandler)).Routes(desk, admintest.BackOffice)
	for _, locale := range i18n.Locales() {
		readCtx := i18n.WithLocale(ctx, locale)
		view, err := store.CreditForCustomer(readCtx, customer.String())
		if err != nil {
			t.Fatal(err)
		}
		if len(view.Rows) != 2 {
			t.Fatalf("ledger rows=%d,want 2", len(view.Rows))
		}
		if view.Rows[0].AmountCents != 1000 || view.Rows[0].RunningTotalCents != canonical || view.Rows[1].AmountCents != -700 || view.Rows[1].RunningTotalCents != -700 {
			t.Errorf("%s recorded-order totals=%+v,want grant/300 then spend/-700", locale, view.Rows)
		}
		response := httptest.NewRecorder()
		desk.ServeHTTP(response, httptest.NewRequestWithContext(readCtx, http.MethodGet, "/admin/credit?customer="+customer.String(), http.NoBody))
		body := html.UnescapeString(response.Body.String())
		if response.Code != http.StatusOK || !strings.Contains(body, i18n.T(readCtx, i18n.KeyAdminCreditRunningTotal)) || !strings.Contains(body, i18n.T(readCtx, i18n.KeyAdminCreditLedgerOrder)) {
			t.Errorf("%s ledger GET does not explain its accounting-order totals: %d", locale, response.Code)
		}
		for _, cell := range []string{`<td class="goen-admin__cellnum">-NT$7</td>`, `<td class="goen-admin__cellnum">NT$3</td>`} {
			if !strings.Contains(body, cell) {
				t.Errorf("%s ledger GET lacks the actual accounting-total cell %q", locale, cell)
			}
		}
	}
}
