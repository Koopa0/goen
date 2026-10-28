//go:build integration

package credits_test

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/credits"
	"github.com/koopa0/goen/internal/i18n"
)

func hiddenField(t *testing.T, body, name string) string {
	t.Helper()
	m := regexp.MustCompile(`<input type="hidden" name="` + regexp.QuoteMeta(name) + `" value="([^"]*)"`).
		FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no hidden %q field in %s", name, body)
	}
	return m[1]
}

// TestHandler_GrantCreditOperationID follows
// the form as a browser does: the operation ID the blank form issues is the
// one the review step carries back, and confirming it lands on the paid customer's
// ledger with the granted notice, which states the balance read then.
func TestHandler_GrantCreditOperationID(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	h := credits.NewHandler(credits.NewStore(pool), slog.New(slog.DiscardHandler))
	email := "form-" + uuid.NewString() + "@example.com"
	if _, err := pool.Exec(ctx,
		`INSERT INTO users(email, full_name) VALUES ($1, 'Form recipient')`, email); err != nil {
		t.Fatalf("create recipient: %v", err)
	}

	serve := func(method, target string, form url.Values, handler http.HandlerFunc) *httptest.ResponseRecorder {
		t.Helper()
		body := strings.NewReader("")
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		req := httptest.NewRequestWithContext(ctx, method, target, body)
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		w := httptest.NewRecorder()
		admintest.BackOffice.RequireStaff(handler)(w, req)
		return w
	}

	blank := serve(http.MethodGet, "/admin/credits", nil, h.Credit)
	if blank.Code != http.StatusOK {
		t.Fatalf("GET /admin/credits answered %d, want 200", blank.Code)
	}
	operation := hiddenField(t, blank.Body.String(), "operation_id")
	if _, err := uuid.Parse(operation); err != nil {
		t.Fatalf("the blank form issued operation_id %q, want a UUID: %v", operation, err)
	}

	review := serve(http.MethodPost, "/admin/credits", url.Values{
		"operation_id": {operation}, "email": {email}, "amount": {"125"}, "reason": {"Courtesy credit"},
	}, h.GrantCredit)
	if review.Code != http.StatusOK {
		t.Fatalf("the review step answered %d, want 200", review.Code)
	}
	if got := hiddenField(t, review.Body.String(), "operation_id"); got != operation {
		t.Fatalf("the review step carries operation_id %q, want the one issued: %q", got, operation)
	}

	confirmed := serve(http.MethodPost, "/admin/credits", url.Values{
		"operation_id": {hiddenField(t, review.Body.String(), "operation_id")},
		"customer_id":  {hiddenField(t, review.Body.String(), "customer_id")},
		"email":        {hiddenField(t, review.Body.String(), "email")},
		"amount":       {hiddenField(t, review.Body.String(), "amount")},
		"reason":       {hiddenField(t, review.Body.String(), "reason")},
		"confirm":      {"grant"},
	}, h.GrantCredit)
	if confirmed.Code != http.StatusSeeOther {
		t.Fatalf("the confirmed grant answered %d, want 303; body=%s", confirmed.Code, confirmed.Body.String())
	}
	customerID := hiddenField(t, review.Body.String(), "customer_id")
	location := confirmed.Header().Get("Location")
	if want := "/admin/credits?customer=" + customerID + "&granted=1"; location != want {
		t.Fatalf("the confirmed grant redirects to %q, want %q", location, want)
	}

	format := i18n.T(ctx, i18n.KeyAdminNoticeCreditGranted)
	lead := html.EscapeString(strings.SplitN(format, "%s", 2)[0])
	sentence := html.EscapeString(fmt.Sprintf(format, "NT$125"))
	for _, tc := range []struct {
		name, target string
		status       int
		shown        bool
	}{
		{"the paid customer's ledger", location, http.StatusOK, true},
		{"granted with no customer", "/admin/credits?granted=1", http.StatusOK, false},
		{"granted for a customer that does not exist", "/admin/credits?customer=" + uuid.NewString() + "&granted=1", http.StatusNotFound, false},
		{"the address the old redirect used", "/admin/credits?ok=1&balance=99900", http.StatusOK, false},
		{"the customer's ledger without granted", "/admin/credits?customer=" + customerID, http.StatusOK, false},
	} {
		got := serve(http.MethodGet, tc.target, nil, h.Credit)
		if got.Code != tc.status {
			t.Errorf("%s: GET %s answered %d, want %d", tc.name, tc.target, got.Code, tc.status)
		}
		body := got.Body.String()
		if shown := strings.Contains(body, sentence); shown != tc.shown {
			t.Errorf("%s: the page states the balance %q = %t, want %t", tc.name, sentence, shown, tc.shown)
		}
		if !tc.shown && strings.Contains(body, lead) {
			t.Errorf("%s: the page shows the granted notice", tc.name)
		}
	}
}
