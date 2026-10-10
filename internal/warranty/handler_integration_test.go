//go:build integration

package warranty_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/warranty"
)

func TestWarrantyRegistrationBindsTheOrderURL(t *testing.T) {
	app := warrantyStoreRolePool(t)
	h := warranty.NewHandler(warranty.NewStore(app), slog.New(slog.DiscardHandler))
	mux := http.NewServeMux()
	mux.HandleFunc("POST /account/warranty/{number}", h.Register)
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, tc := range []struct {
			name   string
			status int
		}{
			{name: "mismatched order", status: http.StatusUnprocessableEntity},
			{name: "correct order", status: http.StatusSeeOther},
			{name: "another customer's line", status: http.StatusUnprocessableEntity},
		} {
			t.Run(locale.Tag()+"/"+tc.name, func(t *testing.T) {
				mine := newFixture(t, 1, 12, parcel{units: 1, arrived: true})
				owner, err := uuid.Parse(mine.userID)
				if err != nil {
					t.Fatal(err)
				}
				second := newFixtureForCustomer(t, owner, 1, 12, parcel{units: 1, arrived: true})
				other := newFixture(t, 1, 12, parcel{units: 1, arrived: true})
				line, number := mine.lineID, mine.number
				visibleLine := mine.lineID
				switch tc.name {
				case "mismatched order":
					number = second.number
					visibleLine = second.lineID
				case "another customer's line":
					line = other.lineID
				}
				before := warrantyRegistrationState(t, mine.lineID, second.lineID, other.lineID)
				ctx := user.NewContext(i18n.WithLocale(t.Context(), locale), user.User{ID: mine.userID, Role: user.RoleCustomer})
				serial := "SN-ORDER-" + uuid.NewString()
				form := url.Values{"line": {line.String()}, "unit": {"1"}, "serial": {serial}}
				req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/account/warranty/"+number, strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)
				if rec.Code != tc.status {
					t.Fatalf("Register status = %d, want %d; body=%s", rec.Code, tc.status, rec.Body.String())
				}
				if tc.status == http.StatusSeeOther {
					if got, want := rec.Header().Get("Location"), "/account/warranty/"+url.PathEscape(mine.number)+"?ok=1"; got != want {
						t.Errorf("Register redirect = %q, want %q", got, want)
					}
					var savedSerial, savedOwner string
					if err := pool.QueryRow(t.Context(), `SELECT serial_number, user_id::text FROM warranty_registrations WHERE order_line_id = $1 AND unit_no = 1`, mine.lineID).Scan(&savedSerial, &savedOwner); err != nil {
						t.Fatal(err)
					}
					if savedSerial != serial || savedOwner != mine.userID {
						t.Errorf("saved registration = (%q, %q), want (%q, %q)", savedSerial, savedOwner, serial, mine.userID)
					}
					return
				}
				if got := warrantyRegistrationState(t, mine.lineID, second.lineID, other.lineID); got != before {
					t.Errorf("refusal changed stored registrations = %s, want %s", got, before)
				}
				if location := rec.Header().Get("Location"); location != "" {
					t.Errorf("refusal redirects to %q", location)
				}
				nodes := warrantyResponseNodes(t, rec.Body.String())
				alert := nodes["warranty-refusal"]
				if got, want := warrantyResponseText(alert), i18n.T(ctx, i18n.KeyWarrantyRefused); got != want || warrantyResponseAttrs(alert)["role"] != "alert" {
					t.Errorf("refusal alert = %q, want %q with role=alert", got, want)
				}
				if nodes["serial-"+line.String()] != nil || strings.Contains(rec.Body.String(), serial) {
					t.Error("refusal exposed a draft from outside the URL's order")
				}
				input := nodes["serial-"+visibleLine.String()]
				attrs := warrantyResponseAttrs(input)
				if input == nil || attrs["value"] != "" || attrs["aria-invalid"] != "" {
					t.Errorf("refusal must keep the URL's order input unchanged: %v", attrs)
				}
			})
		}
	}
}

// TestPostingTheRegistrationFormRegistersTheUnitAndReturnsToItsOrder drives
// POST /account/warranty/{number} for a delivered unit: the registration is
// stored under the posted serial and the browser lands on the order page's
// confirmation.
func TestPostingTheRegistrationFormRegistersTheUnitAndReturnsToItsOrder(t *testing.T) {
	f := newFixture(t, 1, 12, parcel{units: 1, arrived: true})
	h := warranty.NewHandler(warranty.NewStore(pool), slog.New(slog.DiscardHandler))

	form := url.Values{"line": {f.lineID.String()}, "unit": {"1"}, "serial": {"SN-HANDLER-0001"}}
	req := httptest.NewRequestWithContext(
		user.NewContext(t.Context(), user.User{ID: f.userID, Role: user.RoleCustomer}),
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
