//go:build integration

package orders_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/admin/orders"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
)

// TestHandler_DeliverShipmentLaterParcel: a later parcel's arrival is recorded
// while goods remain outstanding.
func TestHandler_DeliverShipmentLaterParcel(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	role := admintest.AdminRolePool(t, pool)
	store := admintest.OrderStore(role, admintest.Refunder{}, nil, nil)
	mux := http.NewServeMux()
	admintest.OrderDesk(store).Routes(mux, admintest.BackOffice)

	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			localeCtx := i18n.WithLocale(ctx, locale)
			number, orderID, lines, _ := admintest.TwoLineOrderWithStock(t, pool, "later-arrival")
			ship := func(tracking string, line uuid.UUID) uuid.UUID {
				t.Helper()
				if err := store.Ship(localeCtx, number, orders.Dispatch{Carrier: "black_cat", Tracking: tracking,
					Lines: map[uuid.UUID]int32{line: 1}}, actor); err != nil {
					t.Fatal(err)
				}
				var id uuid.UUID
				if err := pool.QueryRow(localeCtx, `SELECT id FROM order_shipments WHERE order_id = $1 AND tracking_number = $2`, orderID, tracking).Scan(&id); err != nil {
					t.Fatal(err)
				}
				return id
			}
			first := ship("FIRST-"+number, lines[0])
			unarrived := readParcelArrivalState(t, localeCtx, orderID, first, first)
			refusal := postParcelArrival(localeCtx, mux, "/admin/orders/"+number+"/shipments/"+first.String()+"/delivered")
			if refusal.Code != http.StatusUnprocessableEntity || !strings.Contains(refusal.Body.String(), i18n.T(localeCtx, i18n.KeyAdminShipmentArrivalRefused)) {
				t.Fatalf("arrival before the order is delivered = %d, want a 422 refusal", refusal.Code)
			}
			if diff := cmp.Diff(unarrived, readParcelArrivalState(t, localeCtx, orderID, first, first)); diff != "" {
				t.Fatalf("refused arrival changed the parcel (-want +got):\n%s", diff)
			}
			if _, err := store.Advance(localeCtx, number, order.FulfillmentDelivered, actor); err != nil {
				t.Fatal(err)
			}
			second := ship("SECOND-"+number, lines[1])
			third := ship("THIRD-"+number, lines[0])
			// A dispatch clock ahead of the database cannot create an arrival
			// before dispatch; the per-parcel operation must keep the bulk rule.
			// Moved past order_shipments_moments_frozen, which holds every writer.
			if err := pgx.BeginFunc(localeCtx, pool, func(tx pgx.Tx) error {
				if _, err := tx.Exec(localeCtx, `SET LOCAL session_replication_role = replica`); err != nil {
					return err
				}
				_, err := tx.Exec(localeCtx, `UPDATE order_shipments SET shipped_at = now() + interval '1 minute' WHERE id = $1`, second)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			before := readParcelArrivalState(t, localeCtx, orderID, first, second)
			if !before.First.Valid || before.Second.Valid {
				t.Fatalf("arrival fixture = %+v, want only the first parcel delivered", before)
			}
			foreignNumber, foreignOrder, foreignLines, _ := admintest.TwoLineOrderWithStock(t, pool, "foreign-arrival")
			if err := store.Ship(localeCtx, foreignNumber, orders.Dispatch{Carrier: "black_cat", Tracking: "FOREIGN-" + foreignNumber,
				Lines: map[uuid.UUID]int32{foreignLines[0]: 1}}, actor); err != nil {
				t.Fatal(err)
			}
			var foreign uuid.UUID
			if err := pool.QueryRow(localeCtx, `SELECT id FROM order_shipments WHERE order_id = $1`, foreignOrder).Scan(&foreign); err != nil {
				t.Fatal(err)
			}
			foreignBefore := readParcelArrivalState(t, localeCtx, foreignOrder, foreign, foreign)
			for _, id := range []uuid.UUID{foreign, uuid.New()} {
				rec := postParcelArrival(localeCtx, mux, "/admin/orders/"+number+"/shipments/"+id.String()+"/delivered")
				if rec.Code != http.StatusUnprocessableEntity {
					t.Fatalf("unowned parcel arrival = %d, want 422", rec.Code)
				}
			}
			if diff := cmp.Diff(before, readParcelArrivalState(t, localeCtx, orderID, first, second)); diff != "" {
				t.Fatalf("unowned parcel changed target order (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(foreignBefore, readParcelArrivalState(t, localeCtx, foreignOrder, foreign, foreign)); diff != "" {
				t.Fatalf("unowned parcel changed other order (-want +got):\n%s", diff)
			}
			if err := store.DeliverShipment(t.Context(), number, second); !errors.Is(err, audit.ErrNoActor) {
				t.Fatalf("arrival without an audit actor = %v, want rollback", err)
			}
			if diff := cmp.Diff(before, readParcelArrivalState(t, localeCtx, orderID, first, second)); diff != "" {
				t.Fatalf("audit failure changed parcel arrival (-want +got):\n%s", diff)
			}
			path := "/admin/orders/" + number + "/shipments/" + second.String() + "/delivered"
			thirdPath := "/admin/orders/" + number + "/shipments/" + third.String() + "/delivered"
			page := httptest.NewRecorder()
			mux.ServeHTTP(page, httptest.NewRequestWithContext(localeCtx, http.MethodGet, "/admin/orders/"+number, nil))
			if page.Code != http.StatusOK || !containsPostAction(page.Body.String(), path) || !containsPostAction(page.Body.String(), thirdPath) {
				t.Fatalf("order GET = %d, want each unarrived parcel's own POST action", page.Code)
			}
			for range 2 {
				rec := postParcelArrival(localeCtx, mux, path)
				if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/orders/"+number+"?parceldelivered=1" {
					t.Fatalf("arrival POST = %d %q, want 303 to its order", rec.Code, rec.Header().Get("Location"))
				}
				got := readParcelArrivalState(t, localeCtx, orderID, first, second)
				if !got.Second.Valid || got.Second.Time.Before(got.SecondShipped) {
					t.Fatalf("second parcel arrival = %v, dispatched %v", got.Second, got.SecondShipped)
				}
				want := before
				if !want.Second.Valid {
					want.Second = got.Second
				}
				want.ArrivalAudits = 1
				if diff := cmp.Diff(want, got); diff != "" {
					t.Fatalf("arrival changed unrelated order facts (-want +got):\n%s", diff)
				}
				before = want
				var otherArrival pgtype.Timestamptz
				if err := pool.QueryRow(localeCtx, `SELECT delivered_at FROM order_shipments WHERE id = $1`, third).Scan(&otherArrival); err != nil {
					t.Fatal(err)
				}
				if otherArrival.Valid {
					t.Error("recording the second parcel also stamped another unarrived parcel")
				}
			}
			var auditActor uuid.UUID
			var entity, tracking string
			if err := pool.QueryRow(localeCtx, `SELECT actor_user_id, entity_table, after->>'tracking'
                FROM audit_events WHERE entity_id = $1 AND action = $2`, second, string(audit.ActionDeliverShipment)).Scan(&auditActor, &entity, &tracking); err != nil {
				t.Fatal(err)
			}
			if auditActor != staff || entity != "order_shipments" || tracking != "SECOND-"+number {
				t.Errorf("arrival audit actor/entity/tracking = %s/%s/%s", auditActor, entity, tracking)
			}
			view, err := store.Order(localeCtx, number)
			if err != nil || !view.CanShip || view.Status != order.FulfillmentDelivered {
				t.Fatalf("order after later arrival = status %s, CanShip %t, error %v", view.Status, view.CanShip, err)
			}
			if _, err := store.Advance(localeCtx, number, order.FulfillmentCompleted, actor); !errors.Is(err, orders.ErrRefused) {
				t.Fatalf("completion with goods outstanding = %v, want refusal", err)
			}
			page = httptest.NewRecorder()
			mux.ServeHTTP(page, httptest.NewRequestWithContext(localeCtx, http.MethodGet, "/admin/orders/"+number, nil))
			if containsPostAction(page.Body.String(), path) || !containsPostAction(page.Body.String(), thirdPath) {
				t.Error("arrival actions no longer match the individual parcel stamps")
			}
		})
	}
}

type parcelArrivalState struct {
	First, Second                       pgtype.Timestamptz
	SecondShipped                       time.Time
	Status                              string
	OrderEvents, Notices, ArrivalAudits int
}

func readParcelArrivalState(t *testing.T, ctx context.Context, orderID, first, second uuid.UUID) parcelArrivalState {
	t.Helper()
	var state parcelArrivalState
	if err := pool.QueryRow(ctx, `SELECT
        (SELECT delivered_at FROM order_shipments WHERE id = $2),
        (SELECT delivered_at FROM order_shipments WHERE id = $3),
        (SELECT shipped_at FROM order_shipments WHERE id = $3),
        o.fulfillment_status,
        (SELECT count(*) FROM order_events WHERE order_id = o.id),
        (SELECT count(*) FROM outbox_messages WHERE payload->>'order_number' = o.order_number OR payload->>'order_id' = o.id::text),
        (SELECT count(*) FROM audit_events WHERE action = 'shipment.delivered' AND entity_id IN ($2, $3))
        FROM orders o WHERE o.id = $1`, orderID, first, second).Scan(&state.First, &state.Second, &state.SecondShipped, &state.Status,
		&state.OrderEvents, &state.Notices, &state.ArrivalAudits); err != nil {
		t.Fatal(err)
	}
	return state
}

func postParcelArrival(ctx context.Context, mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, path, http.NoBody)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func containsPostAction(page, action string) bool {
	for _, form := range strings.Split(page, "<form")[1:] {
		opening, _, _ := strings.Cut(form, ">")
		if strings.Contains(opening, `method="post"`) && strings.Contains(opening, `action="`+action+`"`) {
			return true
		}
	}
	return false
}
