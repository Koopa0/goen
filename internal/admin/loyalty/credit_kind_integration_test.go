//go:build integration

package loyalty_test

import (
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/loyalty"
	"github.com/koopa0/goen/internal/i18n"
)

var (
	ledgerRow  = regexp.MustCompile(`(?s)<li class="goen-admin__entry">(.*?)</li>`)
	ledgerMeta = regexp.MustCompile(`<span class="goen-admin__meta">([^<]*)</span>`)
	ledgerWho  = regexp.MustCompile(`<span class="goen-admin__entrywho">([^<]*)</span>`)
)

// TestTheCreditHistoryLabelsAPostingByItsKindAndShowsAGrantsOwnWords posts one
// of every kind through the doors that write them, plus staff grants whose
// reasons spell the words the database once wrote for itself. The history must
// call the system postings by their labels in both languages, and the grants by
// what staff typed.
func TestTheCreditHistoryLabelsAPostingByItsKindAndShowsAGrantsOwnWords(t *testing.T) {
	ctx, staffID := admintest.StaffContext(t, pool)
	s := loyalty.NewStore(pool)

	// A credit-funded return: a grant, the order spend, then the return payout.
	requestID, _, accountID := admintest.CreditFundedReturn(t, pool, 2, 200000)
	var customerID uuid.UUID
	var email string
	if err := pool.QueryRow(ctx, `
		SELECT u.id, u.email FROM store_credit_accounts a JOIN users u ON u.id = a.user_id
		WHERE a.id = $1`, accountID).Scan(&customerID, &email); err != nil {
		t.Fatalf("read the customer: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE return_requests SET status = 'approved', decided_at = now(), resolution = '同意退貨'
		WHERE id = $1`, requestID); err != nil {
		t.Fatalf("approve the return: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT compensate_return_with_credit($1, 200000, $2)`, requestID, staffID); err != nil {
		t.Fatalf("pay the return out as credit: %v", err)
	}

	// Staff grants worded like every system posting.
	grants := []string{"points", "訂單折抵", "order cancelled", "退貨退回購物金", "客訴補償"}
	for _, reason := range grants {
		if _, err := s.GrantCredit(ctx, customerID, 1000, reason, uuid.New()); err != nil {
			t.Fatalf("grant %q: %v", reason, err)
		}
	}

	// A points redemption: the points account is the credit account.
	if _, err := pool.Exec(ctx, `
		INSERT INTO loyalty_entries (account_id, kind, points, reason, idempotency_key, expires_on)
		VALUES ($1, 'award', 500, 'seed', 'kind-history:' || $2::text, shop_today() + 365)`, accountID, uuid.NewString()); err != nil {
		t.Fatalf("seed an award lot: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT redeem_loyalty_points($1, 500, $2)`, customerID, uuid.New()); err != nil {
		t.Fatalf("redeem points: %v", err)
	}

	// An order spend on a second order, then its cancellation and reversal.
	open := admintest.OrderForCustomer(t, pool, customerID, 5000, false)
	if _, err := pool.Exec(ctx, `SELECT spend_store_credit($1, -3000)`, open); err != nil {
		t.Fatalf("spend credit at checkout: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now() WHERE id = $1`, open); err != nil {
		t.Fatalf("cancel the order: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT reverse_order_credit($1)`, open); err != nil {
		t.Fatalf("reverse the spend: %v", err)
	}

	h := loyalty.NewHandler(s, slog.New(slog.DiscardHandler))
	read := func(locale i18n.Locale) []string {
		t.Helper()
		req := httptest.NewRequestWithContext(i18n.WithLocale(ctx, locale), http.MethodGet, "/admin/credit", nil)
		w := httptest.NewRecorder()
		admintest.BackOffice.RequireStaff(h.Credit)(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET /admin/credit answered %d, want 200", w.Code)
		}
		var labels []string
		for _, row := range ledgerRow.FindAllStringSubmatch(w.Body.String(), -1) {
			if who := ledgerWho.FindStringSubmatch(row[1]); len(who) != 2 || html.UnescapeString(who[1]) != email {
				continue
			}
			meta := ledgerMeta.FindAllStringSubmatch(row[1], -1)
			if len(meta) != 2 {
				t.Fatalf("a ledger row has %d meta spans, want a label and a time: %s", len(meta), row[1])
			}
			labels = append(labels, html.UnescapeString(strings.TrimSpace(meta[0][1])))
		}
		slices.Sort(labels)
		return labels
	}

	for _, tt := range []struct {
		locale i18n.Locale
		want   []string
	}{
		{i18n.En, []string{
			"Applied to an order", "Applied to an order", "Credit for a return", "Order cancelled, credit returned", "Points redeemed",
		}},
		{i18n.ZhHant, []string{
			"訂單折抵", "訂單折抵", "退貨退回購物金", "訂單取消，購物金退回", "點數兌換",
		}},
	} {
		want := append(slices.Clone(tt.want), grants...)
		want = append(want, "測試發放") // the fixture's own grant
		slices.Sort(want)
		if got := read(tt.locale); !slices.Equal(got, want) {
			t.Errorf("%s history reads\n  %q\nwant\n  %q", tt.locale, got, want)
		}
	}
}
