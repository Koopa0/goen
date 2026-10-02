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
	"github.com/koopa0/goen/internal/i18n"
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
		backOffice.RequireStaff(h.GrantCredit)(w, req)
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
		backOffice.RequireStaff(h.GrantCredit)(w, req)
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
	for _, want := range []string{`aria-describedby="credit-email-error"`, `id="credit-email-error"`, i18n.T(ctx, i18n.KeyAdminCreditUnknown)} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("unknown recipient error missing %q", want)
		}
	}
	form.Set("amount", "0")
	form.Set("reason", "")
	w = post()
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid grant fields = %d", w.Code)
	}
	for _, want := range []string{`aria-describedby="credit-amount-error"`, `id="credit-amount-error"`, i18n.T(ctx, i18n.KeyAdminCreditAmountError), `aria-describedby="credit-reason-error"`, `id="credit-reason-error"`, i18n.T(ctx, i18n.KeyAdminCreditReasonError)} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("refused credit field missing %q", want)
		}
	}
	if strings.Contains(w.Body.String(), i18n.T(ctx, i18n.KeyAdminNoticeNeeds)) {
		t.Error("the credit form's refusal carries the dispatch form's banner above the field errors")
	}
}

func TestDispatchWithARecordedTrackingNumberIsRefusedOnTheField(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	h := adminHandlerOver(pool, s)
	first, second := shippableOrder(t, "zh-Hant"), shippableOrder(t, "zh-Hant")
	tracking := "DUP-" + uuid.NewString()[:8]
	if err := s.Ship(ctx, first, admin.Dispatch{Carrier: "black_cat", Tracking: tracking},
		uuid.NullUUID{UUID: staff, Valid: true}); err != nil {
		t.Fatalf("first dispatch: %v", err)
	}

	form := url.Values{"carrier": {"black_cat"}, "tracking": {tracking}}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/orders/"+second+"/ship", strings.NewReader(form.Encode()))
	req.SetPathValue("number", second)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	backOffice.RequireStaff(h.Ship)(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a reused tracking number answered %d, want 422", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{`aria-invalid="true"`, `aria-describedby="ship-tracking-error"`,
		`id="ship-tracking-error"`, `value="` + tracking + `"`, i18n.T(ctx, i18n.KeyAdminTrackingTaken)} {
		if !strings.Contains(body, want) {
			t.Errorf("refused dispatch is missing %q", want)
		}
	}
	var parcels int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM order_shipments s JOIN orders o ON o.id = s.order_id WHERE o.order_number = $1`,
		second).Scan(&parcels); err != nil {
		t.Fatal(err)
	}
	if parcels != 0 {
		t.Errorf("the refused dispatch left %d parcels on the second order", parcels)
	}
}
