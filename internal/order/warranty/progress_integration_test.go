//go:build integration

package warranty_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/warranty"
	"github.com/koopa0/goen/internal/user"
)

// TestStore_ReadyOwnParcelExpiry: the next expiry and the confirmation use the
// unit's own parcel: Ready offers each unit with its own parcel's expiry, and
// the confirmation page selects the unit just registered and never a foreign
// one.
func TestStore_ReadyOwnParcelExpiry(t *testing.T) {
	app := warrantyStoreRolePool(t)
	store := warranty.NewStore(app)
	handler := warranty.NewHandler(store, slog.New(slog.DiscardHandler))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /account/warranty/{number}", handler.Order)
	mux.HandleFunc("POST /account/warranty/{number}", handler.Register)
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(locale.Tag(), func(t *testing.T) {
			first := time.Date(2026, time.August, 10, 4, 0, 0, 0, time.UTC)
			second := first.Add(9 * 24 * time.Hour)
			f := newFixture(t, 2, 12, parcel{units: 1, arrived: true, deliveredAt: first})
			var orderID, shipmentID uuid.UUID
			if err := pool.QueryRow(t.Context(), `SELECT order_id FROM order_lines WHERE id = $1`, f.lineID).Scan(&orderID); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(t.Context(), `INSERT INTO order_shipments (order_id, carrier, tracking_number, shipped_at, delivered_at)
				VALUES ($1, 'black_cat', 'SECOND-' || $2, $3, $4) RETURNING id`, orderID, f.number, second.Add(-48*time.Hour), second).Scan(&shipmentID); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(t.Context(), `INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
				VALUES ($1, $2, $3, 1)`, orderID, shipmentID, f.lineID); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(t.Context(), `UPDATE products SET warranty_months = 1 WHERE id = $1`, f.productID); err != nil {
				t.Fatal(err)
			}
			ctx := user.NewContext(i18n.WithLocale(t.Context(), locale), user.User{ID: f.userID, Role: user.RoleCustomer})
			serials := []string{"FIRST-" + uuid.NewString(), "SECOND-" + uuid.NewString()}
			for _, step := range []struct {
				unit, expires string
				serial        string
			}{
				{unit: "1", expires: "2027-08-10", serial: serials[0]},
				{unit: "2", expires: "2027-08-19", serial: serials[1]},
			} {
				ready, err := store.Ready(ctx, f.userID)
				if err != nil {
					t.Fatal(err)
				}
				if len(ready) != 1 || ready[0].Line.ID != f.lineID.String() || ready[0].Line.CoverEnds == nil || ready[0].Line.CoverEnds.ISO() != step.expires || ready[0].Line.NextUnit() != step.unit {
					t.Fatalf("next registration = %+v, want unit %s ending %s", ready, step.unit, step.expires)
				}
				form := url.Values{"line": {f.lineID.String()}, "unit": {step.unit}, "serial": {step.serial}}
				request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/account/warranty/"+f.number, strings.NewReader(form.Encode()))
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				response := httptest.NewRecorder()
				mux.ServeHTTP(response, request)
				if response.Code != http.StatusSeeOther {
					t.Fatalf("registration status = %d, want 303; %s", response.Code, response.Body.String())
				}
				follow := httptest.NewRecorder()
				mux.ServeHTTP(follow, httptest.NewRequestWithContext(ctx, http.MethodGet, response.Header().Get("Location"), nil))
				if follow.Code != http.StatusOK {
					t.Fatalf("confirmation status = %d, want 200", follow.Code)
				}
				confirmation := warrantyResponseNodes(t, follow.Body.String())["warranty-registered"]
				text := warrantyResponseText(confirmation)
				if warrantyResponseAttrs(confirmation)["role"] != "status" || !strings.Contains(text, step.serial) || !strings.Contains(text, step.expires) || !strings.Contains(follow.Body.String(), `href="/account/warranty"`) {
					t.Fatalf("unit %s confirmation = %q, want serial %q, expiry %s and all-warranties link", step.unit, text, step.serial, step.expires)
				}
				if step.unit == "2" && strings.Contains(text, serials[0]) {
					t.Error("second confirmation selected the first unit")
				}
			}
			ready, err := store.Ready(ctx, f.userID)
			if err != nil || len(ready) != 0 {
				t.Fatalf("ready after registering both = %+v, error=%v, want none", ready, err)
			}
			foreign := newFixture(t, 1, 12, parcel{units: 1, arrived: true})
			if err := store.Register(ctx, foreign.number, foreign.lineID.String(), foreign.userID, "FOREIGN-"+uuid.NewString(), 1); err != nil {
				t.Fatal(err)
			}
			forged := httptest.NewRecorder()
			mux.ServeHTTP(forged, httptest.NewRequestWithContext(ctx, http.MethodGet, "/account/warranty/"+f.number+"?ok=1&line="+foreign.lineID.String()+"&unit=1", nil))
			if forged.Code != http.StatusOK || strings.Contains(forged.Body.String(), `id="warranty-registered"`) {
				t.Error("foreign registration produced an owned-order confirmation")
			}
		})
	}
}

// TestStore_ReadyBoundaries: the ready list keeps the ownership, delivery and
// return boundaries: Ready lists only the customer's own delivered, unreturned
// lines, and the list page links each to its form.
func TestStore_ReadyBoundaries(t *testing.T) {
	app := warrantyStoreRolePool(t)
	store := warranty.NewStore(app)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /account/warranty", warranty.NewHandler(store, slog.New(slog.DiscardHandler)).List)
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(locale.Tag(), func(t *testing.T) {
			ready := newFixture(t, 1, 24, parcel{units: 1, arrived: true})
			owner, parseErr := uuid.Parse(ready.userID)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			waiting := newFixtureForCustomer(t, owner, 1, 24, parcel{units: 1})
			noTerm := newFixtureForCustomer(t, owner, 1, 0, parcel{units: 1, arrived: true})
			registered := newFixtureForCustomer(t, owner, 1, 24, parcel{units: 1, arrived: true})
			returned := newFixtureForCustomer(t, owner, 1, 24, parcel{units: 1, arrived: true})
			foreign := newFixture(t, 1, 24, parcel{units: 1, arrived: true})
			guest := newFixtureForGuest(t, 1, 24, parcel{units: 1, arrived: true})
			if err := store.Register(t.Context(), registered.number, registered.lineID.String(), registered.userID, "", 1); err != nil {
				t.Fatal(err)
			}
			var orderID, returnID uuid.UUID
			if err := pool.QueryRow(t.Context(), `SELECT order_id FROM order_lines WHERE id = $1`, returned.lineID).Scan(&orderID); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(t.Context(), `INSERT INTO return_requests (order_id, reason) VALUES ($1, '') RETURNING id`, orderID).Scan(&returnID); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(t.Context(), `INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity) VALUES ($1,$2,$3,1)`, orderID, returnID, returned.lineID); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(t.Context(), `UPDATE return_requests SET status = 'approved', decided_at = now(), goods_refund_cents = 0, card_refund_cents = 0, credit_refund_cents = 0 WHERE id = $1`, returnID); err != nil {
				t.Fatal(err)
			}
			ctx := user.NewContext(i18n.WithLocale(t.Context(), locale), user.User{ID: ready.userID, Role: user.RoleCustomer})
			rows, err := store.Ready(ctx, ready.userID)
			if err != nil || len(rows) != 1 || rows[0].Number != ready.number || rows[0].Line.ID != ready.lineID.String() {
				t.Fatalf("own ready lines = %+v, error=%v, want only %s", rows, err, ready.number)
			}
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequestWithContext(ctx, http.MethodGet, "/account/warranty", nil))
			if response.Code != http.StatusOK {
				t.Fatalf("list status = %d, want 200", response.Code)
			}
			body := response.Body.String()
			if !strings.Contains(body, `/account/warranty/`+ready.number+`#serial-`+ready.lineID.String()) {
				t.Error("ready list has no link to its own registration form")
			}
			for _, excluded := range []fixture{waiting, noTerm, registered, returned, foreign, guest} {
				if strings.Contains(body, `/account/warranty/`+excluded.number+`#serial-`) {
					t.Errorf("ineligible order %s has a ready link", excluded.number)
				}
			}
		})
	}
}
