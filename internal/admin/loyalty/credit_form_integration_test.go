//go:build integration

package loyalty_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/loyalty"
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

// TestTheCreditFormCarriesItsOperationThroughReviewToTheBalanceNotice follows
// the form as a browser does: the operation ID the blank form issues is the
// one the review step carries back, and confirming it lands on ?ok=1 with the
// new balance.
func TestTheCreditFormCarriesItsOperationThroughReviewToTheBalanceNotice(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	h := loyalty.NewHandler(loyalty.NewStore(pool), slog.New(slog.DiscardHandler))
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

	blank := serve(http.MethodGet, "/admin/credit", nil, h.Credit)
	if blank.Code != http.StatusOK {
		t.Fatalf("GET /admin/credit answered %d, want 200", blank.Code)
	}
	operation := hiddenField(t, blank.Body.String(), "operation_id")
	if _, err := uuid.Parse(operation); err != nil {
		t.Fatalf("the blank form issued operation_id %q, want a UUID: %v", operation, err)
	}

	review := serve(http.MethodPost, "/admin/credit", url.Values{
		"operation_id": {operation}, "email": {email}, "amount": {"125"}, "reason": {"Courtesy credit"},
	}, h.GrantCredit)
	if review.Code != http.StatusOK {
		t.Fatalf("the review step answered %d, want 200", review.Code)
	}
	if got := hiddenField(t, review.Body.String(), "operation_id"); got != operation {
		t.Fatalf("the review step carries operation_id %q, want the one issued: %q", got, operation)
	}

	confirmed := serve(http.MethodPost, "/admin/credit", url.Values{
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
	if got, want := confirmed.Header().Get("Location"), "/admin/credit?ok=1&balance=12500"; got != want {
		t.Errorf("the confirmed grant redirects to %q, want %q", got, want)
	}
}
