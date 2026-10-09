//go:build integration

package loyalty_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/loyalty"
	"github.com/koopa0/goen/internal/i18n"
)

func TestCreditDeskFiltersCustomerAndKeepsBalanceAcrossPages(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	appPool := admintest.AdminRolePool(t, pool)
	s := loyalty.NewStore(appPool)
	var customerID uuid.UUID
	address := "history-" + uuid.NewString() + "@example.test"
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, role) VALUES ($1, 'customer') RETURNING id`, address).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	for range 51 {
		if _, err := s.GrantCredit(ctx, customerID, 100, "history grant", uuid.New()); err != nil {
			t.Fatal(err)
		}
	}
	var otherID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, role) VALUES ($1, 'customer') RETURNING id`, "other-"+uuid.NewString()+"@example.test").Scan(&otherID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GrantCredit(ctx, otherID, 90000, "other customer's grant", uuid.New()); err != nil {
		t.Fatal(err)
	}
	all, err := s.Credit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var foundOther bool
	for _, row := range all.Rows {
		if row.CustomerID == otherID.String() {
			foundOther = true
			if row.BalanceCents != 90000 {
				t.Errorf("unfiltered ledger mixed customer balances: got %d, want 90000", row.BalanceCents)
			}
		}
	}
	if !foundOther {
		t.Fatal("unfiltered ledger omitted the newest customer's grant")
	}
	view, err := s.CreditForCustomer(ctx, customerID.String())
	if err != nil || len(view.Rows) != 50 || view.Next == "" {
		t.Fatalf("first customer page: %d rows, next=%q, err=%v", len(view.Rows), view.Next, err)
	}
	var currentBalance int64
	if err := appPool.QueryRow(ctx, `SELECT balance_cents FROM store_credit_balances WHERE user_id = $1`, customerID).Scan(&currentBalance); err != nil {
		t.Fatal(err)
	}
	if view.Rows[0].BalanceCents != currentBalance {
		t.Errorf("newest ledger balance = %d, want canonical balance %d", view.Rows[0].BalanceCents, currentBalance)
	}
	for i, row := range view.Rows {
		if row.CustomerID != customerID.String() || row.BalanceCents != int64(51-i)*100 || row.ActorName == "" {
			t.Errorf("row %d is not this customer's cumulative balance and grant actor: %+v", i, row)
		}
	}
	h := loyalty.NewHandler(s, slog.New(slog.DiscardHandler))
	get := func(target string, locale i18n.Locale) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(i18n.WithLocale(ctx, locale), http.MethodGet, target, nil)
		res := httptest.NewRecorder()
		admintest.BackOffice.RequireStaff(h.Credit)(res, req)
		return res
	}
	for _, locale := range i18n.Locales() {
		res := get("/admin/credit?customer="+customerID.String(), locale)
		if res.Code != http.StatusOK {
			t.Fatalf("filtered desk: %d %s", res.Code, res.Body.String())
		}
		body := res.Body.String()
		if !strings.Contains(body, `value="`+address+`"`) || !strings.Contains(body, `href="/admin/customers/`+customerID.String()+`"`) || !strings.Contains(body, "NT$51") {
			t.Errorf("%s filtered desk lacks recipient, profile link or full running balance", locale.Tag())
		}
		second := get(view.Next, locale)
		if second.Code != http.StatusOK || !strings.Contains(second.Body.String(), `>NT$1</td>`) || strings.Contains(second.Body.String(), "other customer's grant") {
			t.Errorf("%s second page lost customer or oldest balance: %d", locale.Tag(), second.Code)
		}
	}
	for _, customer := range []string{"invalid", uuid.NewString()} {
		if res := get("/admin/credit?customer="+customer, i18n.En); res.Code != http.StatusNotFound {
			t.Errorf("unknown customer filter = %d, want 404", res.Code)
		}
	}
}
