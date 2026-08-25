package admin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/ui/pages"
)

// Invoicer files uniform invoices, as the back office needs them. A NIL
// Invoicer means no e-invoice provider is configured, and the page then renders
// no controls at all.
type Invoicer interface {
	Documents(ctx context.Context, orderNumber string) ([]invoice.Document, error)
	Issue(ctx context.Context, orderNumber string) (invoice.Document, error)
	Void(ctx context.Context, orderNumber, reason string) error
	// Allowance relieves part of a live invoice, which is what a REFUND leaves
	// owed to the 財政部. internal/invoice has had it since the feature shipped
	// and this interface did not declare it, so no handler could call it and no
	// route existed: a customer was refunded while the tax document still
	// recorded the whole sale. README.md said it was delivered.
	Allowance(ctx context.Context, orderNumber string, amountCents int64) (invoice.Document, error)
}

// Store reads and writes through the ADMIN pool, which assumes the admin role.
type Store struct {
	pool     *pgxpool.Pool
	q        *db.Queries
	refunder Refunder
	// invoices may be nil; see Invoicer.
	invoices Invoicer
}

// NewStore returns a Store over the admin pool. refunder may be one that
// refuses: a back office without Stripe credentials can still decide returns,
// and a refund it cannot pay must fail loudly.
func NewStore(pool *pgxpool.Pool, refunder Refunder, invoices Invoicer) *Store {
	if pool == nil || refunder == nil {
		panic("admin: NewStore requires a pool and a refunder")
	}
	return &Store{pool: pool, q: db.New(pool), refunder: refunder, invoices: invoices}
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
		// A search ignores the status filter: somebody on the phone wants that
		// order, not that order if it is in the tab they had open.
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
			StatusText: FundedStatusLabel(ctx, o.FulfillmentStatus, o.Committed, o.OwedCents),
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
		StatusText:    FundedStatusLabel(ctx, o.FulfillmentStatus, o.Committed, o.OwedCents),
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
		// UpdateOrderDelivery's WHERE clause is the authority; this only decides
		// whether to offer the form.
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
	}

	if shipErr := s.fillShippable(ctx, &view, o.ID, o.FulfillmentStatus); shipErr != nil {
		return pages.AdminOrderView{}, shipErr
	}

	if invErr := s.fillInvoices(ctx, &view, number); invErr != nil {
		return pages.AdminOrderView{}, invErr
	}
	for _, n := range NextStatuses(o.FulfillmentStatus) {
		view.Next = append(view.Next, pages.AdminTransition{Value: n, Label: StatusLabel(ctx, n)})
	}
	for _, l := range lines {
		view.Lines = append(view.Lines, pages.OrderLine{
			SKU: l.SKU, Name: l.ProductName, Label: l.VariantLabel.String,
			UnitCents: l.UnitPriceCents, Quantity: l.Quantity,
		})
	}

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

// Advance moves an order along its lifecycle. orders_check_transition validates
// the move and its refusal is returned unreplaced, because it names the rule.
//
// The Checkout Sessions returned are a CANCELLATION's, for the caller to close
// at Stripe once this has committed; every other status returns none.
func (s *Store) Advance(ctx context.Context, number, status string, actor uuid.NullUUID) ([]string, error) {
	if ParseStatus(status) == "" {
		return nil, ErrRefused
	}
	// Ship is the only door to 'shipped', because a dispatch also records the
	// carrier and settles the held stock.
	if status == "shipped" {
		return nil, ErrRefused
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin advance: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	row, err := q.OrderIDByNumber(ctx, number)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	var held []uuid.UUID
	if status == "cancelled" {
		if held, err = q.HeldReservationsForOrder(ctx, number); err != nil {
			return nil, fmt.Errorf("read holds of %s: %w", number, err)
		}
	}
	if err := q.AdvanceOrder(ctx, db.AdvanceOrderParams{
		OrderNumber: number, Status: status,
	}); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRefused, err)
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
	if err := auditIn(ctx, q, Event{
		Action: ActionAdvanceOrder, Table: "orders", ID: nullableID(row.ID),
		Before: nil, After: map[string]any{"number": number, "status": status},
	}); err != nil {
		return nil, err
	}
	// Read inside the transaction so the set handed back is the one this
	// transaction saw, not what a later read finds after a webhook moved a row.
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

// applyStatusEffects does what a status move MEANS beyond the column — the
// stock release, the credit reversal, the parcel stamp — in the CALLER's
// transaction, so a status cannot disagree with what it implies.
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
		// BOTH transitions that end a delivery: shipped -> completed directly is
		// the only honest move for convenience-store pickup, and stamping only on
		// 'delivered' would leave that channel's parcels unstamped, so
		// /admin/returns would read the Consumer Protection Act §19 window as
		// never having started. Idempotent by the query's own WHERE clause.
		if err := q.MarkShipmentsDelivered(ctx, e.orderID); err != nil {
			return fmt.Errorf("mark parcels of %s delivered: %w", e.number, err)
		}
	}
	return nil
}

// eventKindFor maps a fulfilment status to its order_events kind. The two
// vocabularies overlap without being the same list.
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

// fillInvoices puts what has actually been FILED on the order page, which is a
// different question from the preference the customer asked for at checkout.
func (s *Store) fillInvoices(ctx context.Context, view *pages.AdminOrderView, number string) error {
	if s.invoices == nil {
		return nil
	}
	view.InvoicingEnabled = true
	docs, err := s.invoices.Documents(ctx, number)
	if err != nil {
		return err
	}
	for i := range docs {
		d := &docs[i]
		doc := pages.AdminInvoiceDocument{
			Kind: d.Kind, Number: d.Number, ProviderRef: d.ProviderRef,
			AmountCents: d.AmountCents, Status: d.Status,
			IssuedAt: d.IssuedAt.Format("2006-01-02 15:04"),
		}
		for _, l := range d.Lines {
			doc.Lines = append(doc.Lines, pages.AdminInvoiceLine{
				Description: l.Description, Quantity: l.Quantity, AmountCents: l.AmountCents,
			})
		}
		view.InvoiceDocuments = append(view.InvoiceDocuments, doc)
	}
	refunded, err := s.q.SettledRefundsForOrder(ctx, number)
	if err != nil {
		return fmt.Errorf("read settled refunds for %s: %w", number, err)
	}
	view.RefundedCents = refunded
	return nil
}

// fillShippable puts what an order still owes a dispatch on its page. CanShip
// follows from what is OUTSTANDING and not from the status, which is what makes
// a second parcel possible.
func (s *Store) fillShippable(
	ctx context.Context, view *pages.AdminOrderView, orderID uuid.UUID, status string,
) error {
	// DELIVERED is here, and leaving it out was a trap with no exit.
	// orders_legal_transition permits shipped -> delivered while a line is still
	// outstanding, deliberately: delivered is a fact about what WENT OUT, and the
	// parcels that shipped have arrived whether or not more is to come. But the
	// dropdown offers 已送達 beside 已完成 with no hint of that, so an operator
	// moves an order there — and then completing it is refused by
	// orders_finished_when_shipped while the dispatch form is absent. The order
	// is wedged, and the remaining line's hold is stranded: release_reservation
	// refuses a committed order, ExpiredReservations excludes it, and
	// /admin/health counts neither. That is mistake #17's cost exactly, one
	// status later.
	if status != "picking" && status != "shipped" && status != "delivered" {
		return nil
	}
	rows, err := s.q.ShippableLines(ctx, orderID)
	if err != nil {
		return fmt.Errorf("read shippable lines: %w", err)
	}
	for i := range rows {
		l := &rows[i]
		view.Shippable = append(view.Shippable, pages.AdminShippableLine{
			OrderLineID: l.OrderLineID.String(),
			SKU:         l.SKU,
			Name:        l.ProductName,
			Label:       l.VariantLabel.String,
			Remaining:   l.Remaining,
			Held:        l.Held,
		})
	}
	view.CanShip = len(view.Shippable) > 0
	return nil
}

// Dispatch is one parcel: what is in it, and who is carrying it.
type Dispatch struct {
	Carrier  string
	Tracking string
	// Lines is how many of each order line this parcel carries; empty means
	// everything still outstanding, and a line left out is not in this parcel.
	Lines map[uuid.UUID]int32
}

// Ship records a dispatch — the shipment, its lines, the status move, the stock
// it settles and the history entry — in ONE transaction, because every pair of
// those is wrong on its own.
func (s *Store) Ship(ctx context.Context, number string, d Dispatch, actor uuid.NullUUID) error {
	carrier, tracking := strings.TrimSpace(d.Carrier), strings.TrimSpace(d.Tracking)
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
		return fmt.Errorf("%w: %w", ErrRefused, err)
	}
	// A parcel is only recorded for an order that has entered fulfilment. This is
	// the same set fillShippable renders the form for; the trigger
	// shipment_order_in_fulfilment is the authority, and this is the sentence.
	switch row.FulfillmentStatus {
	case "picking", "shipped", "delivered":
	default:
		return fmt.Errorf("%w: order %s is %s and has not been picked",
			ErrRefused, number, row.FulfillmentStatus)
	}
	shipmentID, shipErr := q.CreateShipment(ctx, db.CreateShipmentParams{
		OrderID: row.ID, Carrier: carrier, TrackingNumber: tracking,
	})
	if shipErr != nil {
		return fmt.Errorf("record shipment: %w", shipErr)
	}

	if fillErr := fillParcel(ctx, q, row.ID, shipmentID, number, d.Lines); fillErr != nil {
		return fillErr
	}

	// Only picking advances to shipped. A second parcel leaves the order where it
	// already is; the status check above and shipment_order_in_fulfilment refuse
	// an unpicked order, because this branch never reaches the transition trigger.
	if row.FulfillmentStatus == "picking" {
		if advErr := q.AdvanceOrder(ctx, db.AdvanceOrderParams{
			OrderNumber: number, Status: "shipped",
		}); advErr != nil {
			return fmt.Errorf("%w: %w", ErrRefused, advErr)
		}
	}

	// One notice per PARCEL: the dedupe key is the tracking number and not the
	// order, so an order arriving in two boxes is two notices.
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

// fillParcel writes what is in this parcel and settles the holds behind it,
// which are one act: a line without its hold settled leaves stock spoken for
// against goods that have gone out, and a hold without its line leaves the
// parcel unable to say what it carried.
func fillParcel(
	ctx context.Context, q *db.Queries, orderID, shipmentID uuid.UUID,
	number string, want map[uuid.UUID]int32,
) error {
	packed, err := packParcel(ctx, q, orderID, number, want)
	if err != nil {
		return err
	}
	for _, p := range packed {
		if lineErr := q.CreateShipmentLine(ctx, db.CreateShipmentLineParams{
			OrderID: orderID, ShipmentID: shipmentID,
			OrderLineID: p.lineID, Quantity: p.quantity,
		}); lineErr != nil {
			return fmt.Errorf("record shipment line: %w", lineErr)
		}
		if consumeErr := q.ConsumeReservationPartial(ctx, db.ConsumeReservationPartialParams{
			ReservationID: p.reservationID, Quantity: p.quantity,
		}); consumeErr != nil {
			return fmt.Errorf("settle the hold behind line %s: %w", p.lineID, consumeErr)
		}
	}
	return nil
}

// parcelLine is one line of a dispatch, with the hold it settles.
type parcelLine struct {
	lineID        uuid.UUID
	reservationID uuid.UUID
	quantity      int32
}

// packParcel decides what is in this parcel and which holds it settles; `want`
// empty means everything still outstanding. Each refusal below is a state that
// would otherwise be silent — most of all a line with no hold, which ships
// without posting any inventory movement.
func packParcel(
	ctx context.Context, q *db.Queries, orderID uuid.UUID, number string, want map[uuid.UUID]int32,
) ([]parcelLine, error) {
	rows, err := q.ShippableLines(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("read shippable lines: %w", err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%w: order %s has nothing left to ship", ErrRefused, number)
	}

	packed := make([]parcelLine, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		quantity := r.Remaining
		if len(want) > 0 {
			n, asked := want[r.OrderLineID]
			if !asked || n == 0 {
				continue // not in this parcel
			}
			quantity = n
		}
		if quantity < 0 || quantity > r.Remaining {
			return nil, fmt.Errorf("%w: line %s has %d left to ship, not %d",
				ErrQuantity, r.OrderLineID, r.Remaining, quantity)
		}
		if !r.ReservationID.Valid {
			return nil, fmt.Errorf("%w: line %s of order %s holds no stock; its "+
				"reservation was released and shipping would not decrement anything",
				ErrRefused, r.OrderLineID, number)
		}
		if quantity > r.Held {
			return nil, fmt.Errorf("%w: line %s holds %d units, not %d",
				ErrQuantity, r.OrderLineID, r.Held, quantity)
		}
		packed = append(packed, parcelLine{
			lineID: r.OrderLineID, reservationID: r.ReservationID.UUID, quantity: quantity,
		})
	}

	// Every quantity at zero is a mistake rather than an instruction to send an
	// empty box with a tracking number on it.
	if len(packed) == 0 {
		return nil, fmt.Errorf("%w: no lines were selected for this parcel", ErrQuantity)
	}
	return packed, nil
}

// SetStaffNote records an internal note. erase_user clears customer_note and
// leaves this, so it must never hold anything the customer wrote.
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

// AdjustStock moves stock through the ledger, which record_inventory_movement
// is the only door to. The idempotency key is the caller's, so a resubmitted
// form is one adjustment.
func (s *Store) AdjustStock(ctx context.Context, sku string, delta int32, actorID, key string) error {
	v, err := s.q.AdminVariantBySKU(ctx, sku)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("read variant: %w", err)
	}
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
				return fmt.Errorf("%w: %w", ErrRefused, err)
			}
			// Called on EVERY adjustment: the claim's own EXISTS decides whether
			// the variant is back above its threshold, so a movement that does
			// not cross it claims nothing.
			return enqueueRestockNotices(ctx, q, v.ID)
		})
}

// ReceiveStock books a delivery in, through the ledger's own 'receipt' reason,
// so that goods a shop bought are distinguishable in its own ledger from a
// staff member correcting a miscount.
func (s *Store) ReceiveStock(ctx context.Context, sku string, quantity int32, actorID, key string) error {
	v, err := s.q.AdminVariantBySKU(ctx, sku)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("read variant: %w", err)
	}
	actor, err := uuid.Parse(actorID)
	if err != nil {
		return fmt.Errorf("receive stock: actor %q is not a user id: %w", actorID, err)
	}
	return s.audited(ctx, Event{
		Action: ActionReceiveStock, Table: "product_variants", ID: nullableID(v.ID),
		Before: map[string]any{"sku": sku, "stock": v.StockQuantity},
		After:  map[string]any{"received": quantity},
	},
		func(ctx context.Context, q *db.Queries) error {
			if err := q.ReceiveStock(ctx, db.ReceiveStockParams{
				VariantID: v.ID, Delta: quantity, IdempotencyKey: key, ActorUserID: actor,
			}); err != nil {
				return fmt.Errorf("%w: %w", ErrRefused, err)
			}
			// A receipt is the movement most likely to carry a variant back
			// above its safety stock.
			return enqueueRestockNotices(ctx, q, v.ID)
		})
}

// SetVariantActive retires or restores a variant. A refusal here is usually
// sale_campaign_variant_still_valid: the last discounted variant of a product
// some campaign features.
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
				return fmt.Errorf("%w: %w", ErrRefused, err)
			}
			return nil
		})
}

// SetVariantPrice reprices a variant. product_variants_compare_at_is_higher
// refuses a "sale" that is not a saving.
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
				return fmt.Errorf("%w: %w", ErrRefused, err)
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
			// The presence of the figure, never a zero: 0 received is a finding
			// and NULL is a parcel nobody has opened.
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
		item := pages.AdminReturn{
			ID:          r.ID.String(),
			OrderNumber: r.OrderNumber,
			Status:      r.Status,
			StatusText:  ReturnStatusLabel(ctx, r.Status),
			Reason:      r.Reason,
			Units:       r.Units,
			AmountCents: r.RefundableCents,
			CreatedAt:   r.CreatedAt.Format("2006-01-02 15:04"),
			Decided:     r.Status != "requested",
			Lines:       byRequest[r.ID],
			Window:      r.RescissionWindow,
		}
		if payoutErr := s.fillReturnPayoutState(ctx, r.Status, r.ID, &item); payoutErr != nil {
			return pages.AdminReturnsView{}, payoutErr
		}
		view.Rows = append(view.Rows, item)
	}
	return view, nil
}

func (s *Store) fillReturnPayoutState(ctx context.Context, status string,
	requestID uuid.UUID, item *pages.AdminReturn,
) error {
	if status != "approved" {
		return nil
	}
	owed, done, outErr := s.outstandingOnReturn(ctx, requestID)
	if errors.Is(outErr, ErrRefused) {
		// A payout that no longer fits its sources is not a reason to hide
		// the queue. It is exactly the row a person must investigate.
		item.PayoutOutstanding = true
		item.PayoutBlocked = true
		slog.ErrorContext(ctx, "return payout no longer fits its sources",
			"return_id", requestID, "error", outErr)
		return nil
	}
	if outErr != nil {
		return outErr
	}
	item.PayoutOutstanding = !done
	terminal, termErr := s.q.ReturnRefundTerminal(ctx,
		uuid.NullUUID{UUID: requestID, Valid: true})
	if termErr != nil {
		return fmt.Errorf("read terminal refund for return %s: %w", requestID, termErr)
	}
	item.PayoutBlocked = terminal && owed.Card > 0
	return nil
}

// outstandingOnReturn asks the same two source-specific questions as Decide's
// retry. Decided and settled are separate facts; this method is the one
// projection of what the retry can still move.
func (s *Store) outstandingOnReturn(ctx context.Context, requestID uuid.UUID) (
	refundSplit, bool, error,
) {
	row, err := s.q.ReturnForDecision(ctx, requestID)
	if err != nil {
		return refundSplit{}, false, fmt.Errorf("read return %s payout: %w", requestID, err)
	}
	split, err := s.splitRefund(ctx, &row)
	if err != nil {
		return refundSplit{}, false, err
	}
	owed, done, err := s.stillOwedOnReturn(ctx, requestID, split)
	if err != nil || !done {
		return owed, done, err
	}
	pointsOutstanding, err := s.returnPointsOutstanding(ctx, &row)
	if err != nil {
		return refundSplit{}, false, err
	}
	return owed, !pointsOutstanding, nil
}

// Decide approves or rejects a return, and pays the money back when it
// approves. The refund row is committed BEFORE Stripe is called and settled
// after, so a crash between the two leaves something reconciliation can find;
// request_key is what makes the retry one refund at Stripe and one row here.
func (s *Store) Decide(ctx context.Context, id, decision, resolution string, actor uuid.NullUUID) error {
	requestID, row, retry, readErr := s.returnUnderDecision(ctx, id, decision)
	if readErr != nil {
		return readErr
	}
	if decision == "rejected" {
		return s.closeReturn(ctx, requestID, "rejected", resolution)
	}

	// Validated BEFORE the claim, and it only reads: a claim that cannot be paid
	// should leave the return open for somebody to work out why.
	split, splitErr := s.splitRefund(ctx, &row)
	if splitErr != nil {
		return splitErr
	}

	if retry {
		return s.retryApprovedReturn(ctx, id, requestID, &row, split, resolution, actor)
	}
	if row.RefundableCents == 0 {
		return s.closeReturn(ctx, requestID, "approved", resolution)
	}

	// THE CLAIM, and it commits before a cent moves. Two staff members deciding
	// one return at once both passed the pool read above; only one wins this,
	// and the loser must not have paid anything on the way to finding out.
	if closeErr := s.closeReturn(ctx, requestID, "approved", resolution); closeErr != nil {
		return closeErr
	}
	return s.payApprovedReturn(ctx, &row, split, resolution, actor)
}

func (s *Store) retryApprovedReturn(ctx context.Context, id string, requestID uuid.UUID,
	row *db.ReturnForDecisionRow, split refundSplit, resolution string, actor uuid.NullUUID,
) error {
	outstanding, done, outErr := s.stillOwedOnReturn(ctx, requestID, split)
	if outErr != nil {
		return outErr
	}
	if !done {
		// Only what is MISSING. Re-sending a card refund that already settled
		// meets refunds_settled_is_history, and re-posting the credit meets the
		// idempotency key — so a retry that resent both could never finish the
		// half that had failed.
		return s.payApprovedReturn(ctx, row, outstanding, resolution, actor)
	}

	// Money and points are separately durable. If the money committed and the
	// clawback failed, this is useful work rather than a duplicate decision:
	// finish that last idempotent posting and report success.
	pointsOutstanding, pointsErr := s.returnPointsOutstanding(ctx, row)
	if pointsErr != nil {
		return pointsErr
	}
	if pointsOutstanding {
		return s.reverseReturnPoints(ctx, row)
	}
	// Refused rather than reported as done: a return is decided once, and
	// pressing 同意 on one that is already approved AND paid did nothing. Saying
	// so is what tells the staff member the decision was somebody else's.
	return fmt.Errorf("%w: return %s is already approved and its refund has landed", ErrRefused, id)
}

// returnUnderDecision reads the return this decision is about and says whether
// it is a first decision or a RETRY of a payout that did not complete.
//
// An already-approved return being approved again is not a second decision. The
// claim moved ahead of the money, which is what stops two staff members both
// paying — and it also means a refund that failed no longer leaves the return
// open to be decided again. Pressing 同意 once more is how it resumes: the
// decision is not retaken, and refundRequestKey makes the provider call the
// same one.
func (s *Store) returnUnderDecision(ctx context.Context, id, decision string) (
	uuid.UUID, db.ReturnForDecisionRow, bool, error,
) {
	requestID, err := uuid.Parse(id)
	if err != nil {
		return uuid.Nil, db.ReturnForDecisionRow{}, false, ErrRefused
	}
	if decision != "approved" && decision != "rejected" {
		return uuid.Nil, db.ReturnForDecisionRow{}, false, ErrRefused
	}
	row, err := s.q.ReturnForDecision(ctx, requestID)
	if err != nil {
		return uuid.Nil, db.ReturnForDecisionRow{}, false, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	retry := row.Status == "approved" && decision == "approved"
	if row.Status != "requested" && !retry {
		return uuid.Nil, db.ReturnForDecisionRow{}, false,
			fmt.Errorf("%w: return %s is already %s", ErrRefused, id, row.Status)
	}
	return requestID, row, retry, nil
}

// stillOwedOnReturn reports what a RETRY still has to send, and whether there is
// anything at all.
//
// It used to ask only whether the CARD half had settled. A return paid from both
// sources whose card refund landed and whose store-credit compensation failed
// was therefore reported as done: the retry was refused, no other door posts
// that credit, and the customer was short by the credit portion with nothing on
// /admin/health to say so. The two halves are separate facts and have to be
// asked separately.
func (s *Store) stillOwedOnReturn(ctx context.Context, requestID uuid.UUID, split refundSplit) (
	refundSplit, bool, error,
) {
	out := split
	if split.Card > 0 {
		settled, err := s.q.ReturnRefundSettled(ctx, uuid.NullUUID{UUID: requestID, Valid: true})
		if err != nil {
			return refundSplit{}, false, fmt.Errorf("read the refund for return %s: %w", requestID, err)
		}
		if settled {
			out.Card = 0
		}
	}
	if split.Credit > 0 {
		posted, err := s.q.ReturnCreditPosted(ctx, requestID.String())
		if err != nil {
			return refundSplit{}, false, fmt.Errorf("read the credit for return %s: %w", requestID, err)
		}
		if posted {
			out.Credit = 0
		}
	}
	return out, out.Card == 0 && out.Credit == 0, nil
}

// refundSplit is how a return is paid back: part to the card, part to the
// ledger, because an order can be funded from both at once.
type refundSplit struct {
	// Card is refunded through the provider. Zero means there is no provider
	// call to make, which is the wholly-credit-funded case.
	Card int64
	// Credit is posted to the ledger as a new positive entry.
	Credit int64
}

// splitRefund decides how much of a claim each source pays. CARD FIRST and
// credit last, which is a commercial choice: card money is the customer's own,
// store credit is only a claim on this shop.
func (s *Store) splitRefund(ctx context.Context, row *db.ReturnForDecisionRow) (refundSplit, error) {
	var capturedRemaining, alreadyRefunded int64
	if row.PaymentID.Valid {
		var err error
		// THIS return's own claim is excluded, the way refunds_guard excludes
		// NEW.id: open_refund is idempotent on request_key, so counting the row a
		// stalled attempt wrote would refuse its own retry before Stripe.
		if alreadyRefunded, err = s.q.RefundedSoFar(ctx, db.RefundedSoFarParams{
			PaymentID:  row.PaymentID.UUID,
			RequestKey: refundRequestKey(row.ID),
		}); err != nil {
			return refundSplit{}, fmt.Errorf("read refunds so far for return %s: %w", row.ID, err)
		}
		capturedRemaining = row.CapturedAmountCents.Int64 - alreadyRefunded
	}

	// THIS return's own compensation is excluded, exactly as its own refund row
	// is above. Without it a retry reads the credit it already posted as credit
	// already returned, and refuses its own resume.
	credit, err := s.q.OrderCreditPositionExcluding(ctx, db.OrderCreditPositionExcludingParams{
		OrderID: uuid.NullUUID{UUID: row.OrderID, Valid: true}, ReturnID: row.ID.String(),
	})
	if err != nil {
		return refundSplit{}, fmt.Errorf("read credit position for return %s: %w", row.ID, err)
	}
	creditRemaining := credit.Spent - credit.Returned

	split := refundSplit{Card: min(row.RefundableCents, capturedRemaining)}
	split.Credit = min(row.RefundableCents-split.Card, creditRemaining)

	if split.Card+split.Credit < row.RefundableCents {
		// Every figure, because nobody can act on a constraint name. This reaches
		// the staff member as KeyAdminNoticeRefused and the operator in full.
		return refundSplit{}, fmt.Errorf(
			"%w: this order captured %d on the card, %d is already refunded and %d remains; "+
				"%d of store credit was spent, %d returned and %d remains. "+
				"Refunding %d does not fit across the two",
			ErrRefused, row.CapturedAmountCents.Int64, alreadyRefunded, capturedRemaining,
			credit.Spent, credit.Returned, creditRemaining, row.RefundableCents)
	}
	// A guest order cannot have spent credit, so this is a schema surprise rather
	// than an ordinary refusal.
	if split.Credit > 0 && !row.UserID.Valid {
		return refundSplit{}, fmt.Errorf(
			"%w: return %s owes %d in store credit but the order has no account",
			ErrRefused, row.ID, split.Credit)
	}
	return split, nil
}

// payApprovedReturn pays the money back on a return this caller has already
// won. It runs AFTER the decision is committed, which is the whole point: the
// claim is what settles which of two staff members decides, and it used to
// settle it after the payout, so the loser had already refunded.
//
// A failure here leaves the return approved and the money not sent — visible,
// and recoverable, because open_refund has already committed a `pending` row
// keyed on the return. The old ordering left the opposite: money sent against a
// decision that lost, on a return frozen at `rejected` with no audit row saying
// who caused it.
func (s *Store) payApprovedReturn(ctx context.Context, row *db.ReturnForDecisionRow,
	split refundSplit, resolution string, actor uuid.NullUUID,
) error {
	providerRef := ""
	moved := false
	payoutDone := true
	if split.Card > 0 {
		ref, state, err := s.refundCard(ctx, row, split.Card, resolution)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrRefundIncomplete, err)
		}
		// order_events is rendered on the customer's own order page. Record this
		// pass only when money has actually left by either source; Stripe merely
		// accepting a card refund is not the same as settling it.
		if state == RefundSucceeded {
			providerRef = ref
			moved = true
		} else {
			payoutDone = false
		}
	}
	if split.Credit > 0 {
		if _, err := s.q.CompensateReturnWithCredit(ctx, db.CompensateReturnWithCreditParams{
			UserID:      row.UserID.UUID,
			AmountCents: split.Credit,
			// i18n-exempt: a store_credit_entries.reason VALUE, not chrome —
			// written once and read forever, so it cannot follow a reader.
			Reason:   "退貨退回購物金",
			OrderID:  row.OrderID,
			ReturnID: row.ID.String(),
			Actor:    actor,
		}); err != nil {
			return fmt.Errorf("%w: compensate return %s with credit: %s",
				ErrRefundIncomplete, row.ID, err.Error())
		}
		// The ledger post is synchronous and committed: this money has left even
		// though there is no provider reference to put in the event note.
		moved = true
	}
	// Written once THIS pass moves money, which is why it cannot ride in the
	// claiming transaction. Widening this to an already-settled half would
	// duplicate this bare INSERT on every resume.
	if moved {
		if err := s.q.RecordOrderEvent(ctx, db.RecordOrderEventParams{
			OrderID: row.OrderID, Kind: "refunded", ActorUserID: actor,
			Note: text(providerRef),
		}); err != nil {
			return fmt.Errorf("record refunded event for return %s: %w", row.ID, err)
		}
	}
	// The retry path passes only the outstanding half. Therefore zero here also
	// means that source settled on an earlier attempt; if this attempt returned
	// without error and no card is still pending, the whole payout has landed.
	// Use the return's full refundable amount, not the retry remainder, or a
	// split refund whose second half resumes would claw back only that last half.
	// This follows the timeline insert: money already moved even if the separate
	// points posting fails, and its customer-visible event must not disappear.
	if payoutDone {
		if err := s.reverseReturnPoints(ctx, row); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) returnPointsOutstanding(ctx context.Context, row *db.ReturnForDecisionRow) (bool, error) {
	if !row.UserID.Valid || row.RefundableCents <= 0 {
		return false, nil
	}
	outstanding, err := s.q.ReturnPointsOutstanding(ctx, db.ReturnPointsOutstandingParams{
		OrderID:       row.OrderID,
		ReturnID:      row.ID,
		RefundedCents: row.RefundableCents,
	})
	if err != nil {
		return false, fmt.Errorf("read points payout for return %s: %w", row.ID, err)
	}
	return outstanding, nil
}

// reverseReturnPoints finishes the third, idempotent half of a return payout.
// It deliberately receives the return's full refundable amount: a resumed
// split payout carries only the still-outstanding money in refundSplit, while
// the points calculation describes the return as a whole.
func (s *Store) reverseReturnPoints(ctx context.Context, row *db.ReturnForDecisionRow) error {
	if !row.UserID.Valid || row.RefundableCents <= 0 {
		return nil
	}
	if _, err := s.q.ReverseReturnPoints(ctx, db.ReverseReturnPointsParams{
		OrderID:       row.OrderID,
		ReturnID:      row.ID,
		RefundedCents: row.RefundableCents,
	}); err != nil {
		return fmt.Errorf("%w: reverse points for return %s: %w",
			ErrRefundIncomplete, row.ID, err)
	}
	return nil
}

// refundRequestKey is goen's own idempotency key for a return's refund. Derived
// from the RETURN rather than generated, which is what makes a retry find the
// same row through open_refund and the same refund at Stripe.
func refundRequestKey(returnID uuid.UUID) string { return "return:" + returnID.String() }

// refundCard is the two-transaction dance around the provider. It answers with
// what the provider SAID: a refund Stripe accepted may be 'pending' or
// 'requires_action', and recording either as succeeded asserts money moved.
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
		return "", "", fmt.Errorf("%w: %w", ErrRefused, openErr)
	}

	// Committed. Now the provider.
	intentID, err := s.refunder.PaymentIntentFor(ctx, row.ProviderRef.String)
	if err != nil {
		return "", "", fmt.Errorf("resolve payment intent for return %s: %w", row.ID, err)
	}
	providerRef, state, err := s.refunder.Refund(ctx, intentID, requestKey, cents)
	if err != nil {
		// UNKNOWN IS NOT FAILURE: 'failed' drops the row out of refunds_guard's
		// sum, so an ambiguous transport error marked failed would let the same
		// allowance be claimed twice. Only a decision from Stripe is terminal.
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

	if err := s.q.SettleRefund(ctx, db.SettleRefundParams{
		RequestKey:  requestKey,
		ProviderRef: providerRef,
		Status:      string(state),
	}); err != nil {
		return "", "", fmt.Errorf("settle refund for return %s: %w", row.ID, err)
	}

	switch state {
	case RefundSucceeded, RefundPending, RefundRequiresAction:
		// Stripe ACCEPTED it, so the return is approved either way: the shop took
		// the goods back, and that decision is not Stripe's to make pending.
		return providerRef, state, nil
	case RefundFailed, RefundCancelled:
		// Terminal and no money moved. A return is decided once, so closing it
		// here would leave a settled return, an unpaid customer and no door back.
		return "", state, fmt.Errorf(
			"%w: the provider reports the refund as %s, so no money left — the return stays open",
			ErrRefused, state)
	default:
		panic("admin: unknown RefundState: " + string(state))
	}
}

// closeReturn stamps the decision and appends to the order's history, together.
func (s *Store) closeReturn(
	ctx context.Context,
	requestID uuid.UUID,
	status, resolution string,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin return decision: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	// The row count is the decision. Decide's pre-check runs on the pool outside
	// this transaction, so the statement's own `status = 'requested'` is what
	// settles which of two staff members deciding at once wins — and this now
	// runs BEFORE the money, because settling it afterwards meant the loser had
	// already refunded.
	decided, decideErr := q.DecideReturn(ctx, db.DecideReturnParams{
		ID: requestID, Status: status, Resolution: text(resolution),
	})
	if decideErr != nil {
		return fmt.Errorf("%w: %w", ErrRefused, decideErr)
	}
	if decided == 0 {
		return fmt.Errorf("%w: return %s was decided by somebody else first",
			ErrRefused, requestID)
	}
	if err := auditIn(ctx, q, Event{
		Action: ActionDecideReturn, Table: "return_requests", ID: nullableID(requestID),
		After: map[string]any{"decision": status, "resolution": resolution},
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
	// customer said they were sending.
	Received int32
	// Restocked is how many of them went back on the shelf; Note says why the
	// rest did not.
	Restocked int32
	Note      string
}

// checkInspection refuses counts return_request_lines_restocked_bounded would
// refuse anyway, so a staff member gets a sentence instead of a constraint name.
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

// InspectReturn records what came back and puts the sellable units on the
// shelf, the whole parcel in ONE transaction. The restock is read back from
// what this transaction WROTE, so the set acted on is the one the database
// agreed to.
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
			return fmt.Errorf("%w: %w", ErrRefused, inspectErr)
		}
		// Zero rows is one of three refusals the caller has to hear: not
		// approved, the line belongs elsewhere, or already inspected.
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
			// Per (request, line), so a resubmitted form posts one movement.
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
		// not reach it.
		After: map[string]any{"lines": len(lines), "restocked": len(restock)},
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit return inspection: %w", err)
	}
	return nil
}

// CompleteReturn closes an inspected return. It writes no stock: the movement
// was posted with the INSPECTION, which is when the goods went back on the
// shelf.
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
	// un-inspected.
	closed, err := q.CompleteReturn(ctx, db.CompleteReturnParams{
		ID: requestID, Resolution: resolution,
	})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrRefused, err)
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

// GrantCredit puts store credit on a customer's account. The amount is in cents
// and must be positive: a correction is its own posting with its own reason, so
// the ledger reads as a history rather than a figure somebody edited.
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

	// The key includes the amount and reason, because granting the same customer
	// 500 twice for different reasons is two grants and not a repeat.
	key := "grant:" + user.ID.String() + ":" +
		strconv.FormatInt(amountCents, 10) + ":" + reason
	err = s.audited(ctx, Event{
		Action: ActionGrantCredit, Table: "store_credit_entries", ID: nullableID(user.ID),
		// The customer is named by ID and never by address. audit_events is
		// append-only and erase_user does not reach it, so an email written
		// here outlives the erasure meant to remove it — which is the reason
		// this file already gives for an audit row naming FIELDS rather than
		// their values, and the privacy policy promises the address goes.
		// The row already carries user.ID above.
		Before: nil, After: map[string]any{"amount_cents": amountCents, "reason": reason},
	},
		func(ctx context.Context, q *db.Queries) error {
			if _, postErr := q.PostStoreCredit(ctx, db.PostStoreCreditParams{
				UserID:         user.ID,
				AmountCents:    amountCents,
				Reason:         reason,
				IdempotencyKey: key,
				ActorUserID:    actor,
			}); postErr != nil {
				return fmt.Errorf("%w: %w", ErrRefused, postErr)
			}
			// Read INSIDE the same transaction, so the number shown is the one
			// this grant produced and not one a concurrent spend moved.
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

// MovementPageSize bounds one page of a variant's stock ledger. The running
// total is computed over the WHOLE ledger, so a page is still truthful.
const MovementPageSize = 50

// Movements reads one variant's stock ledger: which sale, which return, which
// hand adjustment, and by whom.
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

// IssueInvoice files a uniform invoice for an order. The audit row names the
// DOCUMENT and never the customer's details: audit_events is append-only and
// erase_user does not reach it.
func (s *Store) IssueInvoice(ctx context.Context, number string) error {
	if s.invoices == nil {
		return fmt.Errorf("%w: no e-invoice provider is configured", ErrRefused)
	}
	doc, err := s.invoices.Issue(ctx, number)
	if err != nil {
		return err
	}
	return s.audited(ctx, Event{
		Action: ActionIssueInvoice, Table: "invoice_documents", ID: uuid.NullUUID{},
		After: map[string]any{"order": number, "invoice": doc.Number},
	}, func(context.Context, *db.Queries) error { return nil })
}

// ReconcilePayment records that somebody dealt with an event goen accepted and
// could not act on — refunded it by hand at the provider, which is the only
// thing that can be done about money against a cancelled order.
//
// The row keeps its reason. Clearing the flag would delete what happened, and
// what happened is the part worth reading afterwards.
func (s *Store) ReconcilePayment(ctx context.Context, eventID string, actor uuid.NullUUID) error {
	if strings.TrimSpace(eventID) == "" {
		return ErrInvalid
	}
	return s.audited(ctx, Event{
		Action: ActionReconcilePayment, Table: "payment_webhook_events", ID: uuid.NullUUID{},
		After: map[string]any{"event": eventID},
	}, func(ctx context.Context, q *db.Queries) error {
		n, err := q.MarkPaymentReconciled(ctx, eventID)
		if err != nil {
			return fmt.Errorf("mark %s reconciled: %w", eventID, err)
		}
		if n == 0 {
			// Nothing outstanding under that id: already dealt with, or never
			// flagged. The row count is the answer, not a read beforehand.
			return ErrNotFound
		}
		return nil
	})
}

// AllowInvoice files a 折讓 against an order's live invoice, relieving the part
// of the sale that was refunded. A void is for an invoice that should not exist;
// an allowance is for one that should exist for less.
func (s *Store) AllowInvoice(ctx context.Context, number string, amountCents int64) error {
	if s.invoices == nil {
		return fmt.Errorf("%w: no e-invoice provider is configured", ErrRefused)
	}
	doc, err := s.invoices.Allowance(ctx, number, amountCents)
	if err != nil {
		return err
	}
	return s.audited(ctx, Event{
		Action: ActionAllowInvoice, Table: "invoice_documents", ID: uuid.NullUUID{},
		After: map[string]any{"order": number, "allowance": doc.Number, "amount_cents": amountCents},
	}, func(context.Context, *db.Queries) error { return nil })
}

// VoidInvoice cancels an order's live invoice, at the e-invoice provider and here.
func (s *Store) VoidInvoice(ctx context.Context, number, reason string) error {
	if s.invoices == nil {
		return fmt.Errorf("%w: no e-invoice provider is configured", ErrRefused)
	}
	if err := s.invoices.Void(ctx, number, reason); err != nil {
		return err
	}
	return s.audited(ctx, Event{
		Action: ActionVoidInvoice, Table: "invoice_documents", ID: uuid.NullUUID{},
		After: map[string]any{"order": number, "reason": reason},
	}, func(context.Context, *db.Queries) error { return nil })
}
