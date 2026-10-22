//go:build integration

package credits_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/credits"
	"github.com/koopa0/goen/internal/i18n"
)

// TestStore_CreditOneCustomer reads one customer's history through the
// admin role: the running total of each posting follows that customer's own
// ledger, a second page carries on from the first, another customer's postings
// never appear, and the newest total is the balance store_credit_balances holds.
func TestStore_CreditOneCustomer(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	reader := admintest.AdminRolePool(t, pool)
	s := credits.NewStore(reader)

	address := "history-" + uuid.NewString() + "@example.test"
	var customerID, otherID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, role) VALUES ($1, 'customer') RETURNING id`, address).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, role) VALUES ($1, 'customer') RETURNING id`, "other-"+uuid.NewString()+"@example.test").Scan(&otherID); err != nil {
		t.Fatal(err)
	}
	for range 51 {
		if _, err := s.GrantCredit(ctx, customerID, 100, "history grant", uuid.New()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.GrantCredit(ctx, otherID, 90000, "other customer's grant", uuid.New()); err != nil {
		t.Fatal(err)
	}

	// The page for every customer has no running total, and carries the link
	// data each row needs.
	all, err := s.Credit(ctx, uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}
	if all.Customer != nil {
		t.Errorf("the page for every customer is narrowed to %+v", all.Customer)
	}
	var sawOther bool
	for _, p := range all.Postings {
		if p.RunningCents != 0 {
			t.Errorf("a posting on the page for every customer carries a running total: %+v", p)
		}
		if p.CustomerID == otherID.String() {
			sawOther = true
		}
	}
	if !sawOther {
		t.Fatal("the page for every customer omits the newest customer's grant")
	}

	page, err := s.Credit(ctx, uuid.NullUUID{UUID: customerID, Valid: true})
	if err != nil || len(page.Postings) != 50 || page.Bound.Next == "" {
		t.Fatalf("first page: %d postings, next=%q, err=%v", len(page.Postings), page.Bound.Next, err)
	}
	if page.Customer == nil || page.Customer.ID != customerID || page.Customer.Email != address {
		t.Errorf("page names customer %+v, want %s", page.Customer, address)
	}
	var balance int64
	if err := reader.QueryRow(ctx, `SELECT balance_cents FROM store_credit_balances WHERE user_id = $1`, customerID).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if page.Postings[0].RunningCents != balance {
		t.Errorf("newest running total = %d, want the balance %d", page.Postings[0].RunningCents, balance)
	}
	for i, p := range page.Postings {
		if p.CustomerID != customerID.String() || p.RunningCents != int64(51-i)*100 || p.ActorEmail == "" {
			t.Errorf("posting %d is not this customer's cumulative total with its granting staff: %+v", i, p)
		}
	}

	h := credits.NewHandler(s, slog.New(slog.DiscardHandler))
	get := func(target string, locale i18n.Locale) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(i18n.WithLocale(ctx, locale), http.MethodGet, target, http.NoBody)
		res := httptest.NewRecorder()
		admintest.BackOffice.RequireStaff(h.Credit)(res, req)
		return res
	}
	for _, locale := range i18n.Locales() {
		first := get("/admin/credits?customer="+customerID.String(), locale)
		body := first.Body.String()
		if first.Code != http.StatusOK {
			t.Fatalf("%s filtered desk = %d", locale.Tag(), first.Code)
		}
		for name, want := range map[string]string{
			"grant form addressed to the customer": `value="` + address + `"`,
			"link back to the customer":            `href="/admin/customers/` + customerID.String() + `"`,
			"the newest running total":             `>NT$51</td>`,
			"the running total column":             i18n.T(i18n.WithLocale(ctx, locale), i18n.KeyAdminCreditRunningTotal),
			"the filter carried into the form":     `name="filter_customer" value="` + customerID.String() + `"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s filtered desk lacks %s %q", locale.Tag(), name, want)
			}
		}
		second := get(page.Bound.Next, locale)
		if second.Code != http.StatusOK || !strings.Contains(second.Body.String(), `>NT$1</td>`) || strings.Contains(second.Body.String(), "other customer's grant") {
			t.Errorf("%s second page lost the customer or the oldest total: %d", locale.Tag(), second.Code)
		}

		whole := get("/admin/credits", locale).Body.String()
		if strings.Contains(whole, i18n.T(i18n.WithLocale(ctx, locale), i18n.KeyAdminCreditRunningTotal)) {
			t.Errorf("%s desk for every customer shows the running total column", locale.Tag())
		}
		if !strings.Contains(whole, `href="/admin/customers/`+otherID.String()+`"`) {
			t.Errorf("%s desk for every customer does not link a row's customer", locale.Tag())
		}
	}
}
