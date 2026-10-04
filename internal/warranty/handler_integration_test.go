//go:build integration

package warranty_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/warranty"
)

// TestPostingTheRegistrationFormRegistersTheUnitAndReturnsToItsOrder drives
// POST /account/warranty/{number} for a delivered unit: the registration is
// stored under the posted serial and the browser lands on the order page's
// confirmation.
func TestPostingTheRegistrationFormRegistersTheUnitAndReturnsToItsOrder(t *testing.T) {
	f := newFixture(t, 1, 12, parcel{units: 1, arrived: true})
	h := warranty.NewHandler(warranty.NewStore(pool), slog.New(slog.DiscardHandler))

	form := url.Values{"line": {f.lineID.String()}, "unit": {"1"}, "serial": {"SN-HANDLER-0001"}}
	req := httptest.NewRequestWithContext(
		account.WithUser(t.Context(), account.User{ID: f.userID, Role: account.RoleCustomer}),
		http.MethodPost, "/account/warranty/"+f.number, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("number", f.number)
	w := httptest.NewRecorder()
	h.Register(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("Register answered %d, want 303; body=%s", w.Code, w.Body.String())
	}
	if got, want := w.Header().Get("Location"), "/account/warranty/"+url.PathEscape(f.number)+"?ok=1"; got != want {
		t.Errorf("Register redirects to %q, want %q", got, want)
	}
	var serial string
	if err := pool.QueryRow(t.Context(),
		`SELECT serial_number FROM warranty_registrations WHERE order_line_id = $1 AND unit_no = 1`,
		f.lineID).Scan(&serial); err != nil {
		t.Fatalf("read the registration: %v", err)
	}
	if serial != "SN-HANDLER-0001" {
		t.Errorf("the registration holds serial %q, want %q", serial, "SN-HANDLER-0001")
	}
}
