//go:build integration

package credits_test

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
	"github.com/koopa0/goen/internal/admin/credits"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/uitest"
	"github.com/koopa0/goen/internal/user"
)

// TestHandler_CreditFilterAccess holds who may read a customer's history and
// what a refused grant keeps. A customer promoted to staff or admin keeps the
// history they earned, as the grant form's recipient lookup already allows; a signed-out visitor and a customer get no ledger; an unknown
// filter is a 404; and a refused grant re-renders the filter and the draft
// without a ledger or audit write.
func TestHandler_CreditFilterAccess(t *testing.T) {
	owner := admintest.Pool(t)
	staff, administrator := admintest.StaffContext(t, owner)
	reader := admintest.AdminRolePool(t, owner)
	store := credits.NewStore(reader)
	desk := http.NewServeMux()
	logger := slog.New(slog.DiscardHandler)
	credits.NewHandler(store, logger).Routes(desk, admintest.BackOffice)

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
		if err := store.GrantCredit(staff, target.id, 100, reason, uuid.New()); err != nil {
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
			response := get(ctx, "/admin/credits?customer="+selector)
			if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), reason) {
				t.Errorf("%s filter %s = %d, want 404 without a ledger", locale.Tag(), selector, response.Code)
			}
		}
		customer := user.NewContext(t.Context(), user.User{ID: targets[0].id.String(), Role: user.RoleCustomer})
		for _, actor := range []context.Context{t.Context(), customer} {
			for _, target := range []string{"/admin/credits", "/admin/credits?customer=" + targets[1].id.String()} {
				response := get(i18n.WithLocale(actor, locale), target)
				if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), reason) {
					t.Errorf("%s non-staff %s = %d, want 404 without a ledger", locale.Tag(), target, response.Code)
				}
			}
		}
		if response := get(ctx, "/admin/credits?customer="+administrator.String()); response.Code != http.StatusOK {
			t.Errorf("%s a staff member with no credit = %d, want 200", locale.Tag(), response.Code)
		}
		for _, target := range targets {
			id := target.id.String()
			response := get(ctx, "/admin/credits?customer="+id)
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), reason) {
				t.Errorf("%s %s's ledger = %d, want 200 with the history", locale.Tag(), target.role, response.Code)
			}

			form := url.Values{"email": {target.email}, "amount": {"0"}, "reason": {"Retain refused draft"}, "operation_id": {uuid.NewString()}, "filter_customer": {id}}
			request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/credits", strings.NewReader(form.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			refused := httptest.NewRecorder()
			desk.ServeHTTP(refused, request)
			if refused.Code != http.StatusUnprocessableEntity {
				t.Errorf("%s %s invalid grant = %d, want 422", locale.Tag(), target.role, refused.Code)
			}
			for _, field := range []struct{ input, want string }{{"credit-email", target.email}, {"credit-amount", "0"}, {"credit-reason", "Retain refused draft"}} {
				input := uitest.InputElementByID(t, refused.Body.String(), field.input)
				if got := uitest.InputAttribute(t, input, "value"); got != field.want {
					t.Errorf("%s draft %s = %q, want %q", locale.Tag(), field.input, got, field.want)
				}
			}
			for _, want := range []string{`name="filter_customer" value="` + id + `"`, form.Get("operation_id"), reason} {
				if !strings.Contains(refused.Body.String(), want) {
					t.Errorf("%s refusal lost %q", locale.Tag(), want)
				}
			}
		}
	}

	// Reviewing a grant for somebody else than the customer being viewed leaves
	// that customer's page: the confirmation carries no filter.
	form := url.Values{"email": {targets[1].email}, "amount": {"1"}, "reason": {"Another recipient"}, "operation_id": {uuid.NewString()}, "filter_customer": {targets[0].id.String()}}
	request := httptest.NewRequestWithContext(staff, http.MethodPost, "/admin/credits", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	review := httptest.NewRecorder()
	desk.ServeHTTP(review, request)
	if review.Code != http.StatusOK || !strings.Contains(review.Body.String(), `name="filter_customer" value=""`) || strings.Contains(review.Body.String(), `value="`+targets[0].id.String()+`"`) {
		t.Errorf("reviewing a grant for another customer = %d, still carries the filter", review.Code)
	}

	var after int
	if err := owner.QueryRow(t.Context(), `SELECT count(*) FROM store_credit_entries`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != entries || admintest.AuditRows(t, owner, audit.ActionGrantCredit) != audits {
		t.Error("refused grants wrote ledger entries or audit records")
	}
}
