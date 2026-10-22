package orders

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/koopa0/goen/internal/order/delivery"
	"github.com/koopa0/goen/internal/shipping"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/credit"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/order/cancellation"
	"github.com/koopa0/goen/internal/order/notice"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/payment/refund"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/orderstatus"
	"github.com/koopa0/goen/internal/web/formtext"
	"github.com/koopa0/goen/internal/web/requestid"
)

// Refunds says what the order page offers of the refund before shipment and
// whether one was ever opened, and what it offers of the refund of units a
// partly shipped order will not send, given how many of the order's lines a
// dispatch still owes; the refunds desk implements it.
type Refunds interface {
	Offer(ctx context.Context, number string, owed int) (refund.Offer, error)
}

type Store struct {
	pool    *pgxpool.Pool
	q       *db.Queries
	refunds Refunds
	// The invoice writer is nil when no provider is configured; the reader
	// still shows what was filed.
	invoiceReader InvoiceReader
	invoiceWriter InvoiceWriter
}

func NewStore(pool *pgxpool.Pool, refunds Refunds, invoiceReader InvoiceReader, invoiceWriter InvoiceWriter) *Store {
	if pool == nil || refunds == nil || invoiceReader == nil {
		panic("orders: NewStore requires a pool, refunds and an invoice reader")
	}
	return &Store{pool: pool, q: db.New(pool), refunds: refunds, invoiceReader: invoiceReader, invoiceWriter: invoiceWriter}
}

// orderRows reads which of the orders are returned in one query, so a page of the queue costs no more than one.
func (s *Store) orderRows(ctx context.Context, rows []db.AdminOrdersRow) ([]OrderRow, error) {
	ids := make([]uuid.UUID, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
	}
	returned, err := s.q.ReturnedOrders(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("read returned orders: %w", err)
	}
	out := make([]OrderRow, len(rows))
	for i := range rows {
		out[i] = orderRow(ctx, &rows[i], slices.Contains(returned, rows[i].ID))
	}
	return out, nil
}

// orderRow is one order as the queue renders it; admin/dashboard's landing page
// reads the same word, colour and total for its newest orders. The total is
// read from order_totals, as on every page that states one.
func orderRow(ctx context.Context, o *db.AdminOrdersRow, returned bool) OrderRow {
	fulfillment := order.FulfillmentStatus(o.FulfillmentStatus)
	text, intent := orderstatus.Badge(ctx, fulfillment, o.Committed, o.OwedCents, returned)
	return OrderRow{
		Number:       o.OrderNumber,
		Status:       fulfillment,
		StatusText:   text,
		StatusIntent: intent,
		PlacedAt:     shoptime.Minute(o.PlacedAt),
		Recipient:    o.Recipient,
		TotalCents:   o.TotalCents,
	}
}

// listPosition is a reader's place in the orders queue. The query builds it as
// PageCursor, so its fields are the ordering values and nothing else.
type listPosition struct {
	At time.Time
	ID uuid.UUID
}

func (s *Store) List(ctx context.Context, status QueueFilter, term string, after ...string) (View, error) {
	term = strings.TrimSpace(term)
	scope := keyset.ScopeURL("/admin/orders", "q", term, "status", string(status))
	from, resumed := keyset.Resume(scope, after, func(p listPosition) bool { return p.ID != uuid.Nil })
	searched := utf8.RuneCountInString(term) >= formtext.MinSearchRunes
	var rows []db.AdminOrdersRow
	var err error
	if searched {
		// A search ignores the status filter: somebody on the phone wants that
		// order, not that order if it is in the tab they had open.
		var found []db.AdminSearchOrdersRow
		if found, err = s.q.AdminSearchOrders(ctx, db.AdminSearchOrdersParams{HasCursor: resumed, AfterAt: from.At, AfterID: from.ID,
			Term: term, EscapedTerm: db.EscapeLike(term), RowLimit: keyset.PageLimit,
		}); err == nil {
			rows = make([]db.AdminOrdersRow, 0, len(found))
			// A conversion, not a field copy: it stops compiling when the two
			// queries' columns drift, so a new column cannot be dropped silently.
			for i := range found {
				rows = append(rows, db.AdminOrdersRow(found[i]))
			}
		}
	} else {
		filter, funding := string(status), ""
		switch status {
		case QueueAwaitingPayment:
			filter, funding = string(order.FulfillmentPending), "unpaid"
		case QueueReady:
			filter, funding = string(order.FulfillmentPending), "funded"
		default: // a fulfilment status filters by itself
		}
		rows, err = s.q.AdminOrders(ctx, db.AdminOrdersParams{HasCursor: resumed, AfterAt: from.At, AfterID: from.ID, Status: filter, Funding: funding, RowLimit: keyset.PageLimit})
	}
	if err != nil {
		return View{}, fmt.Errorf("read orders: %w", err)
	}
	counts, err := s.q.AdminOrderCounts(ctx)
	if err != nil {
		return View{}, fmt.Errorf("read order counts: %w", err)
	}

	// Both branches asked for one row more than the page shows, so the drop is
	// here rather than in each of them. The tab counts above come from
	// AdminOrderCounts and not from len(rows), so the extra row was never in
	// them to begin with.
	rows, bound := keyset.PageBound(scope, resumed, rows, keyset.PageSize, func(r *db.AdminOrdersRow) string { return r.PageCursor })
	view := View{
		Bound:  bound,
		Status: status, Term: term, Searched: searched,
	}
	countsByFilter := make(map[QueueFilter]int64, len(counts))
	var total int64
	for _, c := range counts {
		key := QueueFilter(c.FulfillmentStatus)
		switch {
		case c.FulfillmentStatus == string(order.FulfillmentPending) && c.Funded:
			key = QueueReady
		case c.FulfillmentStatus == string(order.FulfillmentPending):
			key = QueueAwaitingPayment
		}
		countsByFilter[key] += c.N
		total += c.N
	}
	view.Tabs = make([]StatusTab, 0, len(queueTabs)+1)
	view.Tabs = append(view.Tabs, StatusTab{
		Label: i18n.T(ctx, i18n.KeyAdminTabAll), Count: total, Selected: status == QueueAll,
	})
	for _, tab := range queueTabs {
		view.Tabs = append(view.Tabs, StatusTab{
			Value: tab.filter, Label: i18n.T(ctx, tab.label),
			Count: countsByFilter[tab.filter], Selected: tab.filter == status,
		})
	}
	if view.Orders, err = s.orderRows(ctx, rows); err != nil {
		return View{}, err
	}
	return view, nil
}

func (s *Store) Order(ctx context.Context, number string) (OrderView, error) {
	o, err := s.q.AdminOrderByNumber(ctx, db.AdminOrderByNumberParams{
		OrderNumber: number, Locale: i18n.FromContext(ctx).Tag(),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OrderView{}, ErrNotFound
		}
		return OrderView{}, fmt.Errorf("read order: %w", err)
	}
	lines, err := s.q.OrderLinesByOrder(ctx, db.OrderLinesByOrderParams{OrderID: o.ID, Locale: i18n.FromContext(ctx).Tag()})
	if err != nil {
		return OrderView{}, fmt.Errorf("read order lines: %w", err)
	}

	returned, err := s.q.ReturnedOrders(ctx, []uuid.UUID{o.ID})
	if err != nil {
		return OrderView{}, fmt.Errorf("read returned order: %w", err)
	}
	fulfillment := order.FulfillmentStatus(o.FulfillmentStatus)
	statusText, statusIntent := orderstatus.Badge(ctx, fulfillment, o.Committed, o.OwedCents, len(returned) > 0)
	to := delivery.Details{
		PostalCode: o.PostalCode, City: o.City, District: o.District, Street: o.Street,
		PickupChain: shipping.Chain(o.PickupChain), PickupStoreCode: o.PickupStoreCode,
		PickupStoreName: o.PickupStoreName,
	}
	view := OrderView{
		Number: o.OrderNumber, Status: fulfillment,
		StatusText:    statusText,
		StatusIntent:  statusIntent,
		PlacedAt:      shoptime.Minute(o.PlacedAt),
		ShippingName:  o.ShippingMethodName,
		SubtotalCents: o.SubtotalCents, ShippingCents: o.ShippingCents,
		DiscountCents: o.DiscountCents, DiscountReason: o.DiscountReason,
		TotalCents: o.TotalCents,
		Email:      o.Email, Recipient: o.RecipientName, Phone: o.Phone,
		Address: to.Line(),
		Delivery: Delivery{
			Email: o.Email, Recipient: o.RecipientName, Phone: o.Phone,
			PostalCode: o.PostalCode, City: o.City,
			District: o.District, Street: o.Street,
			PickupChain: shipping.Chain(o.PickupChain), PickupStoreCode: o.PickupStoreCode,
			PickupStoreName: o.PickupStoreName,
		},
		// UpdateOrderDelivery's WHERE clause is the authority; this only decides
		// whether to offer the form.
		Correctable:          correctable(fulfillment),
		PickupDestination:    o.PickupChain != "",
		PickupChains:         shipping.Chains(),
		CustomerNote:         o.CustomerNote.String,
		StaffNote:            o.StaffNote.String,
		InvoiceType:          invoice.Preference(o.InvoiceType),
		InvoiceMobileBarcode: o.InvoiceMobileBarcode,
		InvoiceDonationCode:  o.InvoiceDonationCode,
		InvoiceTaxID:         o.InvoiceTaxID,
		Committed:            o.Committed,
		Funded:               funded(o.Committed, o.OwedCents, o.CreditCents),
		Unpaid:               !o.Committed && o.OwedCents > 0,
		OwedCents:            o.OwedCents,
		CreditCents:          o.CreditCents,
	}

	carriers, implied := shipping.CarriersFor(shipping.Chain(o.PickupChain), shipping.Destination(o.DestinationKind))
	view.ShipCarriers, view.ShipCarrier = carriers, string(implied)

	if shipErr := s.fillShippable(ctx, &view, o.ID, fulfillment); shipErr != nil {
		return OrderView{}, shipErr
	}

	if invErr := s.fillInvoices(ctx, &view, number); invErr != nil {
		return OrderView{}, invErr
	}
	if refundErr := s.fillRefundBeforeShipment(ctx, &view, number); refundErr != nil {
		return OrderView{}, refundErr
	}
	if payErr := s.fillPayments(ctx, &view, o.ID); payErr != nil {
		return OrderView{}, payErr
	}
	for i := range lines {
		l := &lines[i]
		photo := assets.Image{Digest: l.ImageDigest, BundledKey: l.ImageBundledKey, Width: int(l.ImageWidth)}
		view.Lines = append(view.Lines, Line{
			SKU: l.SKU, Name: l.ProductName, Label: l.VariantLabel.String,
			UnitCents: l.UnitPriceCents, Quantity: l.Quantity,
			ImageURL:    photo.URL(),
			ImageSrcset: photo.Srcset(),
			ImageAlt:    l.ImageAlt,
		})
	}

	timeline, err := s.q.AdminOrderTimeline(ctx, db.AdminOrderTimelineParams{
		OrderID: o.ID, MailTopics: orderMailTopics, InvoicingEnabled: view.InvoicingEnabled,
	})
	if err != nil {
		return OrderView{}, fmt.Errorf("read order timeline: %w", err)
	}
	for i := range timeline {
		view.Timeline = append(view.Timeline, timelineEntry(&timeline[i]))
	}
	view.MailKept = outbox.Retain

	shipments, err := s.q.OrderShipments(ctx, o.ID)
	if err != nil {
		return OrderView{}, fmt.Errorf("read order shipments: %w", err)
	}
	for i := range shipments {
		sh := &shipments[i]
		view.Shipments = append(view.Shipments, Shipment{
			ID: sh.ID.String(), Carrier: shipping.Carrier(sh.Carrier), Tracking: sh.TrackingNumber,
			ShippedAt:   shoptime.Minute(sh.ShippedAt),
			DeliveredAt: nullableStamp(sh.DeliveredAt),
		})
	}
	return view, nil
}

func correctable(status order.FulfillmentStatus) bool {
	switch status {
	case order.FulfillmentShipped, order.FulfillmentDelivered,
		order.FulfillmentCompleted, order.FulfillmentCancelled:
		return false
	default:
		return true
	}
}

// Advance moves an order along its lifecycle. orders_check_transition validates
// the move and its refusal is returned unreplaced, because it names the rule.
//
// The Checkout Sessions returned are a CANCELLATION's, for the caller to close
// at Stripe once this has committed; every other status returns none.
func (s *Store) Advance(ctx context.Context, number string, status order.FulfillmentStatus, actor uuid.NullUUID) ([]string, error) {
	kind, err := advanceKind(status)
	if err != nil {
		return nil, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin advance: %w", err)
	}
	defer db.Rollback(ctx, tx)
	q := s.q.WithTx(tx)

	row, err := q.LockOrderForAdvance(ctx, number)
	if err != nil {
		return nil, refusedIfNoRow(err, "lock order "+number)
	}
	// orders_check_transition lets a same-status UPDATE through, so a double
	// submit would otherwise record the step and its audit row twice.
	if order.FulfillmentStatus(row.FulfillmentStatus) == status {
		return nil, ErrRefused
	}
	if advanceErr := q.AdvanceOrder(ctx, db.AdvanceOrderParams{
		OrderNumber: number, Status: string(status),
	}); advanceErr != nil {
		return nil, db.WrapRefusal(advanceErr, ErrRefused)
	}
	if status == order.FulfillmentCancelled {
		// The database admits cancelling a paid order once its refund before
		// shipment has settled, but only RefundBeforeShipment closes that
		// return; the effects below would reverse the refunded credit and
		// points again. A pending order store credit paid in full is not
		// committed yet, and this branch would return its credit with no
		// confirmation.
		if funded(row.Committed, row.OwedCents, row.CreditCents) {
			return nil, ErrPaidCancel
		}
	}
	if err := applyStatusEffects(ctx, q, statusEffect{
		status: status, previous: order.FulfillmentStatus(row.FulfillmentStatus), number: number,
		orderID: row.ID, actor: actor,
	}); err != nil {
		return nil, err
	}
	// A cancellation records its own event.
	if status != order.FulfillmentCancelled {
		if err := q.RecordOrderEvent(ctx, db.RecordOrderEventParams{
			OrderID: row.ID, Kind: kind, ActorUserID: actor,
		}); err != nil {
			return nil, fmt.Errorf("record order event: %w", err)
		}
	}
	if err := audit.In(ctx, q, audit.Event{
		Action: audit.ActionAdvanceOrder, Table: "orders", ID: audit.EntityID(row.ID),
		Before: nil, After: audit.State{audit.FieldNumber: number, audit.FieldStatus: string(status)},
	}); err != nil {
		return nil, err
	}
	// Read inside the transaction so the set handed back is the one this
	// transaction saw, not what a later read finds after a webhook moved a row.
	var sessions []string
	if status == order.FulfillmentCancelled {
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

// recordShipment inserts the parcel. Ship's status check reads the order
// without a lock, so another staff member can move it on first;
// shipment_order_in_fulfilment re-reads it under the order's lock and refuses
// the parcel of an order that no longer takes one.
func recordShipment(ctx context.Context, q *db.Queries, orderID uuid.UUID, c shipping.Carrier, tracking string) (uuid.UUID, error) {
	id, err := q.CreateShipment(ctx, db.CreateShipmentParams{
		OrderID: orderID, Carrier: string(c), TrackingNumber: tracking,
	})
	if db.IsConstraint(err, "shipment_order_in_fulfilment") {
		return uuid.Nil, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("record shipment: %w", err)
	}
	return id, nil
}

// refusedIfNoRow reports a missing row as ErrRefused and any other error as the
// failure it is: a lock that timed out is the database not answering, not a
// rule refusing the write.
func refusedIfNoRow(err error, doing string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %w", ErrRefused, err)
	}
	return fmt.Errorf("%s: %w", doing, err)
}

func advanceKind(status order.FulfillmentStatus) (string, error) {
	// Ship is the only door to 'shipped', because a dispatch also records the
	// carrier and settles the held stock.
	if !status.Known() || status == order.FulfillmentShipped {
		return "", ErrRefused
	}
	return eventKindFor(status)
}

type statusEffect struct {
	status   order.FulfillmentStatus
	previous order.FulfillmentStatus
	number   string
	orderID  uuid.UUID
	actor    uuid.NullUUID
}

// applyStatusEffects does what a status move MEANS beyond the column — the
// stock release, the credit reversal, the parcel stamp — in the CALLER's
// transaction, so a status cannot disagree with what it implies.
func applyStatusEffects(ctx context.Context, q *db.Queries, e statusEffect) error {
	switch e.status {
	case order.FulfillmentPending, order.FulfillmentShipped:
	case order.FulfillmentPicking:
		if err := payment.CompleteFunding(ctx, q, e.orderID, e.number, payment.Capture{}); err != nil {
			return fmt.Errorf("complete funding for %s: %w", e.number, err)
		}
	case order.FulfillmentCancelled:
		_, err := cancellation.Settle(ctx, q, &cancellation.Order{
			ID: e.orderID, Number: e.number, Actor: e.actor, Kind: outbox.TerminalCancelledByStaff,
			VoidTrigger: requestid.From(ctx),
		})
		if err != nil {
			return err
		}
		if _, err := q.ReverseOrderPoints(ctx, e.orderID); err != nil {
			return fmt.Errorf("claw back loyalty earned on %s: %w", e.number, err)
		}
	case order.FulfillmentDelivered, order.FulfillmentCompleted:
		// BOTH transitions that end a delivery: shipped -> completed directly is
		// the only honest move for convenience-store pickup, and stamping only on
		// 'delivered' would leave that channel's parcels unstamped, so
		// /admin/returns would read the Consumer Protection Act §19 window as
		// never having started. Idempotent by the query's own WHERE clause.
		if err := q.MarkShipmentsDelivered(ctx, e.orderID); err != nil {
			return fmt.Errorf("mark parcels of %s delivered: %w", e.number, err)
		}
	}
	return enqueueStatusNotice(ctx, q, e)
}

func enqueueStatusNotice(ctx context.Context, q *db.Queries, e statusEffect) error {
	switch e.status {
	case order.FulfillmentDelivered, order.FulfillmentCompleted:
		row, err := q.OrderDestinationKind(ctx, e.number)
		if err != nil {
			return fmt.Errorf("read terminal order destination: %w", err)
		}
		to, ok := shipping.ParseDestination(row.DestinationKind)
		if !ok {
			return fmt.Errorf("order %s ships by a method with an unknown destination %q",
				e.number, row.DestinationKind)
		}
		kind := outbox.TerminalDelivered
		if to == shipping.DestinationPickupPoint {
			if e.status != order.FulfillmentCompleted {
				return nil
			}
			kind = outbox.TerminalCollected
		} else if e.previous == order.FulfillmentDelivered {
			// Completion adds no new arrival, even after outbox retention.
			return nil
		}
		return notice.Enqueue(ctx, q, &outbox.OrderTerminal{OrderID: e.orderID, Kind: kind})
	default:
		return nil
	}
}

// eventKindFor maps a fulfilment status to its order_events kind. The two
// vocabularies overlap without being the same list. A status with no kind is
// refused rather than panicked on, because the status comes from the request.
func eventKindFor(status order.FulfillmentStatus) (string, error) {
	switch status {
	case order.FulfillmentPicking:
		return string(order.EventPicking), nil
	case order.FulfillmentShipped:
		return string(order.EventShipped), nil
	case order.FulfillmentDelivered:
		return string(order.EventDelivered), nil
	case order.FulfillmentCompleted:
		return string(order.EventCompleted), nil
	case order.FulfillmentCancelled:
		return string(order.EventCancelled), nil
	default:
		return "", fmt.Errorf("%w: no order_events kind for fulfilment status %s", ErrRefused, status)
	}
}

func (s *Store) fillRefundBeforeShipment(ctx context.Context, view *OrderView, number string) error {
	offer, err := s.refunds.Offer(ctx, number, len(view.Shippable))
	if err != nil {
		return err
	}
	view.RefundOffered, view.RefundOpen = offer.Offered, offer.Open
	view.RefundCreditPaid = offer.CreditPaid
	view.UnsentOffered = offer.UnsentOffered
	view.UnsentOpen, view.UnsentOpenLines = offer.UnsentOpen, unsentLines(offer.UnsentUnits)
	opened := offer.Opened
	if opened {
		view.CanShip = false
	}
	for _, n := range view.Status.Next() {
		if n == order.FulfillmentShipped {
			// Only [Store.Ship] dispatches: a dispatch must also settle the stock the order holds.
			continue
		}
		if (n == order.FulfillmentCancelled && view.Funded) ||
			(n == order.FulfillmentPicking && (opened || view.Unpaid)) ||
			// orders_finished_when_shipped: an order that still owes a parcel is
			// not finished, and Shippable is what is still outstanding. Units an
			// unsent refund claims are owed until it settles.
			(n == order.FulfillmentCompleted && (len(view.Shippable) > 0 || view.UnsentOpen)) {
			continue
		}
		view.Next = append(view.Next, Transition{Value: n, Label: n.Label(ctx)})
	}
	return nil
}

// unsentLines are the units an open refund of unsent units covers, as the
// order page lists them.
func unsentLines(units []refund.UnsentUnit) []UnsentLine {
	lines := make([]UnsentLine, len(units))
	for i, u := range units {
		lines[i] = UnsentLine{OrderLineID: u.LineID.String(), Name: u.Name, Label: u.Label, Quantity: u.Quantity}
	}
	return lines
}

func (s *Store) fillPayments(ctx context.Context, view *OrderView, orderID uuid.UUID) error {
	paid, err := s.q.OrderCapturedPayment(ctx, orderID)
	switch {
	case err == nil:
		view.Payment = Payment{
			Method:   i18n.T(ctx, i18n.KeyAdminPayMethodCard),
			Card:     payment.CardLabel(paid.CardBrand, paid.CardLast4),
			Captured: money.TWD(paid.CapturedCents),
			PaidAt:   nullableStamp(paid.PaidAt),
		}
	case errors.Is(err, pgx.ErrNoRows):
		view.Payment = paymentWithoutCard(ctx, view.OwedCents, view.CreditCents)
	default:
		return fmt.Errorf("read payment of order %s: %w", view.Number, err)
	}

	refundRows, err := s.q.OrderRefundRows(ctx, orderID)
	if err != nil {
		return fmt.Errorf("read refunds of order %s: %w", view.Number, err)
	}
	for i := range refundRows {
		r := &refundRows[i]
		channel, reason := i18n.KeyAdminPayRefundCard, r.Reason
		if r.Channel == "credit" {
			channel, reason = i18n.KeyAdminPayRefundCredit, credit.Reason(ctx, credit.Kind(r.Kind), r.Reason)
		}
		view.Refunds = append(view.Refunds, Refund{
			Channel: i18n.T(ctx, channel), Amount: money.TWD(r.AmountCents),
			At: nullableStamp(r.At), Reason: reason, Staff: r.Staff,
		})
	}
	return nil
}

// paymentWithoutCard is how an order with no card capture was paid, read from
// what it still owes: store credit paid it, or nothing was due. An order that
// still owes has no payment.
func paymentWithoutCard(ctx context.Context, owedCents, creditCents int64) Payment {
	switch {
	case owedCents > 0:
		return Payment{}
	case creditCents > 0:
		return Payment{Method: i18n.T(ctx, i18n.KeyAdminPayMethodCredit)}
	default:
		return Payment{Method: i18n.T(ctx, i18n.KeyAdminPayMethodFree)}
	}
}

// fillShippable puts what an order still owes a dispatch on its page. CanShip
// follows from what is OUTSTANDING and not from the status, which is what makes
// a second parcel possible.
func (s *Store) fillShippable(
	ctx context.Context, view *OrderView, orderID uuid.UUID, status order.FulfillmentStatus,
) error {
	// DELIVERED must stay in this set. orders_legal_transition permits
	// shipped -> delivered while a line is still outstanding, deliberately:
	// delivered is a fact about what WENT OUT. Without the dispatch form there,
	// the order wedges — orders_finished_when_shipped refuses 'completed' and the
	// remaining line's hold is stranded, since release_reservation refuses a
	// committed order and ExpiredReservations excludes it.
	if status != order.FulfillmentPicking && status != order.FulfillmentShipped &&
		status != order.FulfillmentDelivered {
		return nil
	}
	rows, err := s.q.ShippableLines(ctx, orderID)
	if err != nil {
		return fmt.Errorf("read shippable lines: %w", err)
	}
	for i := range rows {
		l := &rows[i]
		view.Shippable = append(view.Shippable, ShippableLine{
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
	// The carrier is one of the closed set; its display name is not stored.
	carrierCode, tracking := shipping.Carrier(strings.TrimSpace(d.Carrier)), strings.TrimSpace(d.Tracking)
	if !carrierCode.Known() || tracking == "" {
		return ErrInvalid
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin ship: %w", err)
	}
	defer db.Rollback(ctx, tx)
	q := s.q.WithTx(tx)

	row, err := q.OrderIDByNumber(ctx, number)
	if err != nil {
		return refusedIfNoRow(err, "read order "+number)
	}
	// A parcel is only recorded for an order that has entered fulfilment. This is
	// the same set fillShippable renders the form for; the trigger
	// shipment_order_in_fulfilment is the authority, and this is the sentence.
	fulfillment := order.FulfillmentStatus(row.FulfillmentStatus)
	switch fulfillment {
	case order.FulfillmentPicking, order.FulfillmentShipped, order.FulfillmentDelivered:
	default:
		return fmt.Errorf("%w: order %s is %s and has not been picked",
			ErrRefused, number, row.FulfillmentStatus)
	}
	if carrierErr := requireCarrierFor(ctx, q, row.ID, number, carrierCode); carrierErr != nil {
		return carrierErr
	}
	shipmentID, shipErr := recordShipment(ctx, q, row.ID, carrierCode, tracking)
	if shipErr != nil {
		return shipErr
	}

	if fillErr := shipParcel(ctx, q, row.ID, shipmentID, number, d.Lines); fillErr != nil {
		return fillErr
	}

	// Only picking advances to shipped. A second parcel leaves the order where it
	// already is; the status check above and shipment_order_in_fulfilment refuse
	// an unpicked order, because this branch never reaches the transition trigger.
	if fulfillment == order.FulfillmentPicking {
		if advErr := q.AdvanceOrder(ctx, db.AdvanceOrderParams{
			OrderNumber: number, Status: string(order.FulfillmentShipped),
		}); advErr != nil {
			return db.WrapRefusal(advErr, ErrRefused)
		}
	}

	// One notice per PARCEL: the dedupe key is carrier and tracking together,
	// which is what order_shipments is unique on, not the order. An order in two
	// parcels is still two notices.
	if err := enqueueOrderShipped(ctx, q, row.ID, &outbox.OrderShipped{
		OrderNumber: number, Carrier: string(carrierCode), Tracking: tracking,
	}); err != nil {
		return err
	}

	if err := q.RecordOrderEvent(ctx, db.RecordOrderEventParams{
		OrderID: row.ID, Kind: string(order.EventShipped), ActorUserID: actor,
		ShipmentID: uuid.NullUUID{UUID: shipmentID, Valid: true},
	}); err != nil {
		return fmt.Errorf("record order event: %w", err)
	}
	if err := audit.In(ctx, q, audit.Event{
		Action: audit.ActionShipOrder, Table: "orders", ID: audit.EntityID(row.ID),
		Before: nil, After: audit.State{audit.FieldCarrier: string(carrierCode), audit.FieldTracking: tracking},
	}); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit ship: %w", err)
	}
	return nil
}

func requireCarrierFor(
	ctx context.Context, q *db.Queries, orderID uuid.UUID, number string, code shipping.Carrier,
) error {
	dest, err := q.OrderDispatchDestination(ctx, orderID)
	if err != nil {
		return fmt.Errorf("read destination of %s: %w", number, err)
	}
	valid, _ := shipping.CarriersFor(shipping.Chain(dest.PickupChain), shipping.Destination(dest.DestinationKind))
	if !slices.Contains(valid, code) {
		return fmt.Errorf("%w: %s cannot carry order %s", ErrCarrier, code, number)
	}
	return nil
}

// shipParcel decides what is in this parcel and writes it.
func shipParcel(
	ctx context.Context, q *db.Queries, orderID, shipmentID uuid.UUID,
	number string, want map[uuid.UUID]int32,
) error {
	packed, err := packParcel(ctx, q, orderID, number, want)
	if err != nil {
		return err
	}
	return fillParcel(ctx, q, orderID, shipmentID, packed)
}

// fillParcel writes what is in this parcel and settles the holds behind it,
// which are one act: a line without its hold settled leaves stock spoken for
// against goods that have gone out, and a hold without its line leaves the
// parcel unable to say what it carried. shipment_within_purchase refusing a
// line is a quantity the form should not have offered, such as a unit an
// unsent refund took: a notice, never a failure of the page.
func fillParcel(
	ctx context.Context, q *db.Queries, orderID, shipmentID uuid.UUID, packed []parcelLine,
) error {
	for _, p := range packed {
		if lineErr := q.CreateShipmentLine(ctx, db.CreateShipmentLineParams{
			OrderID: orderID, ShipmentID: shipmentID,
			OrderLineID: p.lineID, Quantity: p.quantity,
		}); lineErr != nil {
			if db.IsConstraint(lineErr, "shipment_within_purchase") {
				return fmt.Errorf("%w: %w", ErrQuantity, lineErr)
			}
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
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin order note: %w", err)
	}
	defer db.Rollback(ctx, tx)
	q := s.q.WithTx(tx)
	prior, err := q.LockOrderForStaffNote(ctx, number)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock order note: %w", err)
	}
	if prior.StaffNote.String == note {
		return nil
	}
	action := audit.ActionReplaceOrderNote
	if note == "" {
		action = audit.ActionClearOrderNote
	} else if prior.StaffNote.String == "" {
		action = audit.ActionCreateOrderNote
	}
	if err := q.SetStaffNote(ctx, db.SetStaffNoteParams{
		OrderNumber: number, StaffNote: text(note),
	}); err != nil {
		return fmt.Errorf("set staff note: %w", err)
	}
	// The append-only trail outlives erasure, so it records the operation and
	// order number without retaining another copy of the note.
	if err := audit.In(ctx, q, audit.Event{Action: action, Table: "orders", ID: audit.EntityID(prior.ID), After: audit.State{audit.FieldNumber: number}}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit order note: %w", err)
	}
	return nil
}

func text(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

func nullableStamp(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return shoptime.Minute(t.Time)
}
