//go:build integration

package orders_test

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/orders"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/buyer"
	"github.com/koopa0/goen/internal/shipping"
)

type shippedEvent struct {
	ShipmentID uuid.NullUUID
	Note       pgtype.Text
}

func shippedEvents(t *testing.T, orderID uuid.UUID) []shippedEvent {
	t.Helper()
	rows, err := pool.Query(t.Context(), `
		SELECT shipment_id, note FROM order_events
		WHERE order_id = $1 AND kind = 'shipped' ORDER BY occurred_at, id`, orderID)
	if err != nil {
		t.Fatalf("read shipped events: %v", err)
	}
	defer rows.Close()
	var events []shippedEvent
	for rows.Next() {
		var e shippedEvent
		if err := rows.Scan(&e.ShipmentID, &e.Note); err != nil {
			t.Fatalf("scan shipped event: %v", err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read shipped events: %v", err)
	}
	return events
}

// TestStore_ShipEventNamesParcel: a shipped event names its parcel and carries
// no note. A shipped event names its parcel and carries no prose: the carrier
// and the tracking number are the parcel's, and each page writes them in its
// reader's language. The store ships as the admin role, which is the role that
// must be allowed to write the new column; the suite's owner pool would not
// notice.
func TestStore_ShipEventNamesParcel(t *testing.T) {
	ctx, staff := admintest.StaffContext(t, pool)
	ctx = i18n.WithLocale(ctx, i18n.En)
	actor := uuid.NullUUID{UUID: staff, Valid: true}
	store := admintest.OrderStore(admintest.AdminRolePool(t, pool), admintest.HealthyGateway(t), nil, nil)
	number, orderID, lines, _ := admintest.TwoLineOrderWithStock(t, pool, "shipped-event")

	parcel := func(tracking string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := pool.QueryRow(ctx,
			`SELECT id FROM order_shipments WHERE order_id = $1 AND tracking_number = $2`, orderID, tracking).Scan(&id); err != nil {
			t.Fatalf("read the parcel %s: %v", tracking, err)
		}
		return id
	}
	ship := func(tracking string, line uuid.UUID) uuid.UUID {
		t.Helper()
		if err := store.Ship(ctx, number, orders.Dispatch{
			Carrier: string(shipping.CarrierBlackCat), Tracking: tracking, Lines: map[uuid.UUID]int32{line: 1},
		}, actor); err != nil {
			t.Fatalf("ship %s: %v", tracking, err)
		}
		return parcel(tracking)
	}

	first := ship("FIRST-"+number, lines[0])
	events := shippedEvents(t, orderID)
	if len(events) != 1 {
		t.Fatalf("after one parcel the order has %d shipped events, want 1", len(events))
	}
	if events[0].ShipmentID != (uuid.NullUUID{UUID: first, Valid: true}) {
		t.Errorf("the shipped event names parcel %v, want %s", events[0].ShipmentID, first)
	}
	if events[0].Note.Valid {
		t.Errorf("the shipped event carries the note %q, want none", events[0].Note.String)
	}

	second := ship("SECOND-"+number, lines[1])
	events = shippedEvents(t, orderID)
	named := make([]uuid.UUID, 0, len(events))
	for _, e := range events {
		if e.Note.Valid {
			t.Errorf("a shipped event carries the note %q, want none", e.Note.String)
		}
		named = append(named, e.ShipmentID.UUID)
	}
	slices.SortFunc(named, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	want := []uuid.UUID{first, second}
	slices.SortFunc(want, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	if !slices.Equal(named, want) {
		t.Fatalf("shipped events name parcels %v, want one event for each of %v", named, want)
	}

	t.Run("the customer's page", func(t *testing.T) {
		view, err := buyer.NewStore(pool).Order(ctx, number)
		if err != nil {
			t.Fatalf("read the customer's order: %v", err)
		}
		var page bytes.Buffer
		if err := buyer.Order(buyer.OrderMeta(ctx, number), &view).Render(ctx, &page); err != nil {
			t.Fatalf("render the customer's order: %v", err)
		}
		assertEnglishTimeline(t, page.String(), `goen-order__history`, `</section>`)
		if !strings.Contains(page.String(), shipping.CarrierBlackCat.Name(ctx)) {
			t.Errorf("the parcel list does not name the carrier %q", shipping.CarrierBlackCat.Name(ctx))
		}
	})

	t.Run("the back office page", func(t *testing.T) {
		mux := http.NewServeMux()
		admintest.OrderDesk(store).Routes(mux, admintest.BackOffice)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/orders/"+number, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("the order page answered %d, want 200", rec.Code)
		}
		page := rec.Body.String()
		assertEnglishTimeline(t, page, i18n.T(ctx, i18n.KeyAdminQueueTimeline), `</ul>`)
		if !strings.Contains(page, shipping.CarrierBlackCat.Name(ctx)+" · FIRST-"+number) {
			t.Errorf("the parcel list does not read %q", shipping.CarrierBlackCat.Name(ctx)+" · FIRST-"+number)
		}
	})

	t.Run("another order's parcel", func(t *testing.T) {
		_, otherID, _, _ := admintest.TwoLineOrderWithStock(t, pool, "shipped-event-other")
		// A parcel with no event of its own, so the unique index has nothing to say first.
		var bare uuid.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO order_shipments (order_id, carrier, tracking_number) VALUES ($1, 'black_cat', $2) RETURNING id`,
			orderID, "BARE-"+number).Scan(&bare); err != nil {
			t.Fatalf("record a parcel without an event: %v", err)
		}
		_, err := pool.Exec(ctx, `INSERT INTO order_events (order_id, kind, shipment_id) VALUES ($1, 'shipped', $2)`, otherID, bare)
		fault, ok := errors.AsType[*pgconn.PgError](err)
		if !ok || fault.Code != "23503" || fault.ConstraintName != "order_events_shipment_fk" {
			t.Fatalf("a shipped event naming another order's parcel gave %v, want 23503 on order_events_shipment_fk", err)
		}
	})
}

var han = regexp.MustCompile(`\p{Han}`)

// assertEnglishTimeline holds the part of page from start to the next end: it
// must be there, name the shipped event, and contain no Han character.
func assertEnglishTimeline(t *testing.T, page, start, end string) {
	t.Helper()
	_, after, found := strings.Cut(page, start)
	if !found {
		t.Fatalf("the page has no %q section", start)
	}
	section, _, _ := strings.Cut(after, end)
	if !strings.Contains(section, i18n.T(i18n.WithLocale(t.Context(), i18n.En), i18n.KeyEventShipped)) {
		t.Errorf("the timeline does not show the shipped event:\n%s", section)
	}
	if found := han.FindAllString(section, -1); len(found) > 0 {
		t.Errorf("the English timeline carries Han text %v:\n%s", found, section)
	}
}
