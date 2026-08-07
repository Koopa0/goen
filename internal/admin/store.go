package admin

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/ui/pages"
)

// Store reads and writes through the ADMIN pool, which assumes the admin role.
type Store struct {
	pool     *pgxpool.Pool
	q        *db.Queries
	refunder Refunder
}

// NewStore returns a Store over the admin pool.
//
// refunder may be a Refunder that refuses: a back office without Stripe
// credentials can still approve and reject returns, and a refund it cannot pay
// fails loudly rather than marking money returned that never moved.
func NewStore(pool *pgxpool.Pool, refunder Refunder) *Store {
	if pool == nil || refunder == nil {
		panic("admin: NewStore requires a pool and a refunder")
	}
	return &Store{pool: pool, q: db.New(pool), refunder: refunder}
}

// Dashboard reads the back office landing page.
func (s *Store) Dashboard(ctx context.Context) (pages.AdminDashboardView, error) {
	sum, err := s.q.AdminSummary(ctx)
	if err != nil {
		return pages.AdminDashboardView{}, fmt.Errorf("read summary: %w", err)
	}
	view := pages.AdminDashboardView{
		PendingOrders:  sum.PendingOrders,
		PickingOrders:  sum.PickingOrders,
		LowStock:       sum.LowStock,
		ActiveProducts: sum.ActiveProducts,
		OpenMessages:   sum.OpenMessages,
	}

	low, err := s.q.AdminVariants(ctx, db.AdminVariantsParams{LowOnly: true, RowLimit: 10})
	if err != nil {
		return pages.AdminDashboardView{}, fmt.Errorf("read low stock: %w", err)
	}
	for i := range low {
		view.Low = append(view.Low, variantRow(&low[i]))
	}
	return view, nil
}

// Orders reads the order queue.
func (s *Store) Orders(ctx context.Context, status, term string) (pages.AdminOrdersView, error) {
	term = strings.TrimSpace(term)
	searched := len([]rune(term)) >= MinSearchRunes
	var rows []db.AdminOrdersRow
	var err error
	if searched {
		// A search ignores the status filter. Somebody on the phone to a customer
		// wants that order, not that order if it happens to be in the tab they had
		// open — and the number they were given is unique.
		var found []db.AdminSearchOrdersRow
		if found, err = s.q.AdminSearchOrders(ctx, db.AdminSearchOrdersParams{
			Term: term, RowLimit: PageSize,
		}); err == nil {
			rows = make([]db.AdminOrdersRow, 0, len(found))
			for i := range found {
				f := &found[i]
				rows = append(rows, db.AdminOrdersRow{
					ID: f.ID, OrderNumber: f.OrderNumber,
					FulfillmentStatus: f.FulfillmentStatus, PlacedAt: f.PlacedAt,
					ShippingCents: f.ShippingCents, DiscountCents: f.DiscountCents,
					TaxCents: f.TaxCents, Recipient: f.Recipient,
					SubtotalCents: f.SubtotalCents, Committed: f.Committed,
				})
			}
		}
	} else {
		rows, err = s.q.AdminOrders(ctx, db.AdminOrdersParams{Status: status, RowLimit: PageSize})
	}
	if err != nil {
		return pages.AdminOrdersView{}, fmt.Errorf("read orders: %w", err)
	}
	counts, err := s.q.AdminOrderCounts(ctx)
	if err != nil {
		return pages.AdminOrdersView{}, fmt.Errorf("read order counts: %w", err)
	}

	view := pages.AdminOrdersView{
		Status: status, Term: term, Searched: searched, Counts: map[string]int64{},
	}
	for _, c := range counts {
		view.Counts[c.FulfillmentStatus] = c.N
	}
	for i := range rows {
		o := &rows[i]
		view.Orders = append(view.Orders, pages.AdminOrderRow{
			Number:     o.OrderNumber,
			Status:     o.FulfillmentStatus,
			StatusText: StatusLabel(o.FulfillmentStatus),
			PlacedAt:   o.PlacedAt.Format("2006-01-02 15:04"),
			Recipient:  o.Recipient,
			TotalCents: o.SubtotalCents - o.DiscountCents + o.ShippingCents + o.TaxCents,
			Committed:  o.Committed,
		})
	}
	return view, nil
}

// Order reads one order for the back office, with the delivery details a
// storefront confirmation does not show.
func (s *Store) Order(ctx context.Context, number string) (pages.AdminOrderView, error) {
	o, err := s.q.AdminOrderByNumber(ctx, number)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pages.AdminOrderView{}, ErrNotFound
		}
		return pages.AdminOrderView{}, fmt.Errorf("read order: %w", err)
	}
	lines, err := s.q.OrderLinesByOrder(ctx, o.ID)
	if err != nil {
		return pages.AdminOrderView{}, fmt.Errorf("read order lines: %w", err)
	}

	view := pages.AdminOrderView{
		Number: o.OrderNumber, Status: o.FulfillmentStatus,
		StatusText:    StatusLabel(o.FulfillmentStatus),
		PlacedAt:      o.PlacedAt.Format("2006-01-02 15:04"),
		ShippingName:  o.ShippingMethodName,
		SubtotalCents: o.SubtotalCents, ShippingCents: o.ShippingCents,
		DiscountCents: o.DiscountCents, DiscountReason: o.DiscountReason, TaxCents: o.TaxCents,
		Email: o.Email, Recipient: o.RecipientName, Phone: o.Phone,
		Address: pages.Delivery{
			PostalCode: o.PostalCode, City: o.City, District: o.District, Street: o.Street,
			PickupBrand: o.PickupBrand, PickupStoreCode: o.PickupStoreCode,
			PickupStoreName: o.PickupStoreName,
		}.Line(),
		Delivery: pages.AdminDelivery{
			Email: o.Email, Recipient: o.RecipientName, Phone: o.Phone,
			PostalCode: o.PostalCode, City: o.City,
			District: o.District, Street: o.Street,
			PickupBrand: o.PickupBrand, PickupStoreCode: o.PickupStoreCode,
			PickupStoreName: o.PickupStoreName,
		},
		// Correctable until the parcel leaves. The database says the same thing
		// in UpdateOrderDelivery's WHERE clause; this only decides whether to
		// offer the form, because a control that can only be refused is worse
		// than no control.
		Correctable: o.FulfillmentStatus != "shipped" &&
			o.FulfillmentStatus != "delivered" && o.FulfillmentStatus != "completed",
		PickupDestination: o.PickupStoreCode != "",
		PickupBrands:      pages.PickupBrandChoices(),
		CustomerNote:      o.CustomerNote.String,
		StaffNote:         o.StaffNote.String,
		InvoiceType:       o.InvoiceType,
		InvoiceCarrier:    o.InvoiceCarrier,
		InvoiceTaxID:      o.InvoiceTaxID,
		Committed:         o.Committed,
		CanShip:           o.FulfillmentStatus == "picking",
	}
	for _, n := range NextStatuses(o.FulfillmentStatus) {
		view.Next = append(view.Next, pages.AdminTransition{Value: n, Label: StatusLabel(n)})
	}
	for _, l := range lines {
		view.Lines = append(view.Lines, pages.OrderLine{
			SKU: l.SKU, Name: l.ProductName, Label: l.VariantLabel.String,
			UnitCents: l.UnitPriceCents, Quantity: l.Quantity,
		})
	}

	// The history, with WHO. Both queries existed and neither was called: the
	// customer sees a timeline of their own order and the shop saw none of it,
	// which is backwards — the actor is the whole reason the back office's
	// version is a different query.
	events, err := s.q.OrderEvents(ctx, o.ID)
	if err != nil {
		return pages.AdminOrderView{}, fmt.Errorf("read order events: %w", err)
	}
	for i := range events {
		e := &events[i]
		view.Timeline = append(view.Timeline, pages.AdminOrderEvent{
			Kind: e.Kind, Note: e.Note.String,
			At: e.OccurredAt.Format("2006-01-02 15:04"), Actor: e.ActorName,
		})
	}

	shipments, err := s.q.OrderShipments(ctx, o.ID)
	if err != nil {
		return pages.AdminOrderView{}, fmt.Errorf("read order shipments: %w", err)
	}
	for i := range shipments {
		sh := &shipments[i]
		view.Shipments = append(view.Shipments, pages.AdminShipment{
			Carrier: sh.Carrier, Tracking: sh.TrackingNumber,
			ShippedAt:   sh.ShippedAt.Format("2006-01-02 15:04"),
			DeliveredAt: nullableStamp(sh.DeliveredAt),
		})
	}
	return view, nil
}

// Advance moves an order along its lifecycle.
//
// The transition is validated by orders_check_transition, which knows the state
// machine and refuses an illegal move — including one that would ship an
// unfunded order. A refusal here is that guard talking, and its message names
// the rule, so it is returned rather than replaced.
//
// The Checkout Sessions it returns are a CANCELLATION's, and the caller closes
// them at Stripe once this has committed — the same contract cart.Store.Cancel
// carries, because the shop cancelling on a customer's behalf must not be the
// door that leaves their checkout open. Every other status returns none: only a
// cancellation makes a live session wrong.
func (s *Store) Advance(ctx context.Context, number, status string, actor uuid.NullUUID) ([]string, error) {
	if ParseStatus(status) == "" {
		return nil, ErrRefused
	}
	// Dispatch does not happen here. Ship is the only door to 'shipped' because
	// it also records the carrier and settles the held stock, and leaving this
	// path open would let a hand-written POST to the status endpoint produce a
	// shipped order with neither — which is exactly the state the ship form was
	// built to make impossible.
	if status == "shipped" {
		return nil, ErrRefused
	}

	// The status change and its history entry are one transaction. Two writes
	// would let a status move land with no record of who moved it or when —
	// which is the whole thing order_events exists to prevent, and the table is
	// append-only precisely so the answer cannot be edited afterwards.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin advance: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	row, err := q.OrderIDByNumber(ctx, number)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrRefused, err.Error())
	}
	// Read BEFORE the status moves: afterwards the reservation is still 'held',
	// but reading first keeps the set the loop walks the one this transaction
	// decided on.
	var held []uuid.UUID
	if status == "cancelled" {
		if held, err = q.HeldReservationsForOrder(ctx, number); err != nil {
			return nil, fmt.Errorf("read holds of %s: %w", number, err)
		}
	}
	if err := q.AdvanceOrder(ctx, db.AdvanceOrderParams{
		OrderNumber: number, Status: status,
	}); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrRefused, err.Error())
	}
	if err := applyStatusEffects(ctx, q, statusEffect{
		status: status, number: number, orderID: row.ID, held: held,
	}); err != nil {
		return nil, err
	}
	if err := q.RecordOrderEvent(ctx, db.RecordOrderEventParams{
		OrderID: row.ID, Kind: eventKindFor(status), ActorUserID: actor,
	}); err != nil {
		return nil, fmt.Errorf("record order event: %w", err)
	}
	// order_events already says the order moved. This says which staff member
	// moved it and from which request — the question that spans orders.
	if err := auditIn(ctx, q, Event{
		Action: ActionAdvanceOrder, Table: "orders", ID: nullableID(row.ID),
		Before: nil, After: map[string]any{"number": number, "status": status},
	}); err != nil {
		return nil, err
	}
	// Read inside the transaction, last, for the reason cart.Store.Cancel reads
	// it there: the set handed back is the one this transaction saw, not what a
	// later read finds after a webhook has moved a row.
	var sessions []string
	if status == "cancelled" {
		var sessErr error
		if sessions, sessErr = q.OpenSessionsForOrder(ctx, number); sessErr != nil {
			return nil, fmt.Errorf("read open checkout sessions of %s: %w", number, sessErr)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit advance: %w", err)
	}
	return sessions, nil
}

// statusEffect is one status move and what it has to reach.
type statusEffect struct {
	status  string
	number  string
	orderID uuid.UUID
	// held is the order's live reservations, read before the status moved.
	held []uuid.UUID
}

// applyStatusEffects does what a status move MEANS, beyond the column.
//
// Separate from Advance because each of these was added later and each was
// missing when it was: the stock release (a cancelled order left the shelf short
// until the sweeper noticed, and for a funded one it never did), the credit
// reversal (the goods came back and the customer's money stayed spent), and the
// parcel stamp (delivered_at was read by two pages and written by nothing). They
// run in the CALLER's transaction, because a status that says one thing while the
// stock, the money or the parcel says another is one fact recorded twice and
// disagreeing.
func applyStatusEffects(ctx context.Context, q *db.Queries, e statusEffect) error {
	for _, id := range e.held {
		if err := q.ReleaseReservation(ctx, id); err != nil {
			return fmt.Errorf("release hold %s of %s: %w", id, e.number, err)
		}
	}
	switch e.status {
	case "cancelled":
		// The customer's own cancellation does this too; the back office
		// cancelling on their behalf must not be the path that keeps their credit.
		if _, err := q.ReverseOrderCredit(ctx, e.orderID); err != nil {
			return fmt.Errorf("return store credit spent on %s: %w", e.number, err)
		}
	case "delivered", "completed":
		// A delivered ORDER means its parcels arrived, and the customer's page
		// reads the parcel.
		//
		// 'completed' is here because orders_legal_transition permits shipped ->
		// completed DIRECTLY, and that is the ordinary click for 超商取貨: the shop
		// has no delivery event to record for a parcel the customer walks in and
		// collects, so 已完成 is the only move it can honestly make. Stamping only
		// on 'delivered' left those parcels unstamped forever — mistake #17, a
		// guard naming one shape of a thing that has two, and the fix for the
		// unwritten delivered_at closing one of its two transitions.
		//
		// What it costs is not cosmetic. /admin/returns reads
		// max(delivered_at) to decide whether a request is inside 消保法 §19's
		// seven days, and an unstamped parcel renders as 尚未送達 — "the window
		// has not started" — for goods that demonstrably arrived. The one screen
		// built to inform an unwaivable-right decision was misinforming it, in
		// the shop's favour.
		//
		// Idempotent by the query's own WHERE: coming here from 'delivered'
		// finds nothing left to stamp.
		if err := q.MarkShipmentsDelivered(ctx, e.orderID); err != nil {
			return fmt.Errorf("mark parcels of %s delivered: %w", e.number, err)
		}
	}
	return nil
}

// eventKindFor maps a fulfilment status to its history entry.
//
// The two vocabularies overlap but are not the same list: order_events also
// carries 'placed', 'paid', 'in_transit' and 'refunded', which are not
// fulfilment states. A status with no matching kind is a programming error —
// ParseStatus has already refused anything outside the closed set — so this
// panics rather than silently writing a wrong history.
func eventKindFor(status string) string {
	switch status {
	case "picking":
		return "picking"
	case "shipped":
		return "shipped"
	case "delivered":
		return "delivered"
	case "completed":
		return "completed"
	case "cancelled":
		return "cancelled"
	default:
		panic("admin: no order_events kind for fulfilment status " + status)
	}
}

// Ship records a dispatch: the shipment, the status move, the stock the order
// was holding, and the history entry.
//
// All four in ONE transaction, because every pair of them is wrong on its own:
// a shipment row without the status move is an order that shows as picking with
// a tracking number; the status move without consuming the reservations leaves
// stock held against an order that has already gone out, so a sweeper would
// later return it to the shelf and oversell; and either without the event
// leaves no record of who dispatched it.
func (s *Store) Ship(ctx context.Context, number, carrier, tracking string, actor uuid.NullUUID) error {
	carrier, tracking = strings.TrimSpace(carrier), strings.TrimSpace(tracking)
	if carrier == "" || tracking == "" {
		return ErrInvalid
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin ship: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	row, err := q.OrderIDByNumber(ctx, number)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrRefused, err.Error())
	}

	shipmentID, shipErr := q.CreateShipment(ctx, db.CreateShipmentParams{
		OrderID: row.ID, Carrier: carrier, TrackingNumber: tracking,
	})
	if shipErr != nil {
		return fmt.Errorf("record shipment: %w", shipErr)
	}

	if linesErr := writeShipmentLines(ctx, q, row.ID, shipmentID, number); linesErr != nil {
		return linesErr
	}

	// The status move is what orders_legal_transition guards: only picking may
	// become shipped, and the whole transaction fails if this order is not there
	// yet. That refusal is the point — it is what stops a shipment being
	// recorded against an order nobody has picked.
	if advErr := q.AdvanceOrder(ctx, db.AdvanceOrderParams{
		OrderNumber: number, Status: "shipped",
	}); advErr != nil {
		return fmt.Errorf("%w: %s", ErrRefused, advErr.Error())
	}

	if err := settleHeldStock(ctx, q, row.ID, number); err != nil {
		return err
	}

	if err := enqueueOrderShipped(ctx, q, row.ID, &OrderShipped{
		OrderNumber: number, Carrier: carrier, Tracking: tracking,
	}); err != nil {
		return err
	}

	if err := q.RecordOrderEvent(ctx, db.RecordOrderEventParams{
		OrderID: row.ID, Kind: "shipped", ActorUserID: actor,
		Note: text(carrier + " " + tracking),
	}); err != nil {
		return fmt.Errorf("record order event: %w", err)
	}
	if err := auditIn(ctx, q, Event{
		Action: ActionShipOrder, Table: "orders", ID: nullableID(row.ID),
		Before: nil, After: map[string]any{"carrier": carrier, "tracking": tracking},
	}); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit ship: %w", err)
	}
	return nil
}

// settleHeldStock consumes the reservations a dispatch is taking off the shelf.
//
// Nothing held is not "nothing to do" — it is stock that went BACK on the shelf
// under an order about to leave the warehouse. Every line takes a hold in
// PlaceOrder's own transaction, so an empty set here means somebody released
// them, and shipping anyway posts no inventory movement at all: the parcel goes
// out and stock_quantity stays where it was, over-stating the shelf by exactly
// this order forever and overselling the next customer. Ranging over an empty
// slice made all of that silent.
func settleHeldStock(ctx context.Context, q *db.Queries, orderID uuid.UUID, number string) error {
	held, err := q.HeldReservations(ctx, orderID)
	if err != nil {
		return fmt.Errorf("read held reservations: %w", err)
	}
	if len(held) == 0 {
		return fmt.Errorf("%w: order %s holds no stock; its reservations were "+
			"released and shipping would not decrement anything", ErrRefused, number)
	}
	for _, id := range held {
		if consumeErr := q.ConsumeReservation(ctx, id); consumeErr != nil {
			return fmt.Errorf("consume reservation %s: %w", id, consumeErr)
		}
	}
	return nil
}

// SetStaffNote records an internal note. staff_note is internal by design —
// erase_user clears customer_note and leaves this, so it must never be used for
// anything the customer wrote.
func (s *Store) SetStaffNote(ctx context.Context, number, note string) error {
	if err := s.q.SetStaffNote(ctx, db.SetStaffNoteParams{
		OrderNumber: number, StaffNote: text(note),
	}); err != nil {
		return fmt.Errorf("set staff note: %w", err)
	}
	return nil
}

// Variants reads the stock list.
func (s *Store) Variants(ctx context.Context, lowOnly bool) (pages.AdminVariantsView, error) {
	rows, err := s.q.AdminVariants(ctx, db.AdminVariantsParams{LowOnly: lowOnly, RowLimit: PageSize})
	if err != nil {
		return pages.AdminVariantsView{}, fmt.Errorf("read variants: %w", err)
	}
	view := pages.AdminVariantsView{LowOnly: lowOnly}
	for i := range rows {
		view.Variants = append(view.Variants, variantRow(&rows[i]))
	}
	return view, nil
}

// AdjustStock moves stock through the ledger.
//
// record_inventory_movement is the only door: admin holds no UPDATE on
// stock_quantity, so a direct write is refused by the DATABASE rather than by
// this function remembering to route around it. Every adjustment therefore has
// a movement row with a reason and an actor behind it.
//
// The idempotency key is the caller's, so a resubmitted form is one adjustment
// rather than two — inventory_movements has a unique index on it.
func (s *Store) AdjustStock(ctx context.Context, sku string, delta int32, actorID, key string) error {
	v, err := s.q.AdminVariantBySKU(ctx, sku)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("read variant: %w", err)
	}
	// RequireStaff guarantees a signed-in user reached this, so the actor is
	// always known. An unparseable id is a wiring mistake, not a runtime state,
	// and recording an adjustment with no actor would leave the ledger unable to
	// say who moved the stock.
	actor, err := uuid.Parse(actorID)
	if err != nil {
		return fmt.Errorf("adjust stock: actor %q is not a user id: %w", actorID, err)
	}
	return s.audited(ctx, Event{
		Action: ActionAdjustStock, Table: "product_variants", ID: nullableID(v.ID),
		Before: map[string]any{"sku": sku, "stock": v.StockQuantity},
		After:  map[string]any{"delta": delta},
	},
		func(ctx context.Context, q *db.Queries) error {
			if err := q.AdjustStock(ctx, db.AdjustStockParams{
				VariantID: v.ID, Delta: delta, IdempotencyKey: key, ActorUserID: actor,
			}); err != nil {
				return fmt.Errorf("%w: %s", ErrRefused, err.Error())
			}
			// Called on EVERY adjustment, not only the ones somebody labels a
			// restock. The claim's own EXISTS decides whether the variant is
			// back above its threshold, so a movement that does not cross it
			// claims nothing — and a rule written here instead would be a rule
			// the next stock path forgets.
			return enqueueRestockNotices(ctx, q, v.ID)
		})
}

// SetVariantActive retires or restores a variant.
//
// A refusal here is sale_campaign_variant_still_valid: deactivating the last
// discounted variant of a product a campaign features would leave the campaign
// pointing at nothing marked down.
func (s *Store) SetVariantActive(ctx context.Context, sku string, active bool) error {
	v, err := s.q.AdminVariantBySKU(ctx, sku)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("read variant: %w", err)
	}
	return s.audited(ctx, Event{
		Action: ActionRetireVariant, Table: "product_variants", ID: nullableID(v.ID),
		Before: map[string]any{"sku": sku, "active": v.IsActive},
		After:  map[string]any{"active": active},
	},
		func(ctx context.Context, q *db.Queries) error {
			if err := q.SetVariantActive(ctx, db.SetVariantActiveParams{
				ID: v.ID, IsActive: active,
			}); err != nil {
				return fmt.Errorf("%w: %s", ErrRefused, err.Error())
			}
			return nil
		})
}

// SetVariantPrice repriced a variant. product_variants_compare_at_is_higher
// refuses a compare-at price that is not above the price, so a "sale" that is
// not a saving cannot be written.
func (s *Store) SetVariantPrice(ctx context.Context, sku string, price, compareAt int64) error {
	v, err := s.q.AdminVariantBySKU(ctx, sku)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("read variant: %w", err)
	}
	var cmp pgtype.Int8
	if compareAt > 0 {
		cmp = pgtype.Int8{Int64: compareAt, Valid: true}
	}
	return s.audited(ctx, Event{
		Action: ActionRepriceVariant, Table: "product_variants", ID: nullableID(v.ID),
		Before: map[string]any{"sku": sku, "price_cents": v.PriceCents},
		After:  map[string]any{"price_cents": price, "compare_at_cents": compareAt},
	},
		func(ctx context.Context, q *db.Queries) error {
			if err := q.SetVariantPrice(ctx, db.SetVariantPriceParams{
				ID: v.ID, PriceCents: price, CompareAtPriceCents: cmp,
			}); err != nil {
				return fmt.Errorf("%w: %s", ErrRefused, err.Error())
			}
			return nil
		})
}

func variantRow(r *db.AdminVariantsRow) pages.AdminVariant {
	return pages.AdminVariant{
		SKU: r.SKU, Slug: r.Slug, ProductName: r.ProductName, Brand: r.Brand,
		PriceCents: r.PriceCents, CompareCents: r.CompareAtPriceCents.Int64,
		Stock: r.StockQuantity, Safety: r.SafetyStock,
		Active: r.IsActive, ProductStatus: r.ProductStatus,
	}
}

func text(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

// writeShipmentLines records what is in the parcel.
//
// A shipment with no lines is a dispatch nothing can be reconciled against:
// return_within_shipment bounds a return by what actually went out, so with no
// lines recorded that ceiling is zero and no return of this order is ever
// possible. An order with nothing left to ship is refused rather than given an
// empty shipment.
func writeShipmentLines(ctx context.Context, q *db.Queries, orderID, shipmentID uuid.UUID, number string) error {
	remaining, err := q.UnshippedLines(ctx, orderID)
	if err != nil {
		return fmt.Errorf("read unshipped lines: %w", err)
	}
	if len(remaining) == 0 {
		return fmt.Errorf("%w: order %s has nothing left to ship", ErrRefused, number)
	}
	for _, l := range remaining {
		if lineErr := q.CreateShipmentLine(ctx, db.CreateShipmentLineParams{
			OrderID: orderID, ShipmentID: shipmentID,
			OrderLineID: l.ID, Quantity: l.Remaining,
		}); lineErr != nil {
			return fmt.Errorf("record shipment line: %w", lineErr)
		}
	}
	return nil
}

// Returns reads the back-office queue.
func (s *Store) Returns(ctx context.Context) (pages.AdminReturnsView, error) {
	rows, err := s.q.ReturnQueue(ctx, PageSize)
	if err != nil {
		return pages.AdminReturnsView{}, fmt.Errorf("read return queue: %w", err)
	}
	ids := make([]uuid.UUID, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].ID)
	}
	// ONE query for every request on the page. Read per row it would be fifty
	// round trips to fill one screen, which is the shape a queue page has to
	// avoid rather than measure.
	lines, err := s.q.ReturnLines(ctx, ids)
	if err != nil {
		return pages.AdminReturnsView{}, fmt.Errorf("read return lines: %w", err)
	}
	byRequest := make(map[uuid.UUID][]pages.AdminReturnLine, len(rows))
	for i := range lines {
		l := &lines[i]
		byRequest[l.ReturnRequestID] = append(byRequest[l.ReturnRequestID], pages.AdminReturnLine{
			SKU: l.SKU, Name: l.ProductName, Label: l.VariantLabel.String,
			UnitCents: l.UnitPriceCents, Quantity: l.Quantity,
			OrderLineID: l.OrderLineID.String(),
			// Inspected is the presence of the figure, never a zero: a line
			// somebody opened and found empty reads 0 received, which is a
			// finding, and a line nobody has opened reads NULL, which is work.
			Inspected:   l.ReceivedQuantity.Valid,
			Received:    l.ReceivedQuantity.Int32,
			Restocked:   l.RestockedQuantity.Int32,
			Note:        l.InspectionNote,
			Restockable: l.Restockable,
		})
	}

	view := pages.AdminReturnsView{}
	for i := range rows {
		r := &rows[i]
		view.Rows = append(view.Rows, pages.AdminReturn{
			ID:          r.ID.String(),
			OrderNumber: r.OrderNumber,
			Status:      r.Status,
			StatusText:  ReturnStatusLabel(r.Status),
			Reason:      r.Reason,
			Units:       r.Units,
			AmountCents: r.RefundableCents,
			CreatedAt:   r.CreatedAt.Format("2006-01-02 15:04"),
			Decided:     r.Status != "requested",
			Lines:       byRequest[r.ID],
			Window:      r.RescissionWindow,
		})
	}
	return view, nil
}

// Decide approves or rejects a return, and pays the money back when it approves.
//
// # Why two transactions
//
// The refund row is committed BEFORE Stripe is called, and settled after. A row
// written only on success is a refund that succeeded at the provider and exists
// nowhere in goen — the state nothing can reconcile. request_key exists for
// exactly this window: it is goen's own key, sent to Stripe as the idempotency
// key, so the retry that follows a crash is one refund at Stripe and one row
// here.
//
// Both halves of that were once only a claim. The retry was refused before it
// reached Stripe, because the arithmetic counted the row THIS return had just
// written as somebody else's claim on the capture (see splitRefund), and the
// row nothing could resume was also a row nothing READ — /admin/health lists
// them now. A design that leaves something for reconciliation to find has to be
// checked from reconciliation's end, not only from the writer's.
//
// A rejection touches no money and is one transaction.
func (s *Store) Decide(ctx context.Context, id, decision, resolution string, actor uuid.NullUUID) error {
	requestID, err := uuid.Parse(id)
	if err != nil {
		return ErrRefused
	}
	if decision != "approved" && decision != "rejected" {
		return ErrRefused
	}

	row, err := s.q.ReturnForDecision(ctx, requestID)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrRefused, err.Error())
	}
	if row.Status != "requested" {
		return fmt.Errorf("%w: return %s is already %s", ErrRefused, id, row.Status)
	}

	if decision == "rejected" {
		return s.closeReturn(ctx, requestID, row.OrderID, "rejected", resolution, "", actor)
	}

	if row.RefundableCents == 0 {
		// A goodwill return of a zero-priced line. Nothing to pay back.
		return s.closeReturn(ctx, requestID, row.OrderID, "approved", resolution, "", actor)
	}

	split, err := s.splitRefund(ctx, &row)
	if err != nil {
		return err
	}
	return s.approveWithRefund(ctx, &row, split, resolution, actor)
}

// refundSplit is how a return is paid back: part to the card, part to the ledger.
//
// An order can be funded from two places at once — store credit plus a card — so
// paying one back is two acts. Before this, RefundableCents was compared to what
// the CARD captured, so a part-credit order claimed more than its capture and was
// refused, and a wholly credit-funded order had no payment row at all. The
// customer sent goods back and could not be paid.
//
// RefundableCents is return_refundable_amount now, and it used to be the raw line
// prices — which claimed the UNDISCOUNTED total against a capture that
// payments_capture_matches_order forces to be the discounted one. On a couponed
// order that over-refunded a partial return and refused a full one outright. It
// also never returned the delivery fee, which a statutory rescission has to.
type refundSplit struct {
	// Card is refunded through the provider. Zero means there is no provider call
	// to make, which is the wholly-credit-funded case.
	Card int64
	// Credit is posted to the ledger as a new positive entry.
	Credit int64
}

// splitRefund decides how much of a claim each source pays.
//
// CARD FIRST, credit last, and that is a commercial choice rather than an
// arithmetic one. Card money is the customer's own; store credit is a claim on
// this shop. Returning the real money first is what somebody expects, and it is
// the more generous reading when only part of an order comes back.
//
// It refuses a claim neither source can cover, with the numbers rather than a
// constraint name — refunds_within_capture would refuse an over-claim on the card
// anyway, and a staff member cannot act on "refunds_within_capture".
func (s *Store) splitRefund(ctx context.Context, row *db.ReturnForDecisionRow) (refundSplit, error) {
	var capturedRemaining, alreadyRefunded int64
	if row.PaymentID.Valid {
		var err error
		// THIS return's own claim is excluded, the way refunds_guard excludes
		// NEW.id. open_refund is idempotent on request_key, so a Decide retried
		// after a stalled provider call finds the row it wrote last time —
		// counting that row as somebody else's claim made the retry compute
		// zero headroom and be refused here, BEFORE Stripe was reached. A
		// refund that could not be resumed by any door.
		if alreadyRefunded, err = s.q.RefundedSoFar(ctx, db.RefundedSoFarParams{
			PaymentID:  row.PaymentID.UUID,
			RequestKey: refundRequestKey(row.ID),
		}); err != nil {
			return refundSplit{}, fmt.Errorf("read refunds so far for return %s: %w", row.ID, err)
		}
		capturedRemaining = row.CapturedAmountCents.Int64 - alreadyRefunded
	}

	credit, err := s.q.OrderCreditPosition(ctx, uuid.NullUUID{UUID: row.OrderID, Valid: true})
	if err != nil {
		return refundSplit{}, fmt.Errorf("read credit position for return %s: %w", row.ID, err)
	}
	creditRemaining := credit.Spent - credit.Returned

	split := refundSplit{Card: min(row.RefundableCents, capturedRemaining)}
	split.Credit = min(row.RefundableCents-split.Card, creditRemaining)

	if split.Card+split.Credit < row.RefundableCents {
		// Every figure, because a staff member has to act on this: what was taken,
		// what has already gone back, what each source has left, and what is being
		// asked for. refunds_within_capture would refuse the card over-claim
		// anyway, and nobody can act on a constraint name.
		return refundSplit{}, fmt.Errorf(
			"%w: 這筆訂單卡片收了 %d,已退 %d,只剩 %d 可退;購物金用了 %d,已還 %d,"+
				"只剩 %d 可還。這次要退 %d,兩邊加起來不夠",
			ErrRefused, row.CapturedAmountCents.Int64, alreadyRefunded, capturedRemaining,
			credit.Spent, credit.Returned, creditRemaining, row.RefundableCents)
	}
	// Compensating credit needs somebody to compensate. A guest order cannot have
	// spent credit in the first place, so this is a schema surprise rather than an
	// ordinary refusal.
	if split.Credit > 0 && !row.UserID.Valid {
		return refundSplit{}, fmt.Errorf(
			"%w: return %s owes %d in store credit but the order has no account",
			ErrRefused, row.ID, split.Credit)
	}
	return split, nil
}

// approveWithRefund pays the money back and closes the return.
//
// Split from Decide along the seam the doc comment already describes: that
// function decides WHETHER, this one moves money. The provider call sits between
// two transactions on purpose — the refund row is committed BEFORE Stripe is
// asked, so a crash between the request and the response leaves something
// reconciliation can find.
//
// The CREDIT portion is posted in the closing transaction rather than beside the
// refund row, because it involves no provider: if the card refund fails there is
// nothing to reconcile on the ledger, and the return stays pending for a human.
func (s *Store) approveWithRefund(ctx context.Context, row *db.ReturnForDecisionRow,
	split refundSplit, resolution string, actor uuid.NullUUID,
) error {
	providerRef := ""
	if split.Card > 0 {
		ref, state, err := s.refundCard(ctx, row, split.Card, resolution)
		if err != nil {
			return err
		}
		// The order's own history says 退款 only when the money actually left.
		// A refund Stripe has ACCEPTED and not settled is real — the row
		// records it and /admin/health lists it — but order_events is rendered
		// on the customer's own order page, so an event written here would tell
		// somebody their money is back while it is still in flight. That is the
		// same claim the hard-coded 'succeeded' was making, one table over.
		if state == RefundSucceeded {
			providerRef = ref
		}
	}
	if split.Credit > 0 {
		if _, err := s.q.CompensateReturnWithCredit(ctx, db.CompensateReturnWithCreditParams{
			UserID:      row.UserID.UUID,
			AmountCents: split.Credit,
			Reason:      "退貨退回購物金",
			OrderID:     row.OrderID,
			ReturnID:    row.ID.String(),
			Actor:       actor,
		}); err != nil {
			return fmt.Errorf("compensate return %s with credit: %w", row.ID, err)
		}
	}
	return s.closeReturn(ctx, row.ID, row.OrderID, "approved", resolution, providerRef, actor)
}

// refundRequestKey is goen's own idempotency key for a return's refund.
//
// Derived from the RETURN rather than generated, which is what makes a retry
// find the same row through open_refund and the same refund at Stripe. It lives
// in one function because two callers have to agree on it: the one that WRITES
// the row, and the arithmetic that must not count that row as somebody else's
// claim on the capture.
func refundRequestKey(returnID uuid.UUID) string { return "return:" + returnID.String() }

// refundCard is the two-transaction dance around the provider.
//
// It answers with what the provider SAID rather than with the good news: a
// refund Stripe accepted may be 'pending' or 'requires_action', and recording
// either as succeeded is goen asserting money moved that has not.
func (s *Store) refundCard(ctx context.Context, row *db.ReturnForDecisionRow,
	cents int64, resolution string,
) (string, RefundState, error) {
	requestKey := refundRequestKey(row.ID)
	if _, openErr := s.q.OpenRefund(ctx, db.OpenRefundParams{
		PaymentID:       row.PaymentID.UUID,
		RequestKey:      requestKey,
		AmountCents:     cents,
		Reason:          resolution,
		ReturnRequestID: row.ID,
	}); openErr != nil {
		// refunds_within_capture speaks here when the claim exceeds what was
		// taken, which is a reconciliation problem rather than a staff error.
		return "", "", fmt.Errorf("%w: %s", ErrRefused, openErr.Error())
	}

	// Committed. Now the provider.
	intentID, err := s.refunder.PaymentIntentFor(ctx, row.ProviderRef.String)
	if err != nil {
		return "", "", fmt.Errorf("resolve payment intent for return %s: %w", row.ID, err)
	}
	providerRef, state, err := s.refunder.Refund(ctx, intentID, requestKey, cents)
	if err != nil {
		// UNKNOWN IS NOT FAILURE. Every error here used to be written as a
		// terminal 'failed', including an ambiguous transport error in which
		// Stripe may well have refunded and goen simply did not hear — and
		// 'failed' drops the row out of refunds_guard's sum, so the same
		// allowance could be claimed a second time. Only a decision from Stripe
		// is terminal; anything else leaves the row 'pending' for the retry or
		// for the person reading /admin/health.
		if !declinedByStripe(err) {
			return "", "", fmt.Errorf("refund for return %s: %w", row.ID, err)
		}
		if settleErr := s.q.SettleRefund(ctx, db.SettleRefundParams{
			RequestKey: requestKey, Status: string(RefundFailed),
		}); settleErr != nil {
			return "", "", fmt.Errorf("refund refused (%w) and could not be marked failed: %w",
				err, settleErr)
		}
		return "", "", fmt.Errorf("refund for return %s: %w", row.ID, err)
	}

	// What the provider SAID, never a constant. settle_refund stamps
	// succeeded_at only for 'succeeded', so a pending refund carries no time —
	// which is what refunds_succeeded_has_time means by the two being one fact.
	if err := s.q.SettleRefund(ctx, db.SettleRefundParams{
		RequestKey:  requestKey,
		ProviderRef: providerRef,
		Status:      string(state),
	}); err != nil {
		return "", "", fmt.Errorf("settle refund for return %s: %w", row.ID, err)
	}

	switch state {
	case RefundSucceeded, RefundPending, RefundRequiresAction:
		// Stripe ACCEPTED the refund. Succeeded means the money left; the other
		// two mean it is on its way and the row now says so, which is the whole
		// point of reading the status back. The return is approved either way —
		// the shop took the goods back, and that decision is not Stripe's to
		// make pending.
		return providerRef, state, nil
	case RefundFailed, RefundCancelled:
		// Terminal and no money moved. The return must NOT close: approving it
		// would leave a settled return, an unpaid customer and no door back,
		// since a return is decided once.
		return "", state, fmt.Errorf("%w: 退款在金流端是 %s,錢沒有退出去 —— 退貨先不結案",
			ErrRefused, state)
	default:
		// RefundState is goen's own closed set and refundState is its only
		// producer, so a value here is a case somebody forgot rather than
		// anything a provider said.
		panic("admin: unknown RefundState: " + string(state))
	}
}

// closeReturn stamps the decision and appends to the order's history, together.
func (s *Store) closeReturn(
	ctx context.Context,
	requestID, orderID uuid.UUID,
	status, resolution, providerRef string,
	actor uuid.NullUUID,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin return decision: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	// The row count is the decision, not the error. Decide's pre-check runs on
	// the pool outside this transaction, so it is the statement's own
	// `status = 'requested'` that actually settles which of two staff members
	// deciding at once wins — and the loser must not go on to write an audit row
	// claiming it made a decision it did not make.
	decided, decideErr := q.DecideReturn(ctx, db.DecideReturnParams{
		ID: requestID, Status: status, Resolution: text(resolution),
	})
	if decideErr != nil {
		return fmt.Errorf("%w: %s", ErrRefused, decideErr.Error())
	}
	if decided == 0 {
		return fmt.Errorf("%w: return %s was decided by somebody else first",
			ErrRefused, requestID)
	}
	if status == "approved" && providerRef != "" {
		if evErr := q.RecordOrderEvent(ctx, db.RecordOrderEventParams{
			OrderID: orderID, Kind: "refunded", ActorUserID: actor,
			Note: text(providerRef),
		}); evErr != nil {
			return fmt.Errorf("record refunded event: %w", evErr)
		}
	}
	if err := auditIn(ctx, q, Event{
		Action: ActionDecideReturn, Table: "return_requests", ID: nullableID(requestID),
		After: map[string]any{
			"decision": status, "resolution": resolution, "refund_ref": providerRef,
		},
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit return decision: %w", err)
	}
	return nil
}

// ReturnLineInspection is what a staff member found in one line of a parcel.
type ReturnLineInspection struct {
	OrderLineID uuid.UUID
	// Received is how many units actually arrived, which may be fewer than the
	// customer said they were sending — that difference is a conversation, and
	// recording it is how anybody has that conversation later.
	Received int32
	// Restocked is how many of them went back on the shelf. The rest are damaged,
	// incomplete or otherwise unsellable; Note says which.
	Restocked int32
	Note      string
}

// checkInspection refuses counts the database would refuse anyway.
//
// return_request_lines_restocked_bounded says the same thing and is the
// authority. Saying it here as well is what turns a constraint name into a
// sentence a staff member can act on — the same reason splitRefund names every
// figure rather than letting refunds_within_capture speak.
func checkInspection(lines []ReturnLineInspection) error {
	if len(lines) == 0 {
		return ErrInvalid
	}
	for _, l := range lines {
		if l.Received < 0 || l.Restocked < 0 || l.Restocked > l.Received {
			return fmt.Errorf("%w: cannot restock %d of %d received",
				ErrInvalid, l.Restocked, l.Received)
		}
	}
	return nil
}

// InspectReturn records what came back and puts the sellable units on the shelf.
//
// The whole parcel in ONE transaction: every line's inspection, every restock
// movement, and the audit row. Split, a crash between them leaves stock on the
// shelf that no inspection explains, or an inspection claiming units that never
// moved — and inventory_movements is the ledger a shop reconciles against, so a
// row with no counterpart is worse than either half being absent.
//
// The restock is read back from what this transaction just WROTE rather than
// from the caller's slice, for the reason Advance reads its held reservations
// inside its own transaction: the set acted on has to be the set the database
// agreed to, not the set the form proposed.
func (s *Store) InspectReturn(
	ctx context.Context, id string, lines []ReturnLineInspection, actor uuid.NullUUID,
) error {
	requestID, err := uuid.Parse(id)
	if err != nil {
		return ErrRefused
	}
	if checkErr := checkInspection(lines); checkErr != nil {
		return checkErr
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin return inspection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	for _, l := range lines {
		n, inspectErr := q.InspectReturnLine(ctx, db.InspectReturnLineParams{
			RequestID: requestID, OrderLineID: l.OrderLineID,
			Received: l.Received, Restocked: l.Restocked, Note: l.Note,
		})
		if inspectErr != nil {
			return fmt.Errorf("%w: %s", ErrRefused, inspectErr.Error())
		}
		// Zero rows is one of three things and all are refusals the caller has to
		// hear: the request is not approved, the line does not belong to it, or
		// somebody has already inspected it. Silently writing nothing would leave
		// the page reporting work that did not happen — and for the third case it
		// would show a corrected count against stock that never moved.
		if n == 0 {
			return fmt.Errorf("%w: line %s of return %s is not open for inspection "+
				"— it may already have been inspected, in which case a recount is a "+
				"stock adjustment", ErrRefused, l.OrderLineID, requestID)
		}
	}

	restock, err := q.ReturnRestockLines(ctx, requestID)
	if err != nil {
		return fmt.Errorf("read what this return restocks: %w", err)
	}
	for _, r := range restock {
		if err := q.RestockReturnedUnits(ctx, db.RestockReturnedUnitsParams{
			VariantID: r.VariantID, Delta: r.Quantity,
			// Per (request, line), so a resubmitted form posts one movement —
			// inventory_movements has a unique index on this.
			IdempotencyKey: "return:" + requestID.String() + ":" + r.OrderLineID.String(),
			RequestID:      requestID,
			ActorUserID:    actor,
		}); err != nil {
			return fmt.Errorf("restock %s from return %s: %w", r.VariantID, requestID, err)
		}
	}

	if err := auditIn(ctx, q, Event{
		Action: ActionInspectReturn, Table: "return_requests", ID: nullableID(requestID),
		// Counts, never the note: audit_events is append-only and erase_user does
		// not reach it, so a staff member's sentence about a customer's parcel
		// would outlive every later correction of it. The note lives on the line,
		// which erase_user's cascade does reach.
		After: map[string]any{"lines": len(lines), "restocked": len(restock)},
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit return inspection: %w", err)
	}
	return nil
}

// CompleteReturn closes an inspected return.
//
// It writes no stock: the movement was posted with the INSPECTION, because that
// is when the goods physically went back on the shelf. Closing is bookkeeping
// after the fact, and posting it here would leave a window in which the units
// were on the shelf and the ledger did not say so.
func (s *Store) CompleteReturn(ctx context.Context, id, resolution string, actor uuid.NullUUID) error {
	requestID, err := uuid.Parse(id)
	if err != nil {
		return ErrRefused
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin return completion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	// return_requests_completed_is_inspected refuses this while any line is
	// un-inspected, so an uninspected parcel raises rather than closing quietly.
	closed, err := q.CompleteReturn(ctx, db.CompleteReturnParams{
		ID: requestID, Resolution: resolution,
	})
	if err != nil {
		return fmt.Errorf("%w: %s", ErrRefused, err.Error())
	}
	if closed == 0 {
		return fmt.Errorf("%w: return %s is not open for completion", ErrRefused, requestID)
	}

	if err := auditIn(ctx, q, Event{
		Action: ActionCompleteReturn, Table: "return_requests", ID: nullableID(requestID),
		After: map[string]any{"resolution": resolution},
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit return completion: %w", err)
	}
	return nil
}

// GrantCredit puts store credit on a customer's account.
//
// This is the input the ledger was missing: store credit was displayable but
// nothing ever granted any, so a checkout that applied it would have been a
// button over an empty ledger.
//
// The amount is in cents and must be positive — this grants, it does not take
// away. A correction is its own posting with its own reason, so the ledger
// reads as a history rather than as a current figure that was edited.
func (s *Store) GrantCredit(ctx context.Context, email string, amountCents int64, reason string, actor uuid.NullUUID) (balanceCents int64, err error) {
	email, reason = strings.TrimSpace(email), strings.TrimSpace(reason)
	if email == "" || reason == "" || amountCents <= 0 {
		return 0, ErrInvalid
	}
	if amountCents > MaxCreditGrant {
		return 0, ErrInvalid
	}

	user, err := s.q.CustomerByEmail(ctx, email)
	if err != nil {
		return 0, fmt.Errorf("%w: no customer for %s", ErrRefused, email)
	}

	// Keyed on the actor's own request, so a double-submitted form is one
	// posting. The key includes the amount and reason because granting the same
	// customer 500 twice for different reasons is two grants, not a repeat.
	key := "grant:" + user.ID.String() + ":" +
		strconv.FormatInt(amountCents, 10) + ":" + reason
	err = s.audited(ctx, Event{
		Action: ActionGrantCredit, Table: "store_credit_entries", ID: nullableID(user.ID),
		Before: nil, After: map[string]any{"email": email, "amount_cents": amountCents, "reason": reason},
	},
		func(ctx context.Context, q *db.Queries) error {
			if _, postErr := q.PostStoreCredit(ctx, db.PostStoreCreditParams{
				UserID:         user.ID,
				AmountCents:    amountCents,
				Reason:         reason,
				IdempotencyKey: key,
				ActorUserID:    actor,
			}); postErr != nil {
				return fmt.Errorf("%w: %s", ErrRefused, postErr.Error())
			}
			// Read INSIDE the same transaction, after the posting. The number
			// the staff member is shown is then the one this grant produced,
			// not one a concurrent spend could have moved in between.
			//
			// It matters because the form is a blank box: granting again
			// because the first grant was not visible is how a customer ends
			// up with twice what they were owed. CreditBalance was written for
			// this and had no caller.
			var balErr error
			balanceCents, balErr = q.CreditBalance(ctx, uuid.NullUUID{UUID: user.ID, Valid: true})
			if balErr != nil {
				return fmt.Errorf("read credit balance: %w", balErr)
			}
			return nil
		})
	return balanceCents, err
}

// Credit reads the recent ledger for the back office.
func (s *Store) Credit(ctx context.Context) (pages.AdminCreditView, error) {
	rows, err := s.q.RecentCredit(ctx, PageSize)
	if err != nil {
		return pages.AdminCreditView{}, fmt.Errorf("read credit ledger: %w", err)
	}
	view := pages.AdminCreditView{}
	for i := range rows {
		r := &rows[i]
		view.Rows = append(view.Rows, pages.AdminCreditEntry{
			Email:       r.Email,
			AmountCents: r.AmountCents,
			Reason:      r.Reason,
			At:          r.CreatedAt.Format("2006-01-02 15:04"),
		})
	}
	return view, nil
}

// nullableStamp formats a timestamp that may be absent.
func nullableStamp(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return t.Time.Format("2006-01-02 15:04")
}

// MovementPageSize bounds one page of a variant's stock ledger.
//
// The ledger is append-only and grows with every sale, so this is a page and not the
// history: fifty rows is what a person reading "why is this four" needs, and the
// running total is computed over the WHOLE ledger so the page is still truthful.
const MovementPageSize = 50

// Movements reads one variant's stock ledger.
//
// inventory_movements is written by record_inventory_movement and by nothing else —
// that is what makes "one writer" true rather than aspirational — and until this
// method nothing READ it. A shop could see that a SKU has four units and not how it
// got there: which sale, which return, which hand adjustment, and by whom.
func (s *Store) Movements(ctx context.Context, sku string) (pages.AdminMovementsView, error) {
	v, err := s.q.AdminVariantBySKU(ctx, sku)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pages.AdminMovementsView{}, ErrNotFound
		}
		return pages.AdminMovementsView{}, fmt.Errorf("read variant %s: %w", sku, err)
	}
	rows, err := s.q.VariantMovements(ctx, db.VariantMovementsParams{
		SKU: sku, RowLimit: MovementPageSize,
	})
	if err != nil {
		return pages.AdminMovementsView{}, fmt.Errorf("read movements of %s: %w", sku, err)
	}

	view := pages.AdminMovementsView{
		SKU: v.SKU, ProductName: v.ProductName, Slug: v.Slug,
		Stock: v.StockQuantity, Safety: v.SafetyStock,
		Rows: make([]pages.AdminMovement, 0, len(rows)),
	}
	for i := range rows {
		m := &rows[i]
		view.Rows = append(view.Rows, pages.AdminMovement{
			At:          m.CreatedAt.Format("2006-01-02 15:04"),
			Delta:       m.Delta,
			Reason:      m.Reason,
			OrderNumber: m.OrderNumber,
			Actor:       m.Actor,
			Running:     m.RunningTotal,
		})
	}
	return view, nil
}
