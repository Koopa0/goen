package orders

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/carrier"
	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/destination"
	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ordercancel"
	"github.com/koopa0/goen/internal/ordernotice"
	"github.com/koopa0/goen/internal/outbox"
	"github.com/koopa0/goen/internal/payment"
	"github.com/koopa0/goen/internal/pgerr"
	"github.com/koopa0/goen/internal/pgtx"
	"github.com/koopa0/goen/internal/pickup"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

// Refunds offers the refund before shipment on the order page and reports
// whether one was ever opened; the refunds desk implements it.
type Refunds interface {
	FillOrder(ctx context.Context, view *admin.OrderView, number string) (opened bool, err error)
}

// Invoices puts what has been filed on the order page; the invoicing desk
// implements it.
type Invoices interface {
	FillOrder(ctx context.Context, view *admin.OrderView, number string) error
}

// Stock ranks the SKUs by days cover, for the dashboard; the stock desk
// implements it.
type Stock interface {
	DaysCover(ctx context.Context, days int, now time.Time) (listed []admin.StockRisk, moreSoldOut int, err error)
}

// Health lists what the health desk judges to need a person, as dashboard
// tasks; the health desk implements it.
type Health interface {
	Tasks(ctx context.Context) ([]admin.Task, error)
}

// Sales reads the dashboard's last seven days and its latest paid order; the
// reports desk implements it.
type Sales interface {
	FillWeek(ctx context.Context, view *admin.DashboardView, now time.Time) error
}

type Store struct {
	pool     *pgxpool.Pool
	q        *db.Queries
	refunds  Refunds
	invoices Invoices
	stock    Stock
	health   Health
	sales    Sales
}

func NewStore(pool *pgxpool.Pool, refunds Refunds, invoices Invoices, stock Stock, health Health, sales Sales) *Store {
	if pool == nil || refunds == nil || invoices == nil || stock == nil || health == nil || sales == nil {
		panic("orders: NewStore requires a pool, refunds, invoices, stock, health and sales")
	}
	return &Store{pool: pool, q: db.New(pool), refunds: refunds, invoices: invoices, stock: stock, health: health, sales: sales}
}

func (s *Store) Dashboard(ctx context.Context) (admin.DashboardView, error) {
	sum, err := s.q.AdminSummary(ctx)
	if err != nil {
		return admin.DashboardView{}, fmt.Errorf("read summary: %w", err)
	}
	view := admin.DashboardView{
		PendingOrders:             sum.PendingOrders,
		ReadyOrders:               sum.ReadyOrders,
		ReadyOldestSeconds:        sum.ReadyOldestSeconds,
		PickingOrders:             sum.PickingOrders,
		PickingOldestSeconds:      sum.PickingOldestSeconds,
		SoldOut:                   sum.SoldOut,
		OpenMessages:              sum.OpenMessages,
		OpenMessagesOldestSeconds: sum.OpenMessagesOldestSeconds,

		PendingReturns:                   sum.PendingReturns,
		PendingReturnsOldestSeconds:      sum.PendingReturnsOldestSeconds,
		UninspectedReturns:               sum.UninspectedReturns,
		UninspectedReturnsOldestSeconds:  sum.UninspectedReturnsOldestSeconds,
		UnansweredQuestions:              sum.UnansweredQuestions,
		UnansweredQuestionsOldestSeconds: sum.UnansweredQuestionsOldestSeconds,
	}
	// No status: the newest orders whatever state they are in. The tiles above
	// the queue already count each state, and a queue filtered to one of them
	// hides the order somebody is standing at the counter asking about.
	recent, err := s.q.AdminOrders(ctx, db.AdminOrdersParams{RowLimit: DashboardRows})
	if err != nil {
		return admin.DashboardView{}, fmt.Errorf("read recent orders: %w", err)
	}
	if view.Recent, err = s.orderRows(ctx, recent); err != nil {
		return admin.DashboardView{}, err
	}

	listed, _, err := s.stock.DaysCover(ctx, admin.CoverWindowDays, time.Now())
	if err != nil {
		return admin.DashboardView{}, fmt.Errorf("read days cover: %w", err)
	}
	view.Runway, view.RunwayCut, view.RunwayBasis = admin.DashboardRunway(listed)
	view.Tasks = view.DeskTasks()
	return view, nil
}

// HealthTasks is what the health desk says needs a person. The dashboard is
// still worth opening without it, so the caller decides what an error costs.
func (s *Store) HealthTasks(ctx context.Context) ([]admin.Task, error) {
	return s.health.Tasks(ctx)
}

// FillWeek is the last seven days of the shop's sales. The dashboard is still
// worth opening without it, so the caller decides what an error costs.
func (s *Store) FillWeek(ctx context.Context, view *admin.DashboardView, now time.Time) error {
	return s.sales.FillWeek(ctx, view, now)
}

const DashboardRows = 8

// orderRows reads which of the orders are returned in one query, so a page of the queue costs no more than one.
func (s *Store) orderRows(ctx context.Context, rows []db.AdminOrdersRow) ([]admin.OrderRow, error) {
	ids := make([]uuid.UUID, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
	}
	returned, err := s.q.ReturnedOrders(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("read returned orders: %w", err)
	}
	out := make([]admin.OrderRow, len(rows))
	for i := range rows {
		out[i] = orderRow(ctx, &rows[i], slices.Contains(returned, rows[i].ID))
	}
	return out, nil
}

// orderRow is one order as both the queue and the landing page render it. The
// total is assembled here rather than in the query because the storefront's
// own order view computes it the same way from the same four columns.
func orderRow(ctx context.Context, o *db.AdminOrdersRow, returned bool) admin.OrderRow {
	fulfillment := order.FulfillmentStatus(o.FulfillmentStatus)
	text, intent := orderStatus(ctx, fulfillment, o.Committed, o.OwedCents, returned)
	return admin.OrderRow{
		Number:       o.OrderNumber,
		Status:       fulfillment,
		StatusText:   text,
		StatusIntent: intent,
		PlacedAt:     shoptime.Minute(o.PlacedAt),
		Recipient:    o.Recipient,
		TotalCents:   o.SubtotalCents - o.DiscountCents + o.ShippingCents + o.TaxCents,
	}
}

// orderStatus is the word and colour an order carries: an order returned in full says so in place of where its
// delivery stands, since the refund has already been paid.
func orderStatus(ctx context.Context, status order.FulfillmentStatus, committed bool, owedCents int64, returned bool) (string, components.Intent) {
	if returned {
		return i18n.T(ctx, i18n.KeyStatusRefunded), components.IntentNeutral
	}
	return admin.FundedFulfillmentLabel(ctx, status, committed, owedCents), admin.FundedFulfillmentIntent(status, committed, owedCents)
}

// listPosition is a reader's place in the orders queue. The query builds it as
// PageCursor, so its fields are the ordering values and nothing else.
type listPosition struct {
	At time.Time
	ID uuid.UUID
}

func (s *Store) List(ctx context.Context, status admin.QueueFilter, term string, after ...string) (admin.OrdersView, error) {
	term = strings.TrimSpace(term)
	scope := web.ScopeURL("/admin/orders", "q", term, "status", string(status))
	from, resumed := web.ResumeKeyset(scope, after, func(p listPosition) bool { return p.ID != uuid.Nil })
	searched := utf8.RuneCountInString(term) >= web.MinSearchRunes
	var rows []db.AdminOrdersRow
	var err error
	if searched {
		// A search ignores the status filter: somebody on the phone wants that
		// order, not that order if it is in the tab they had open.
		var found []db.AdminSearchOrdersRow
		if found, err = s.q.AdminSearchOrders(ctx, db.AdminSearchOrdersParams{HasCursor: resumed, AfterAt: from.At, AfterID: from.ID,
			Term: term, EscapedTerm: catalog.EscapeLike(term), RowLimit: web.PageLimit,
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
		case admin.QueueAwaitingPayment:
			filter, funding = string(order.FulfillmentPending), "unpaid"
		case admin.QueueReady:
			filter, funding = string(order.FulfillmentPending), "funded"
		default: // a fulfilment status filters by itself
		}
		rows, err = s.q.AdminOrders(ctx, db.AdminOrdersParams{HasCursor: resumed, AfterAt: from.At, AfterID: from.ID, Status: filter, Funding: funding, RowLimit: web.PageLimit})
	}
	if err != nil {
		return admin.OrdersView{}, fmt.Errorf("read orders: %w", err)
	}
	counts, err := s.q.AdminOrderCounts(ctx)
	if err != nil {
		return admin.OrdersView{}, fmt.Errorf("read order counts: %w", err)
	}

	// Both branches asked for one row more than the page shows, so the drop is
	// here rather than in each of them. The tab counts above come from
	// AdminOrderCounts and not from len(rows), so the extra row was never in
	// them to begin with.
	rows, bound := web.PageBound(scope, resumed, rows, web.PageSize, func(r *db.AdminOrdersRow) string { return r.PageCursor })
	view := admin.OrdersView{
		Bound:  bound,
		Status: status, Term: term, Searched: searched,
	}
	countsByFilter := make(map[admin.QueueFilter]int64, len(counts))
	var total int64
	for _, c := range counts {
		key := admin.QueueFilter(c.FulfillmentStatus)
		switch {
		case c.FulfillmentStatus == string(order.FulfillmentPending) && c.Funded:
			key = admin.QueueReady
		case c.FulfillmentStatus == string(order.FulfillmentPending):
			key = admin.QueueAwaitingPayment
		}
		countsByFilter[key] += c.N
		total += c.N
	}
	view.Tabs = make([]admin.StatusTab, 0, len(queueTabs)+1)
	view.Tabs = append(view.Tabs, admin.StatusTab{
		Label: i18n.T(ctx, i18n.KeyAdminTabAll), Count: total, Selected: status == admin.QueueAll,
	})
	for _, tab := range queueTabs {
		view.Tabs = append(view.Tabs, admin.StatusTab{
			Value: tab.filter, Label: i18n.T(ctx, tab.label),
			Count: countsByFilter[tab.filter], Selected: tab.filter == status,
		})
	}
	if view.Orders, err = s.orderRows(ctx, rows); err != nil {
		return admin.OrdersView{}, err
	}
	return view, nil
}

func (s *Store) Order(ctx context.Context, number string) (admin.OrderView, error) {
	o, err := s.q.AdminOrderByNumber(ctx, number)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return admin.OrderView{}, ErrNotFound
		}
		return admin.OrderView{}, fmt.Errorf("read order: %w", err)
	}
	lines, err := s.q.OrderLinesByOrder(ctx, o.ID)
	if err != nil {
		return admin.OrderView{}, fmt.Errorf("read order lines: %w", err)
	}

	returned, err := s.q.ReturnedOrders(ctx, []uuid.UUID{o.ID})
	if err != nil {
		return admin.OrderView{}, fmt.Errorf("read returned order: %w", err)
	}
	fulfillment := order.FulfillmentStatus(o.FulfillmentStatus)
	statusText, statusIntent := orderStatus(ctx, fulfillment, o.Committed, o.OwedCents, len(returned) > 0)
	view := admin.OrderView{
		Number: o.OrderNumber, Status: fulfillment,
		StatusText:    statusText,
		StatusIntent:  statusIntent,
		PlacedAt:      shoptime.Minute(o.PlacedAt),
		ShippingName:  o.ShippingMethodName,
		SubtotalCents: o.SubtotalCents, ShippingCents: o.ShippingCents,
		DiscountCents: o.DiscountCents, DiscountReason: o.DiscountReason, TaxCents: o.TaxCents,
		Email: o.Email, Recipient: o.RecipientName, Phone: o.Phone,
		Address: pages.Delivery{
			PostalCode: o.PostalCode, City: o.City, District: o.District, Street: o.Street,
			PickupChain: pickup.Chain(o.PickupChain), PickupStoreCode: o.PickupStoreCode,
			PickupStoreName: o.PickupStoreName,
		}.Line(),
		Delivery: admin.Delivery{
			Email: o.Email, Recipient: o.RecipientName, Phone: o.Phone,
			PostalCode: o.PostalCode, City: o.City,
			District: o.District, Street: o.Street,
			PickupChain: pickup.Chain(o.PickupChain), PickupStoreCode: o.PickupStoreCode,
			PickupStoreName: o.PickupStoreName,
		},
		// UpdateOrderDelivery's WHERE clause is the authority; this only decides
		// whether to offer the form.
		Correctable:          correctable(fulfillment),
		PickupDestination:    o.PickupChain != "",
		PickupChains:         pages.PickupChainChoices(),
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

	carriers, implied := carrier.ForDelivery(pickup.Chain(o.PickupChain), destination.Kind(o.DestinationKind) == destination.PickupPoint)
	view.ShipCarriers, view.ShipCarrier = carriers, string(implied)

	if shipErr := s.fillShippable(ctx, &view, o.ID, fulfillment); shipErr != nil {
		return admin.OrderView{}, shipErr
	}

	if invErr := s.invoices.FillOrder(ctx, &view, number); invErr != nil {
		return admin.OrderView{}, invErr
	}
	if refundErr := s.fillRefundBeforeShipment(ctx, &view, number); refundErr != nil {
		return admin.OrderView{}, refundErr
	}
	if payErr := s.fillPayments(ctx, &view, o.ID); payErr != nil {
		return admin.OrderView{}, payErr
	}
	for _, l := range lines {
		view.Lines = append(view.Lines, pages.OrderLine{
			SKU: l.SKU, Name: l.ProductName, Label: l.VariantLabel.String,
			UnitCents: l.UnitPriceCents, Quantity: l.Quantity,
		})
	}

	timeline, err := s.q.AdminOrderTimeline(ctx, db.AdminOrderTimelineParams{
		OrderID: o.ID, MailTopics: orderMailTopics, InvoicingEnabled: view.InvoicingEnabled,
	})
	if err != nil {
		return admin.OrderView{}, fmt.Errorf("read order timeline: %w", err)
	}
	for i := range timeline {
		view.Timeline = append(view.Timeline, timelineEntry(&timeline[i]))
	}
	view.MailKept = outbox.Retain

	shipments, err := s.q.OrderShipments(ctx, o.ID)
	if err != nil {
		return admin.OrderView{}, fmt.Errorf("read order shipments: %w", err)
	}
	for i := range shipments {
		sh := &shipments[i]
		view.Shipments = append(view.Shipments, admin.Shipment{
			Carrier: carrier.Carrier(sh.Carrier), Tracking: sh.TrackingNumber,
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
	defer pgtx.Rollback(ctx, tx)
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
		return nil, pgerr.WrapRefusal(advanceErr, ErrRefused)
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
		Before: nil, After: map[string]any{"number": number, "status": string(status)},
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
func recordShipment(ctx context.Context, q *db.Queries, orderID uuid.UUID, c carrier.Carrier, tracking string) (uuid.UUID, error) {
	id, err := q.CreateShipment(ctx, db.CreateShipmentParams{
		OrderID: orderID, Carrier: string(c), TrackingNumber: tracking,
	})
	if pgerr.IsConstraint(err, "shipment_order_in_fulfilment") {
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
		_, err := ordercancel.Settle(ctx, q, &ordercancel.Order{
			ID: e.orderID, Number: e.number, Actor: e.actor, Kind: email.TerminalCancelledByStaff,
			VoidTrigger: web.RequestID(ctx),
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
		to, ok := destination.For(row.DestinationKind)
		if !ok {
			return fmt.Errorf("order %s ships by a method with an unknown destination %q",
				e.number, row.DestinationKind)
		}
		kind := email.TerminalDelivered
		if to == destination.PickupPoint {
			if e.status != order.FulfillmentCompleted {
				return nil
			}
			kind = email.TerminalCollected
		} else if e.previous == order.FulfillmentDelivered {
			// Completion adds no new arrival, even after outbox retention.
			return nil
		}
		return ordernotice.Enqueue(ctx, q, &email.OrderTerminal{OrderID: e.orderID, Kind: kind})
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

func (s *Store) fillRefundBeforeShipment(ctx context.Context, view *admin.OrderView, number string) error {
	opened, err := s.refunds.FillOrder(ctx, view, number)
	if err != nil {
		return err
	}
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
			// not finished, and Shippable is what is still outstanding.
			(n == order.FulfillmentCompleted && len(view.Shippable) > 0) {
			continue
		}
		view.Next = append(view.Next, admin.Transition{Value: n, Label: admin.FulfillmentLabel(ctx, n)})
	}
	return nil
}

func (s *Store) fillPayments(ctx context.Context, view *admin.OrderView, orderID uuid.UUID) error {
	paid, err := s.q.OrderCapturedPayment(ctx, orderID)
	switch {
	case err == nil:
		view.Payment = admin.Payment{
			Method:   i18n.T(ctx, i18n.KeyAdminPayMethodCard),
			Card:     payment.CardLabel(paid.CardBrand, paid.CardLast4),
			Captured: pages.TWD(paid.CapturedCents),
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
			channel, reason = i18n.KeyAdminPayRefundCredit, admin.CreditReason(ctx, r.Reason)
		}
		view.Refunds = append(view.Refunds, admin.Refund{
			Channel: i18n.T(ctx, channel), Amount: pages.TWD(r.AmountCents),
			At: nullableStamp(r.At), Reason: reason, Staff: r.Staff,
		})
	}
	return nil
}

// paymentWithoutCard is how an order with no card capture was paid, read from
// what it still owes: store credit paid it, or nothing was due. An order that
// still owes has no payment.
func paymentWithoutCard(ctx context.Context, owedCents, creditCents int64) admin.Payment {
	switch {
	case owedCents > 0:
		return admin.Payment{}
	case creditCents > 0:
		return admin.Payment{Method: i18n.T(ctx, i18n.KeyAdminPayMethodCredit)}
	default:
		return admin.Payment{Method: i18n.T(ctx, i18n.KeyAdminPayMethodFree)}
	}
}

// fillShippable puts what an order still owes a dispatch on its page. CanShip
// follows from what is OUTSTANDING and not from the status, which is what makes
// a second parcel possible.
func (s *Store) fillShippable(
	ctx context.Context, view *admin.OrderView, orderID uuid.UUID, status order.FulfillmentStatus,
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
		view.Shippable = append(view.Shippable, admin.ShippableLine{
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
	carrierCode, tracking := carrier.Carrier(strings.TrimSpace(d.Carrier)), strings.TrimSpace(d.Tracking)
	if !carrierCode.Known() || tracking == "" {
		return ErrInvalid
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin ship: %w", err)
	}
	defer pgtx.Rollback(ctx, tx)
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

	if fillErr := fillParcel(ctx, q, row.ID, shipmentID, number, d.Lines); fillErr != nil {
		return fillErr
	}

	// Only picking advances to shipped. A second parcel leaves the order where it
	// already is; the status check above and shipment_order_in_fulfilment refuse
	// an unpicked order, because this branch never reaches the transition trigger.
	if fulfillment == order.FulfillmentPicking {
		if advErr := q.AdvanceOrder(ctx, db.AdvanceOrderParams{
			OrderNumber: number, Status: string(order.FulfillmentShipped),
		}); advErr != nil {
			return pgerr.WrapRefusal(advErr, ErrRefused)
		}
	}

	// One notice per PARCEL: the dedupe key is carrier and tracking together,
	// which is what order_shipments is unique on, not the order. An order in two
	// parcels is still two notices.
	if err := enqueueOrderShipped(ctx, q, row.ID, &email.OrderShipped{
		OrderNumber: number, Carrier: string(carrierCode), Tracking: tracking,
	}); err != nil {
		return err
	}

	if err := q.RecordOrderEvent(ctx, db.RecordOrderEventParams{
		OrderID: row.ID, Kind: string(order.EventShipped), ActorUserID: actor,
		Note: text(i18n.CarrierName(i18n.WithLocale(ctx, i18n.ZhHant), carrierCode) + " " + tracking),
	}); err != nil {
		return fmt.Errorf("record order event: %w", err)
	}
	if err := audit.In(ctx, q, audit.Event{
		Action: audit.ActionShipOrder, Table: "orders", ID: audit.EntityID(row.ID),
		Before: nil, After: map[string]any{"carrier": string(carrierCode), "tracking": tracking},
	}); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit ship: %w", err)
	}
	return nil
}

func requireCarrierFor(
	ctx context.Context, q *db.Queries, orderID uuid.UUID, number string, code carrier.Carrier,
) error {
	dest, err := q.OrderDispatchDestination(ctx, orderID)
	if err != nil {
		return fmt.Errorf("read destination of %s: %w", number, err)
	}
	valid, _ := carrier.ForDelivery(pickup.Chain(dest.PickupChain), destination.Kind(dest.DestinationKind) == destination.PickupPoint)
	if !slices.Contains(valid, code) {
		return fmt.Errorf("%w: %s cannot carry order %s", ErrCarrier, code, number)
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
	defer pgtx.Rollback(ctx, tx)
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
	if err := audit.In(ctx, q, audit.Event{Action: action, Table: "orders", ID: audit.EntityID(prior.ID), After: map[string]any{"number": number}}); err != nil {
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
