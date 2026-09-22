//go:build integration

package admin_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/koopa0/goen/internal/admin"
)

func TestCreditGrantRequiresRecipientReviewBeforePosting(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	h := adminHandlerOver(pool, s)
	var customer uuid.UUID
	email := "confirm-" + uuid.NewString() + "@example.com"
	if err := pool.QueryRow(ctx, `INSERT INTO users(email, full_name) VALUES ($1, 'Credit recipient') RETURNING id`, email).Scan(&customer); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"email": {email}, "amount": {"125"}, "reason": {"Courtesy credit"}, "operation_id": {uuid.NewString()}}
	post := func() *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/credit", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h.RequireStaff(h.GrantCredit)(w, req)
		return w
	}
	count := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM store_credit_entries e JOIN store_credit_accounts a ON a.id=e.account_id WHERE a.user_id=$1`, customer).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	w := post()
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Credit recipient") || !strings.Contains(w.Body.String(), email) || !strings.Contains(w.Body.String(), "NT$0") || !strings.Contains(w.Body.String(), "125") {
		t.Fatalf("missing recipient review: %d %s", w.Code, w.Body.String())
	}
	if count() != 0 {
		t.Fatal("preview posted credit before confirmation")
	}
	form.Set("confirm", "grant")
	w = post()
	if w.Code != http.StatusOK || count() != 0 {
		t.Fatal("confirmation without reviewed identity posted credit")
	}
	form.Set("customer_id", customer.String())
	for range 2 {
		w = post()
		if w.Code != http.StatusSeeOther {
			t.Fatalf("confirmed grant = %d %s", w.Code, w.Body.String())
		}
	}
	if count() != 1 {
		t.Fatal("confirmation replay did not retain one durable grant")
	}
	var balance int64
	if err := pool.QueryRow(ctx, `SELECT balance_cents FROM store_credit_balances WHERE user_id=$1`, customer).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if balance != 12500 {
		t.Fatalf("confirmed grant balance=%d, want 12500", balance)
	}
}

func TestCreditConfirmationDoesNotFollowAReassignedEmail(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	h := adminHandlerOver(pool, s)
	var original, replacement uuid.UUID
	email := "reassigned-" + uuid.NewString() + "@example.com"
	if err := pool.QueryRow(ctx, `INSERT INTO users(email,full_name) VALUES($1,'Original recipient') RETURNING id`, email).Scan(&original); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"email": {email}, "amount": {"25"}, "reason": {"Keep recipient"}, "operation_id": {uuid.NewString()}}
	post := func() *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/credit", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h.RequireStaff(h.GrantCredit)(w, req)
		return w
	}
	if w := post(); w.Code != http.StatusOK {
		t.Fatalf("preview = %d", w.Code)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET email=$2 WHERE id=$1`, original, "moved-"+email); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users(email,full_name) VALUES($1,'Different recipient') RETURNING id`, email).Scan(&replacement); err != nil {
		t.Fatal(err)
	}
	form.Set("customer_id", original.String())
	form.Set("confirm", "grant")
	w := post()
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Different recipient") {
		t.Fatalf("changed identity must be reviewed: %d %s", w.Code, w.Body.String())
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM store_credit_entries e JOIN store_credit_accounts a ON a.id=e.account_id WHERE a.user_id IN ($1,$2)`, original, replacement).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("reassigned email transferred credit without identity review")
	}
	form.Set("email", "unknown-"+email)
	w = post()
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), `aria-invalid="true"`) || !strings.Contains(w.Body.String(), "Keep recipient") {
		t.Fatalf("unknown recipient must preserve refused form: %d %s", w.Code, w.Body.String())
	}
}
