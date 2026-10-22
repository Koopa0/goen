//go:build integration

package warranty_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/warranty"
)

// TestStore_ReadyNetDeliveredCap: the next expiry respects the net-delivered
// ordinal cap: after a return Ready offers no unit beyond the units delivered
// net of returns, and an over-cap post is refused.
func TestStore_ReadyNetDeliveredCap(t *testing.T) {
	app := warrantyStoreRolePool(t)
	store := warranty.NewStore(app)
	handler := nextWarrantyRouter(store)
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(locale.Tag(), func(t *testing.T) {
			f, firstShipment := reverseWarrantyParcels(t, 2)
			if _, err := pool.Exec(t.Context(), `UPDATE order_shipments SET delivered_at='2026-08-20T12:00:00+08:00' WHERE id=$1`, firstShipment); err != nil {
				t.Fatal(err)
			}
			for _, unit := range []string{"1", "2"} {
				response := nextWarrantyPost(t, handler, f, locale, unit, "CAP-"+uuid.NewString())
				if response.Code != http.StatusSeeOther {
					t.Fatalf("unit%s POST = %d,want303: %s", unit, response.Code, response.Body.String())
				}
			}
			available, err := store.Ready(t.Context(), f.userID)
			if err != nil || len(available) != 1 || available[0].Line.NextUnit() != "3" || available[0].Line.CoverEnds == nil || available[0].Line.CoverEnds.ISO() != "2027-08-12" {
				t.Fatalf("before return ready = %+v (%v),wantunit3 ending2027-08-12", available, err)
			}
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer db.Rollback(t.Context(), tx)
			var orderID, returnID uuid.UUID
			if orderErr := tx.QueryRow(t.Context(), `SELECT order_id FROM order_lines WHERE id=$1`, f.lineID).Scan(&orderID); orderErr != nil {
				t.Fatal(orderErr)
			}
			if requestErr := tx.QueryRow(t.Context(), `INSERT INTO return_requests(order_id,reason) VALUES($1,'') RETURNING id`, orderID).Scan(&returnID); requestErr != nil {
				t.Fatal(requestErr)
			}
			if _, lineErr := tx.Exec(t.Context(), `INSERT INTO return_request_lines(order_id,return_request_id,order_line_id,quantity) VALUES($1,$2,$3,1)`, orderID, returnID, f.lineID); lineErr != nil {
				t.Fatal(lineErr)
			}
			if _, approvalErr := tx.Exec(t.Context(), approveReturnSQL, returnID); approvalErr != nil {
				t.Fatal(approvalErr)
			}
			if commitErr := tx.Commit(t.Context()); commitErr != nil {
				t.Fatal(commitErr)
			}
			owner, err := uuid.Parse(f.userID)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := db.New(app).RegistrableLines(t.Context(), db.RegistrableLinesParams{
				OrderNumber: pgtype.Text{String: f.number, Valid: true}, UserID: uuid.NullUUID{UUID: owner, Valid: true},
			})
			if err != nil || len(rows) != 1 {
				t.Fatalf("next expiry rows = %+v (%v),wantone", rows, err)
			}
			row := rows[0]
			if row.DeliveredUnits != 2 || row.RegisteredUnits != 2 || row.HasExpiry {
				t.Errorf("next expiry = %+v,wantnet2/registered2 without next expiry", row)
			}
			ready, err := store.Ready(t.Context(), f.userID)
			if err != nil || len(ready) != 0 {
				t.Errorf("ready = %+v (%v),wantnone", ready, err)
			}
			before := warrantyRegistrationState(t, f.lineID)
			response := nextWarrantyPost(t, handler, f, locale, "3", "REFUSED-CAP-"+uuid.NewString())
			if response.Code != http.StatusUnprocessableEntity || response.Header().Get("Location") != "" {
				t.Errorf("over-cap unit3 POST = %d/%q,want422 without redirect", response.Code, response.Header().Get("Location"))
			}
			if got := warrantyRegistrationState(t, f.lineID); got != before {
				t.Errorf("over-cap refusal changed registrations = %s,want%s", got, before)
			}
		})
	}
}
