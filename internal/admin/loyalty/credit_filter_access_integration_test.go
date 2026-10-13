//go:build integration

package loyalty_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/customers"
	"github.com/koopa0/goen/internal/admin/loyalty"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/user"
)

func TestCreditFilterKeepsActorProtectionAndPromotedCustomerHistory(t *testing.T) {
	owner := admintest.Pool(t)
	staff, administrator := admintest.StaffContext(t, owner)
	reader := admintest.AdminRolePool(t, owner)
	store := loyalty.NewStore(reader)
	desk := http.NewServeMux()
	logger := slog.New(slog.DiscardHandler)
	loyalty.NewHandler(store, logger).Routes(desk, admintest.BackOffice)
	customers.NewHandler(customers.NewStore(reader), logger).Routes(desk, admintest.BackOffice)
	type profile struct {
		id    uuid.UUID
		email string
		role  user.Role
	}
	targets := []profile{{role: user.RoleCustomer}, {role: user.RoleStaff}, {role: user.RoleAdmin}}
	const reason = "History retained after promotion"
	for i := range targets {
		target := &targets[i]
		target.email = "credit-history-" + uuid.NewString() + "@example.com"
		if err := owner.QueryRow(t.Context(), `INSERT INTO users(email,role,full_name) VALUES($1,'customer','Credit history customer') RETURNING id`, target.email).Scan(&target.id); err != nil {
			t.Fatal(err)
		}
		if _, err := store.GrantCredit(staff, target.id, 100, reason, uuid.New()); err != nil {
			t.Fatal(err)
		}
		if _, err := owner.Exec(t.Context(), `UPDATE users SET role=$2 WHERE id=$1`, target.id, string(target.role)); err != nil {
			t.Fatal(err)
		}
	}
	var entries int
	if err := owner.QueryRow(t.Context(), `SELECT count(*) FROM store_credit_entries`).Scan(&entries); err != nil {
		t.Fatal(err)
	}
	audits := admintest.AuditRows(t, owner, audit.ActionGrantCredit)
	get := func(ctx context.Context, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, path, http.NoBody)
		response := httptest.NewRecorder()
		desk.ServeHTTP(response, req)
		return response
	}
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(staff, locale)
		for _, selector := range []string{"invalid", uuid.Nil.String(), uuid.NewString()} {
			response := get(ctx, "/admin/credit?customer="+selector)
			if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), reason) {
				t.Errorf("%s invalid filter %s = %d, want404 without ledger", locale, selector, response.Code)
			}
		}
		for _, actor := range []context.Context{t.Context(), user.NewContext(t.Context(), user.User{ID: targets[0].id.String(), Role: user.RoleCustomer})} {
			for _, target := range []string{"/admin/credit", "/admin/credit?customer=" + targets[1].id.String()} {
				response := get(i18n.WithLocale(actor, locale), target)
				if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), reason) {
					t.Errorf("%s nonstaff %s = %d, want404 without ledger", locale, target, response.Code)
				}
			}
		}
		response := get(ctx, "/admin/credit?customer="+administrator.String())
		if response.Code != http.StatusOK {
			t.Errorf("existing uncredited profile filter = %d, want200", response.Code)
		}
		for _, target := range targets {
			id := target.id.String()
			response := get(ctx, "/admin/credit?customer="+id)
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), reason) {
				t.Errorf("%s %s profile ledger = %d, want200 with existing history", locale, target.role, response.Code)
			}
			response = get(ctx, "/admin/customers/"+id)
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `href="/admin/credit?customer=`+id+`"`) {
				t.Errorf("%s %s profile = %d without matching credit link", locale, target.role, response.Code)
			}
			operation := uuid.NewString()
			form := url.Values{"email": {target.email}, "amount": {"0"}, "reason": {"Retain refused draft"}, "operation_id": {operation}, "filter_customer": {id}}
			request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/credit", strings.NewReader(form.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			refused := httptest.NewRecorder()
			desk.ServeHTTP(refused, request)
			if refused.Code != http.StatusUnprocessableEntity {
				t.Errorf("%s %s invalid grant = %d, want422", locale, target.role, refused.Code)
			}
			for _, field := range []struct{ input, want string }{{"credit-email", target.email}, {"credit-amount", "0"}, {"credit-reason", "Retain refused draft"}} {
				input := admintest.InputElementByID(t, refused.Body.String(), field.input)
				if got := admintest.InputAttribute(t, input, "value"); got != field.want {
					t.Errorf("%s draft %s = %q, want%q", locale, field.input, got, field.want)
				}
			}
			for _, want := range []string{`name="filter_customer" value="` + id + `"`, operation, reason} {
				if !strings.Contains(refused.Body.String(), want) {
					t.Errorf("%s refusal lost %q", locale, want)
				}
			}
		}
	}
	var after int
	if err := owner.QueryRow(t.Context(), `SELECT count(*) FROM store_credit_entries`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != entries || admintest.AuditRows(t, owner, audit.ActionGrantCredit) != audits {
		t.Error("invalid grants wrote ledger entries or audit records")
	}
}
