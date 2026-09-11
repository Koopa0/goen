package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/pickup"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

// InvoiceReader reads the uniform invoices filed for an order.
type InvoiceReader interface {
	Documents(ctx context.Context, orderNumber string) ([]invoice.Document, error)
}

// InvoiceWriter files and changes uniform invoices for the back office. A
// successful method has durably recorded its audit event in the same local
// transaction as the invoice transition.
type InvoiceWriter interface {
	Issue(ctx context.Context, orderNumber string) (invoice.Document, error)
	Void(ctx context.Context, orderNumber, reason string) error
	// Allowance relieves the authoritative whole-dollar refunded delta of a live
	// invoice. The provider boundary derives money under a database lock; this
	// consumer supplies only the aggregate and operation identities.
	Allowance(ctx context.Context, orderNumber string, operationID uuid.UUID) (invoice.Document, error)
}

var (
	_ InvoiceReader = (*invoice.Store)(nil)
	_ InvoiceWriter = (*invoice.Store)(nil)
)

// Store reads and writes through the ADMIN pool, which assumes the admin role.
type Store struct {
	pool     *pgxpool.Pool
	q        *db.Queries
	refunder Refunder
	// Both invoice dependencies may be nil when no provider is configured.
	invoiceReader InvoiceReader
	invoiceWriter InvoiceWriter
}

// NewStore returns a Store over the admin pool. refunder may be one that
// refuses: a back office without Stripe credentials can still decide returns,
// and a refund it cannot pay must fail loudly.
func NewStore(pool *pgxpool.Pool, refunder Refunder, reader InvoiceReader, writer InvoiceWriter) *Store {
	if pool == nil || refunder == nil {
		panic("admin: NewStore requires a pool and a refunder")
	}
	if (reader == nil) != (writer == nil) {
		panic("admin: NewStore requires both invoice dependencies or neither")
	}
	return &Store{
		pool:          pool,
		q:             db.New(pool),
		refunder:      refunder,
		invoiceReader: reader,
		invoiceWriter: writer,
	}
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
func (s *Store) Orders(ctx context.Context, status pages.FulfillmentStatus, term string) (pages.AdminOrdersView, error) {
	term = strings.TrimSpace(term)
	searched := utf8.RuneCountInString(term) >= MinSearchRunes
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
		rows, err = s.q.AdminOrders(ctx, db.AdminOrdersParams{Status: string(status), RowLimit: PageSize})
	}
	if err != nil {
		return pages.AdminOrdersView{}, fmt.Errorf("read orders: %w", err)
	}
	counts, err := s.q.AdminOrderCounts(ctx)
	if err != nil {
		return pages.AdminOrdersView{}, fmt.Errorf("read order counts: %w", err)
	}

	view := pages.AdminOrdersView{Status: status, Term: term, Searched: searched}
	countsByStatus := make(map[pages.FulfillmentStatus]int64, len(counts))
	var total int64
	for _, c := range counts {
		countsByStatus[pages.FulfillmentStatus(c.FulfillmentStatus)] = c.N
		total += c.N
	}
	view.Tabs = make([]pages.AdminStatusTab, 0, len(statuses)+1)
	view.Tabs = append(view.Tabs, pages.AdminStatusTab{
		Label: i18n.T(ctx, i18n.KeyAdminTabAll), Count: total, Selected: status == "",
	})
	for _, definition := range statuses {
		view.Tabs = append(view.Tabs, pages.AdminStatusTab{
			Value: definition.value, Label: i18n.T(ctx, definition.label),
			Count: countsByStatus[definition.value], Selected: definition.value == status,
		})
	}
	for i := range rows {
		o := &rows[i]
		fulfillment := pages.FulfillmentStatus(o.FulfillmentStatus)
		view.Orders = append(view.Orders, pages.AdminOrderRow{
			Number:     o.OrderNumber,
			Status:     fulfillment,
			StatusText: FundedStatusLabel(ctx, fulfillment, o.Committed, o.OwedCents),
			PlacedAt:   shoptime.Minute(o.PlacedAt),
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

	fulfillment := pages.FulfillmentStatus(o.FulfillmentStatus)
	view := pages.AdminOrderView{
		Number: o.OrderNumber, Status: fulfillment,
		StatusText:    FundedStatusLabel(ctx, fulfillment, o.Committed, o.OwedCents),
		PlacedAt:      shoptime.Minute(o.PlacedAt),
		ShippingName:  o.ShippingMethodName,
		SubtotalCents: o.SubtotalCents, ShippingCents: o.ShippingCents,
		DiscountCents: o.DiscountCents, DiscountReason: o.DiscountReason, TaxCents: o.TaxCents,
		Email: o.Email, Recipient: o.RecipientName, Phone: o.Phone,
		Address: pages.Delivery{
			PostalCode: o.PostalCode, City: o.City, District: o.District, Street: o.Street,
			PickupBrand: pickup.Brand(o.PickupBrand), PickupStoreCode: o.PickupStoreCode,
			PickupStoreName: o.PickupStoreName,
		}.Line(),
		Delivery: pages.AdminDelivery{
			Email: o.Email, Recipient: o.RecipientName, Phone: o.Phone,
			PostalCode: o.PostalCode, City: o.City,
			District: o.District, Street: o.Street,
			PickupBrand: pickup.Brand(o.PickupBrand), PickupStoreCode: o.PickupStoreCode,
			PickupStoreName: o.PickupStoreName,
		},
		// UpdateOrderDelivery's WHERE clause is the authority; this only decides
		// whether to offer the form.
		Correctable: fulfillment != pages.FulfillmentShipped &&
			fulfillment != pages.FulfillmentDelivered && fulfillment != pages.FulfillmentCompleted,
		PickupDestination: o.PickupStoreCode != "",
		PickupBrands:      pages.PickupBrandChoices(),
		CustomerNote:      o.CustomerNote.String,
		StaffNote:         o.StaffNote.String,
		InvoiceType:       invoice.Preference(o.InvoiceType),
		InvoiceCarrier:    o.InvoiceCarrier,
		InvoiceTaxID:      o.InvoiceTaxID,
		Committed:         o.Committed,
	}

	if shipErr := s.fillShippable(ctx, &view, o.ID, fulfillment); shipErr != nil {
		return pages.AdminOrderView{}, shipErr
	}

	if invErr := s.fillInvoices(ctx, &view, number); invErr != nil {
		return pages.AdminOrderView{}, invErr
	}
	for _, n := range NextStatuses(fulfillment) {
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
			At: shoptime.Minute(e.OccurredAt), Actor: e.ActorName,
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
			ShippedAt:   shoptime.Minute(sh.ShippedAt),
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
func (s *Store) Advance(ctx context.Context, number string, status pages.FulfillmentStatus, actor uuid.NullUUID) ([]string, error) {
	if !status.Known() {
		return nil, ErrRefused
	}
	// Ship is the only door to 'shipped', because a dispatch also records the
	// carrier and settles the held stock.
	if status == pages.FulfillmentShipped {
		return nil, ErrRefused
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin advance: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	row, err := q.OrderIDByNumber(ctx, number)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	if advanceErr := q.AdvanceOrder(ctx, db.AdvanceOrderParams{
		OrderNumber: number, Status: string(status),
	}); advanceErr != nil {
		return nil, fmt.Errorf("%w: %w", ErrRefused, advanceErr)
	}
	// AdvanceOrder's UPDATE owns the aggregate row before this snapshot. If an
	// expiry release won the order lock first, we now see no held row; if this
	// transition won, that release waits behind us. A pre-lock snapshot can go
	// stale and make an otherwise valid cancellation roll back.
	var held []uuid.UUID
	if status == pages.FulfillmentCancelled {
		if held, err = q.HeldReservationsForOrder(ctx, number); err != nil {
			return nil, fmt.Errorf("read holds of %s: %w", number, err)
		}
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
		Action: actionAdvanceOrder, Table: "orders", ID: nullableID(row.ID),
		Before: nil, After: map[string]any{"number": number, "status": string(status)},
	}); err != nil {
		return nil, err
	}
	// Read inside the transaction so the set handed back is the one this
	// transaction saw, not what a later read finds after a webhook moved a row.
	var sessions []string
	if status == pages.FulfillmentCancelled {
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
	status  pages.FulfillmentStatus
	number  string
	orderID uuid.UUID
	// held is the order's live reservations, read after the status UPDATE took
	// the aggregate lock.
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
<<<<<<< HEAD
	case pages.FulfillmentPending, pages.FulfillmentPicking, pages.FulfillmentShipped:
		// These moves only release the holds already walked above.
	case pages.FulfillmentCancelled:
=======
	case "picking":
		if err := payment.CompleteFunding(ctx, q, e.orderID, e.number, payment.Capture{}); err != nil {
			return fmt.Errorf("complete funding for %s: %w", e.number, err)
		}
	case "cancelled":
>>>>>>> 28971eb (Run funding-complete side effects for zero-owed orders)
		// The customer's own cancellation does this too; the back office
		// cancelling on their behalf must not be the path that keeps their credit.
		if _, err := q.ReverseOrderCredit(ctx, e.orderID); err != nil {
			return fmt.Errorf("return store credit spent on %s: %w", e.number, err)
		}
		if _, err := q.ReverseOrderPoints(ctx, e.orderID); err != nil {
			return fmt.Errorf("claw back loyalty earned on %s: %w", e.number, err)
		}
	case pages.FulfillmentDelivered, pages.FulfillmentCompleted:
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
func eventKindFor(status pages.FulfillmentStatus) string {
	switch status {
	case pages.FulfillmentPicking:
		return "picking"
	case pages.FulfillmentShipped:
		return "shipped"
	case pages.FulfillmentDelivered:
		return "delivered"
	case pages.FulfillmentCompleted:
		return "completed"
	case pages.FulfillmentCancelled:
		return "cancelled"
	default:
		panic("admin: no order_events kind for fulfilment status " + string(status))
	}
}

// fillInvoices puts what has actually been FILED on the order page, which is a
// different question from the preference the customer asked for at checkout.
func (s *Store) fillInvoices(ctx context.Context, view *pages.AdminOrderView, number string) error {
	if s.invoiceReader == nil {
		return nil
	}
	view.InvoicingEnabled = true
	docs, err := s.invoiceReader.Documents(ctx, number)
	if err != nil {
		return err
	}
	for i := range docs {
		d := &docs[i]
		doc := pages.AdminInvoiceDocument{
			Kind: d.Kind, Number: d.Number, ProviderRef: d.ProviderRef,
			AmountCents: d.AmountCents, Status: d.Status,
			IssuedAt: shoptime.Minute(d.IssuedAt),
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
	ctx context.Context, view *pages.AdminOrderView, orderID uuid.UUID, status pages.FulfillmentStatus,
) error {
	// DELIVERED must stay in this set. orders_legal_transition permits
	// shipped -> delivered while a line is still outstanding, deliberately:
	// delivered is a fact about what WENT OUT. Without the dispatch form there,
	// the order wedges — orders_finished_when_shipped refuses 'completed' and the
	// remaining line's hold is stranded, since release_reservation refuses a
	// committed order and ExpiredReservations excludes it.
	if status != pages.FulfillmentPicking && status != pages.FulfillmentShipped &&
		status != pages.FulfillmentDelivered {
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
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	row, err := q.OrderIDByNumber(ctx, number)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrRefused, err)
	}
	// A parcel is only recorded for an order that has entered fulfilment. This is
	// the same set fillShippable renders the form for; the trigger
	// shipment_order_in_fulfilment is the authority, and this is the sentence.
	fulfillment := pages.FulfillmentStatus(row.FulfillmentStatus)
	switch fulfillment {
	case pages.FulfillmentPicking, pages.FulfillmentShipped, pages.FulfillmentDelivered:
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
	if fulfillment == pages.FulfillmentPicking {
		if advErr := q.AdvanceOrder(ctx, db.AdvanceOrderParams{
			OrderNumber: number, Status: string(pages.FulfillmentShipped),
		}); advErr != nil {
			return fmt.Errorf("%w: %w", ErrRefused, advErr)
		}
	}

	// One notice per PARCEL: the dedupe key is carrier and tracking together,
	// which is what order_shipments is unique on, not the order. An order in two
	// parcels is still two notices.
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
		Action: actionShipOrder, Table: "orders", ID: nullableID(row.ID),
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
		Action: actionAdjustStock, Table: "product_variants", ID: nullableID(v.ID),
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
		Action: actionReceiveStock, Table: "product_variants", ID: nullableID(v.ID),
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
		Action: actionRetireVariant, Table: "product_variants", ID: nullableID(v.ID),
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
		Action: actionRepriceVariant, Table: "product_variants", ID: nullableID(v.ID),
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

// GrantCredit puts store credit on a customer's account. The amount is in cents
// and must be positive: a correction is its own posting with its own reason, so
// the ledger reads as a history rather than a figure somebody edited.
func (s *Store) GrantCredit(ctx context.Context, email string, amountCents int64, reason string, operationID uuid.UUID) (balanceCents int64, err error) {
	email, reason = strings.TrimSpace(email), strings.TrimSpace(reason)
	if email == "" || reason == "" || utf8.RuneCountInString(reason) > MaxCreditReasonRunes || amountCents <= 0 || operationID == uuid.Nil {
		return 0, ErrInvalid
	}
	if amountCents > MaxCreditGrant {
		return 0, ErrInvalid
	}
	// The authenticated request context, not a parallel caller argument, owns
	// ledger attribution. The database role is shared by all staff requests, so
	// this is the last trustworthy per-person boundary before the role-specific
	// posting function verifies that the durable user is still staff/admin.
	actorID, ok := actorFrom(ctx)
	if !ok {
		return 0, ErrNoActor
	}
	user, err := s.q.CustomerByEmail(ctx, email)
	if err != nil {
		return 0, fmt.Errorf("%w: no customer for %s", ErrRefused, email)
	}

	event := Event{
		Action: actionGrantCredit, Table: "store_credit_entries", ID: nullableID(user.ID),
		// The customer is named by ID and never by address: audit_events is
		// append-only and erase_user does not reach it, so an email written here
		// would outlive the erasure meant to remove it.
		Before: nil, After: map[string]any{"amount_cents": amountCents, "reason": reason},
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin %s: %w", event.Action, err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)
	entryID, err := q.PostStoreCredit(ctx, db.PostStoreCreditParams{
		UserID: user.ID, AmountCents: amountCents, Reason: reason,
		ActorUserID: actorID, OperationID: operationID,
	})
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	// A retry of the same durable request observes the original posting and must
	// not manufacture a second audit row claiming money moved again.
	if entryID != uuid.Nil {
		if auditErr := auditIn(ctx, q, event); auditErr != nil {
			return 0, auditErr
		}
	}
	// Read INSIDE the same transaction, so the number shown is the one this grant
	// produced and not one a concurrent spend moved.
	balanceCents, err = q.CreditBalance(ctx, uuid.NullUUID{UUID: user.ID, Valid: true})
	if err != nil {
		return 0, fmt.Errorf("read credit balance: %w", err)
	}
	if commitErr := tx.Commit(ctx); commitErr != nil {
		return 0, fmt.Errorf("commit %s: %w", event.Action, commitErr)
	}
	return balanceCents, nil
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
			At:          shoptime.Minute(r.CreatedAt),
		})
	}
	return view, nil
}

// nullableStamp formats a timestamp that may be absent.
func nullableStamp(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return shoptime.Minute(t.Time)
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
			At:          shoptime.Minute(m.CreatedAt),
			Delta:       m.Delta,
			Reason:      m.Reason,
			OrderNumber: m.OrderNumber,
			Actor:       m.Actor,
			Running:     m.RunningTotal,
		})
	}
	return view, nil
}

// IssueInvoice files a uniform invoice for an order. The writer owns the local
// persistence transaction, including its audit row; a second no-op audited
// transaction here would let either half commit without the other.
func (s *Store) IssueInvoice(ctx context.Context, number string) error {
	if s.invoiceWriter == nil {
		return fmt.Errorf("%w: no e-invoice provider is configured", ErrRefused)
	}
	filingCtx, err := invoiceFilingContext(ctx)
	if err != nil {
		return err
	}
	_, err = s.invoiceWriter.Issue(filingCtx, number)
	return err
}

// ReleasePaymentEventAfterRefundOrAccounting records the only safe release
// conclusion for an event goen accepted and could not apply: provider money was
// fully refunded, or a succeeded local payment already accounts for it. Merely
// inspecting an event must not terminate its linked attempt and open another
// place to charge.
//
// The row keeps its reason. Clearing the flag would delete what happened, and
// what happened is the part worth reading afterwards.
func (s *Store) ReleasePaymentEventAfterRefundOrAccounting(
	ctx context.Context, eventID string,
) error {
	if strings.TrimSpace(eventID) == "" {
		return ErrInvalid
	}
	return s.audited(ctx, Event{
		Action: actionReconcilePayment, Table: "payment_webhook_events", ID: uuid.NullUUID{},
		After: map[string]any{
			"event":      eventID,
			"resolution": "fully_refunded_or_already_accounted",
		},
	}, func(ctx context.Context, q *db.Queries) error {
		reconciled, err := q.ReleasePaymentEvent(ctx, eventID)
		if err != nil {
			return fmt.Errorf("mark %s reconciled: %w", eventID, err)
		}
		if !reconciled {
			// Nothing outstanding under that id: already dealt with, or never
			// flagged. The row count is the answer, not a read beforehand.
			return ErrNotFound
		}
		return nil
	})
}

// reconcileCompletePayment records one explicit money outcome for a provider-
// complete Session whose capture outcome was not represented by a flaggable
// webhook event. Paid attribution runs through capture_payment and all of its
// side effects; only confirmed-unpaid/fully-refunded releases a later attempt.
// The provider ref remains distinct from ReconcilePayment's event-id identity.
func (s *Store) reconcileCompletePayment(
	ctx context.Context, providerRef string, resolution completePaymentResolution,
) error {
	if strings.TrimSpace(providerRef) == "" ||
		resolution == completePaymentResolutionUnknown {
		return ErrInvalid
	}

	event := Event{
		Action: actionReconcilePayment, Table: "payments", ID: uuid.NullUUID{},
		After: map[string]any{
			"provider_ref": providerRef,
			"resolution":   resolution.auditValue(),
		},
	}

	// Paid attribution has capture side effects supplied by internal/payment,
	// and the audit must share their transaction.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin %s: %w", event.Action, err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }() //nolint:errcheck // no-op after commit
	q := s.q.WithTx(tx)

	switch resolution {
	case completePaymentPaid:
		attributed, captureErr := payment.AttributeCompleteCapture(ctx, tx, providerRef)
		if captureErr != nil {
			if errors.Is(captureErr, payment.ErrReleasedStockRequiresRefund) {
				return fmt.Errorf("%w: %w", ErrPaymentRequiresRefund, captureErr)
			}
			return captureErr
		}
		if !attributed {
			return ErrNotFound
		}
	case completePaymentUnpaidOrRefunded:
		released, releaseErr := q.ReleaseCompletePayment(ctx, providerRef)
		if releaseErr != nil {
			return fmt.Errorf("release complete payment %s: %w", providerRef, releaseErr)
		}
		if !released {
			return ErrNotFound
		}
	default:
		return ErrInvalid
	}

	if err := auditIn(ctx, q, event); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit %s: %w", event.Action, err)
	}
	return nil
}

// AllowInvoice files a 折讓 against an order's live invoice, relieving the part
// of the sale that was refunded. A void is for an invoice that should not exist;
// an allowance is for one that should exist for less.
func (s *Store) AllowInvoice(
	ctx context.Context, number string, operationID uuid.UUID,
) error {
	if s.invoiceWriter == nil {
		return fmt.Errorf("%w: no e-invoice provider is configured", ErrRefused)
	}
	filingCtx, err := invoiceFilingContext(ctx)
	if err != nil {
		return err
	}
	_, err = s.invoiceWriter.Allowance(filingCtx, number, operationID)
	return err
}

// VoidInvoice cancels an order's live invoice, at the e-invoice provider and here.
func (s *Store) VoidInvoice(ctx context.Context, number, reason string) error {
	if s.invoiceWriter == nil {
		return fmt.Errorf("%w: no e-invoice provider is configured", ErrRefused)
	}
	filingCtx, err := invoiceFilingContext(ctx)
	if err != nil {
		return err
	}
	return s.invoiceWriter.Void(filingCtx, number, reason)
}

func invoiceFilingContext(ctx context.Context) (context.Context, error) {
	actorID, ok := actorFrom(ctx)
	if !ok {
		return nil, ErrNoActor
	}
	requestID := web.RequestID(ctx)
	if requestID == "" {
		return nil, ErrNoActor
	}
	return invoice.WithFilingIdentity(ctx, actorID, requestID), nil
}
