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
	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/warranty"
)

func reverseWarrantyParcels(t *testing.T, laterQuantity int) (fixture, uuid.UUID) {
	t.Helper()
	f := newFixture(t, laterQuantity+1, 12, parcel{units: 1})
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Rollback(t.Context(), tx)
	var orderID, firstShipment uuid.UUID
	if err := tx.QueryRow(t.Context(), `SELECT s.order_id,s.id FROM order_shipments s JOIN order_shipment_lines sl ON sl.shipment_id=s.id WHERE sl.order_line_id=$1`, f.lineID).Scan(&orderID, &firstShipment); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `UPDATE order_shipments SET shipped_at='2026-08-08T12:00:00+08:00' WHERE id=$1`, firstShipment); err != nil {
		t.Fatal(err)
	}
	var secondShipment uuid.UUID
	if err := tx.QueryRow(t.Context(), `INSERT INTO order_shipments(order_id,carrier,tracking_number,shipped_at,delivered_at)
 VALUES($1,'black_cat','REVERSED-'||$2,'2026-08-10T12:00:00+08:00','2026-08-12T12:00:00+08:00') RETURNING id`, orderID, f.number).Scan(&secondShipment); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `INSERT INTO order_shipment_lines(order_id,shipment_id,order_line_id,quantity) VALUES($1,$2,$3,$4)`, orderID, secondShipment, f.lineID, laterQuantity); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	return f, firstShipment
}

func nextWarrantyRouter(store *warranty.Store) http.Handler {
	h := warranty.NewHandler(store, slog.New(slog.DiscardHandler))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /account/warranty", h.List)
	mux.HandleFunc("GET /account/warranty/{number}", h.Order)
	mux.HandleFunc("POST /account/warranty/{number}", h.Register)
	return mux
}

func nextWarrantyGet(t *testing.T, handler http.Handler, f fixture, locale i18n.Locale, target string) *httptest.ResponseRecorder {
	t.Helper()
	ctx := user.NewContext(i18n.WithLocale(t.Context(), locale), user.User{ID: f.userID, Role: user.RoleCustomer})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, target, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want200: %s", target, rec.Code, rec.Body.String())
	}
	return rec
}

func nextWarrantyPost(t *testing.T, handler http.Handler, f fixture, locale i18n.Locale, unit, serial string) *httptest.ResponseRecorder {
	t.Helper()
	ctx := user.NewContext(i18n.WithLocale(t.Context(), locale), user.User{ID: f.userID, Role: user.RoleCustomer})
	form := url.Values{"line": {f.lineID.String()}, "unit": {unit}, "serial": {serial}}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/account/warranty/"+f.number, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func assertNoNextWarranty(t *testing.T, store *warranty.Store, handler http.Handler, f fixture, locale i18n.Locale, net, registered int, neutral bool) {
	t.Helper()
	ctx := i18n.WithLocale(t.Context(), locale)
	view, err := store.Registrable(ctx, f.number, f.userID)
	if err != nil || len(view.Lines) != 1 {
		t.Fatalf("registrable = %+v (%v), wantone line", view, err)
	}
	line := view.Lines[0]
	if line.Delivered != net || line.Registered != registered || line.Registrable() || line.CoverEnds != nil {
		t.Errorf("next line = %+v, want net%d/registered%d without eligible expiry", line, net, registered)
	}
	ready, err := store.Ready(ctx, f.userID)
	if err != nil || len(ready) != 0 {
		t.Fatalf("ready = %+v (%v), wantnone", ready, err)
	}
	body := nextWarrantyGet(t, handler, f, locale, "/account/warranty/"+f.number).Body.String()
	if strings.Contains(body, `name="unit"`) || strings.Contains(body, `id="serial-`+f.lineID.String()+`"`) {
		t.Error("unavailable next ordinal renders a native registration form")
	}
	if neutral {
		if strings.Count(body, i18n.T(ctx, i18n.KeyWarrantyNextUnavailable)) != 1 || strings.Contains(body, i18n.T(ctx, i18n.KeyWarrantyOrderAllDone)) || strings.Contains(body, i18n.T(ctx, i18n.KeyWarrantyAfterShipping)) || strings.Contains(body, "2027-08-12") || strings.Contains(body, "2027-08-20") {
			t.Error("unavailable ordinal claims an expiry, future delivery eligibility or all-registered state")
		}
	} else if !strings.Contains(body, i18n.T(ctx, i18n.KeyWarrantyReturned)) || strings.Contains(body, i18n.T(ctx, i18n.KeyWarrantyOrderAllDone)) || strings.Contains(body, i18n.T(ctx, i18n.KeyWarrantyAfterShipping)) {
		t.Error("returned line promises future registration or says all units were registered")
	}
	list := nextWarrantyGet(t, handler, f, locale, "/account/warranty").Body.String()
	if strings.Contains(list, `id="warranty-ready-heading"`) {
		t.Error("unavailable ordinal appears in Ready to register")
	}
}

func TestWarrantyReverseParcelArrivalWaitsForTheActualNextUnit(t *testing.T) {
	store := warranty.NewStore(warrantyStoreRolePool(t))
	handler := nextWarrantyRouter(store)
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(locale.Tag(), func(t *testing.T) {
			f, firstShipment := reverseWarrantyParcels(t, 1)
			assertNoNextWarranty(t, store, handler, f, locale, 1, 0, true)
			before := warrantyRegistrationState(t, f.lineID)
			for _, unit := range []string{"1", "2"} {
				serial := " Raw & \"" + uuid.NewString() + "\" "
				response := nextWarrantyPost(t, handler, f, locale, unit, serial)
				nodes := warrantyResponseNodes(t, response.Body.String())
				attrs := warrantyResponseAttrs(nodes["serial-"+f.lineID.String()])
				_, readonly := attrs["readonly"]
				if attrs["value"] != serial || !readonly || strings.Contains(response.Body.String(), `name="unit"`) || warrantyResponseText(nodes["warranty-refusal"]) != i18n.T(i18n.WithLocale(t.Context(), locale), i18n.KeyWarrantyRefused) {
					t.Errorf("unavailable next-unit refusal lost its raw readonly serial or global refusal: %v", attrs)
				}
				if response.Code != http.StatusUnprocessableEntity || response.Header().Get("Location") != "" {
					t.Errorf("unit%s POST = %d, want422 without redirect", unit, response.Code)
				}
			}
			if got := warrantyRegistrationState(t, f.lineID); got != before {
				t.Errorf("refused registrations = %s, want%s", got, before)
			}
			if _, err := pool.Exec(t.Context(), `UPDATE order_shipments SET delivered_at='2026-08-20T12:00:00+08:00' WHERE id=$1`, firstShipment); err != nil {
				t.Fatal(err)
			}
			for _, step := range []struct{ unit, expiry string }{{"1", "2027-08-20"}, {"2", "2027-08-12"}} {
				ready, err := store.Ready(t.Context(), f.userID)
				if err != nil || len(ready) != 1 || ready[0].Line.NextUnit() != step.unit || ready[0].Line.CoverEnds == nil || ready[0].Line.CoverEnds.ISO() != step.expiry {
					t.Fatalf("ready = %+v (%v), wantunit%s ending%s", ready, err, step.unit, step.expiry)
				}
				serial := "REVERSED-" + uuid.NewString()
				response := nextWarrantyPost(t, handler, f, locale, step.unit, serial)
				if response.Code != http.StatusSeeOther {
					t.Fatalf("unit%s POST = %d, want303: %s", step.unit, response.Code, response.Body.String())
				}
				follow := nextWarrantyGet(t, handler, f, locale, response.Header().Get("Location"))
				confirmation := warrantyResponseText(warrantyResponseNodes(t, follow.Body.String())["warranty-registered"])
				if !strings.Contains(confirmation, serial) || !strings.Contains(confirmation, step.expiry) {
					t.Errorf("unit%s confirmation = %q, wantserial%s/expiry%s", step.unit, confirmation, serial, step.expiry)
				}
			}
			var saved int
			if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM warranty_registrations WHERE order_line_id=$1`, f.lineID).Scan(&saved); err != nil || saved != 2 {
				t.Fatalf("saved registrations = %d (%v), want2", saved, err)
			}
		})
	}
}

func TestWarrantyRegistrationGapDoesNotOfferAnAlreadyRegisteredOrdinal(t *testing.T) {
	store := warranty.NewStore(warrantyStoreRolePool(t))
	handler := nextWarrantyRouter(store)
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(locale.Tag(), func(t *testing.T) {
			f, _ := reverseWarrantyParcels(t, 2)
			response := nextWarrantyPost(t, handler, f, locale, "2", "GAP-"+uuid.NewString())
			if response.Code != http.StatusSeeOther {
				t.Fatalf("existingwriter unit2 POST = %d, want303: %s", response.Code, response.Body.String())
			}
			assertNoNextWarranty(t, store, handler, f, locale, 2, 1, true)
			before := warrantyRegistrationState(t, f.lineID)
			response = nextWarrantyPost(t, handler, f, locale, "2", "DUPLICATE-"+uuid.NewString())
			if response.Code != http.StatusUnprocessableEntity || warrantyRegistrationState(t, f.lineID) != before {
				t.Error("duplicate ordinal was accepted or changed registrations")
			}
		})
	}
}

func TestWarrantyReverseArrivalKeepsDecidedReturnCounts(t *testing.T) {
	store := warranty.NewStore(warrantyStoreRolePool(t))
	handler := nextWarrantyRouter(store)
	for _, tc := range []struct {
		status string
		net    int
	}{{"requested", 1}, {"rejected", 1}, {"approved", 0}, {"completed", 0}} {
		for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
			t.Run(tc.status+"/"+locale.Tag(), func(t *testing.T) {
				f, _ := reverseWarrantyParcels(t, 1)
				tx, err := pool.Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer db.Rollback(t.Context(), tx)
				var orderID, returnID uuid.UUID
				if err := tx.QueryRow(t.Context(), `SELECT order_id FROM order_lines WHERE id=$1`, f.lineID).Scan(&orderID); err != nil {
					t.Fatal(err)
				}
				if err := tx.QueryRow(t.Context(), `INSERT INTO return_requests(order_id,reason) VALUES($1,'') RETURNING id`, orderID).Scan(&returnID); err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(t.Context(), `INSERT INTO return_request_lines(order_id,return_request_id,order_line_id,quantity) VALUES($1,$2,$3,1)`, orderID, returnID, f.lineID); err != nil {
					t.Fatal(err)
				}
				if tc.status == "rejected" {
					if _, err := tx.Exec(t.Context(), `UPDATE return_requests SET status='rejected',decided_at=now() WHERE id=$1`, returnID); err != nil {
						t.Fatal(err)
					}
				}
				if tc.status == "approved" || tc.status == "completed" {
					if _, err := tx.Exec(t.Context(), approveReturnSQL, returnID); err != nil {
						t.Fatal(err)
					}
				}
				if tc.status == "completed" {
					completeWarrantyReturn(t, tx, returnID)
				}
				if err := tx.Commit(t.Context()); err != nil {
					t.Fatal(err)
				}
				assertNoNextWarranty(t, store, handler, f, locale, tc.net, 0, tc.net > 0)
				before := warrantyRegistrationState(t, f.lineID)
				for _, unit := range []string{"1", "2"} {
					response := nextWarrantyPost(t, handler, f, locale, unit, "RETURN-"+uuid.NewString())
					if response.Code != http.StatusUnprocessableEntity {
						t.Errorf("returned unit%s POST = %d,want422", unit, response.Code)
					}
				}
				if got := warrantyRegistrationState(t, f.lineID); got != before {
					t.Error("returned-unit refusal changed stored registrations")
				}
			})
		}
	}
}

func completeWarrantyReturn(t *testing.T, tx pgx.Tx, returnID uuid.UUID) {
	t.Helper()
	var staffID, refundID uuid.UUID
	if err := tx.QueryRow(t.Context(), `INSERT INTO users(email,role,full_name) VALUES('warranty-return-'||gen_random_uuid()||'@goen.invalid','staff','Warranty return fixture') RETURNING id`).Scan(&staffID); err != nil {
		t.Fatal(err)
	}
	requestID := "warranty-next-" + returnID.String()
	if err := tx.QueryRow(t.Context(), `SELECT claim_return_refund_execution($1,$2,$3)`, returnID, staffID, requestID).Scan(&refundID); err != nil {
		t.Fatal(err)
	}
	var recorded bool
	if err := tx.QueryRow(t.Context(), `SELECT record_refund_succeeded($1,$2,$3,$4)`, refundID, "re_"+strings.ReplaceAll(refundID.String(), "-", ""), staffID, requestID).Scan(&recorded); err != nil || !recorded {
		t.Fatalf("record completed-return refund = %t (%v), want true", recorded, err)
	}
	for _, sql := range []string{
		`INSERT INTO order_events(order_id,kind,return_request_id) SELECT order_id,'refunded',id FROM return_requests WHERE id=$1`,
		`UPDATE return_request_lines SET received_quantity=1,restocked_quantity=0 WHERE return_request_id=$1`,
		`UPDATE return_requests SET status='completed' WHERE id=$1`,
	} {
		if _, err := tx.Exec(t.Context(), sql, returnID); err != nil {
			t.Fatal(err)
		}
	}
}
