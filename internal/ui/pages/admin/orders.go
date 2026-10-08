package admin

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/koopa0/goen/internal/carrier"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/inventory"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/pickup"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

type Variant struct {
	SKU                        string
	Slug                       string
	ProductName                string
	Brand                      string
	PriceCents                 int64
	CompareCents               int64
	Stock                      int32
	Safety                     int32
	Active                     bool
	ProductStatus              string
	Options                    []string
	FormID                     string
	DraftDelta, DeltaError     string
	ArrivalInput, ArrivalError string
}

func (v *Variant) OptionText() string { return strings.Join(v.Options, " · ") }

func (v *Variant) StockText() string { return strconv.FormatInt(int64(v.Stock), 10) }

func (v *Variant) SafetyText() string { return strconv.FormatInt(int64(v.Safety), 10) }

func (v *Variant) SellableText() string {
	n := max(v.Stock-v.Safety, 0)
	return strconv.FormatInt(int64(n), 10)
}

func (v *Variant) Low() bool { return v.Stock <= v.Safety }

type OrderRow struct {
	Number       string
	Status       order.FulfillmentStatus
	StatusText   string
	StatusIntent components.Intent
	PlacedAt     string
	Recipient    string
	TotalCents   int64
}

func (o OrderRow) Total() string { return money.TWD(o.TotalCents) }

type Transition struct {
	Value order.FulfillmentStatus
	Label string
}

// QueueFilter is one view of the orders queue. It is not a fulfilment status:
// pending orders are split by whether money is still owed, so the filters are
// their own closed set. The values are the ?status= query values.
type QueueFilter string

const (
	QueueAll             QueueFilter = ""
	QueueAwaitingPayment QueueFilter = "pending"
	QueueReady           QueueFilter = "ready"
	QueuePicking         QueueFilter = "picking"
	QueueShipped         QueueFilter = "shipped"
	QueueDelivered       QueueFilter = "delivered"
	QueueCompleted       QueueFilter = "completed"
	QueueCancelled       QueueFilter = "cancelled"
)

type StatusTab struct {
	Value    QueueFilter
	Label    string
	Count    int64
	Selected bool
}

func (t StatusTab) CountText() string { return strconv.FormatInt(t.Count, 10) }

// DashboardView lists the newest orders in Recent whatever their state: filtering it
// to one state would answer what the tiles above already answer, and hide the order
// somebody walked over to ask about.
type DashboardView struct {
	PendingOrders int64
	ReadyOrders   int64
	// ReadyOldestSeconds and the other *OldestSeconds are how long the oldest
	// waiting item has waited, on the database's clock.
	ReadyOldestSeconds        int64
	PickingOrders             int64
	PickingOldestSeconds      int64
	SoldOut                   int64
	OpenMessages              int64
	OpenMessagesOldestSeconds int64
	// PendingReturns is the requests nobody has decided, UninspectedReturns the
	// approved ones whose parcel nobody has opened.
	PendingReturns                   int64
	PendingReturnsOldestSeconds      int64
	UninspectedReturns               int64
	UninspectedReturnsOldestSeconds  int64
	UnansweredQuestions              int64
	UnansweredQuestionsOldestSeconds int64
	Recent                           []OrderRow
	// Runway is the first days cover rows of SKUs expected to run out within
	// the warning line, RunwayCut says more were left off and RunwayBasis what
	// an empty list can say.
	Runway      []StockRisk
	RunwayCut   bool
	RunwayBasis RunwayBasis
	// Tasks is the work that waits for a person, in the order it is listed.
	Tasks []Task
	// HealthUnavailable is set when the health desk could not be read, so an
	// absent payment or invoice task is not taken for "nothing to check".
	HealthUnavailable bool
	// Week is the last seven days; WeekUnavailable says it could not be read,
	// which is not the same as a week without orders. Likewise Latest, nil when
	// no order was ever paid, and LatestUnavailable.
	Week              Week
	WeekUnavailable   bool
	Latest            *LatestPaid
	LatestUnavailable bool
}

// Task is one kind of work that waits for a person: what it is, how much of
// it, and where it is done.
type Task struct {
	Label i18n.Key
	Count int64
	Href  string
	// HasAge is false for work with no start time to measure from.
	HasAge     bool
	AgeSeconds int64
	// Alert marks work that is wrong rather than merely waiting.
	Alert bool
}

const secondsPerDay = 24 * 60 * 60

// AgeText says how long the oldest item has waited, in whole days.
func (t Task) AgeText(ctx context.Context) string {
	days := t.AgeSeconds / secondsPerDay
	if days < 1 {
		return i18n.T(ctx, i18n.KeyAdminQueueTaskUnderADay)
	}
	return i18n.Count(ctx, i18n.KeyAdminQueueTaskOldestDays, days, days)
}

// DeskTasks lists what the order desk itself counts, leaving out each kind with
// nothing waiting.
func (v *DashboardView) DeskTasks() []Task {
	all := []Task{
		{Label: i18n.KeyAdminStatusReadyToPick, Count: v.ReadyOrders, Href: "/admin/orders?status=ready", HasAge: true, AgeSeconds: v.ReadyOldestSeconds},
		{Label: i18n.KeyAdminStatusPicking, Count: v.PickingOrders, Href: "/admin/orders?status=picking", HasAge: true, AgeSeconds: v.PickingOldestSeconds},
		{Label: i18n.KeyAdminQueueStatPending, Count: v.PendingOrders, Href: "/admin/orders?status=pending"},
		{Label: i18n.KeyAdminQueueStatReturns, Count: v.PendingReturns, Href: "/admin/returns", HasAge: true, AgeSeconds: v.PendingReturnsOldestSeconds},
		{Label: i18n.KeyAdminQueueTaskUninspected, Count: v.UninspectedReturns, Href: "/admin/returns", HasAge: true, AgeSeconds: v.UninspectedReturnsOldestSeconds},
		{Label: i18n.KeyAdminQueueStatQuestions, Count: v.UnansweredQuestions, Href: "/admin/questions", HasAge: true, AgeSeconds: v.UnansweredQuestionsOldestSeconds},
		{Label: i18n.KeyAdminQueueStatMessages, Count: v.OpenMessages, Href: "/admin/messages", HasAge: true, AgeSeconds: v.OpenMessagesOldestSeconds},
		{Label: i18n.KeyAdminQueueStatSoldOut, Count: v.SoldOut, Href: "/admin/reports#stock"},
	}
	return slices.DeleteFunc(all, func(t Task) bool { return t.Count == 0 })
}

type OrdersView struct {
	web.Bound

	Term     string
	Searched bool
	Status   QueueFilter
	Orders   []OrderRow
	Tabs     []StatusTab
	Notice   components.Result
}

func (v OrdersView) Searching() bool { return v.Searched }

func (v OrdersView) TermTooShort() bool { return v.Term != "" && !v.Searched }

func (v OrdersView) Empty() bool { return len(v.Orders) == 0 }

func (v OrdersView) EmptyText(ctx context.Context) string {
	if v.Searching() {
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminQueueNoneFound), v.Term)
	}
	if v.Status == "" {
		return i18n.T(ctx, i18n.KeyAdminQueueNoneYet)
	}
	return i18n.T(ctx, i18n.KeyAdminQueueEmpty)
}

func (o OrderRow) RecipientText(ctx context.Context) string {
	if o.Recipient == "" {
		return i18n.T(ctx, i18n.KeyAdminErasedRecipient)
	}
	return o.Recipient
}

type OrderView struct {
	Number       string
	Status       order.FulfillmentStatus
	StatusText   string
	StatusIntent components.Intent
	PlacedAt     string
	ShippingName string
	Lines        []pages.OrderLine
	// OutstandingQuantities omits purchased line totals on a remaining-quantity slip.
	OutstandingQuantities bool
	SubtotalCents         int64
	ShippingCents         int64
	DiscountCents         int64
	DiscountReason        string
	TaxCents              int64
	Email                 string
	Recipient             string
	Phone                 string
	Address               string
	CustomerNote          string
	StaffNote             string
	InvoiceType           invoice.Preference
	InvoiceMobileBarcode  string
	InvoiceDonationCode   string
	InvoiceTaxID          string
	InvoiceDocuments      []InvoiceDocument
	InvoicingEnabled      bool
	// RefundedCents is what has actually gone back, and what a 折讓 relieves.
	RefundedCents int64
	// AllowanceOperationID identifies one rendered allowance form across HTTP
	// retries without collapsing a later, legitimate equal partial allowance.
	AllowanceOperationID string
	// AllowanceAwaitingUntil is when the consent link of an allowance sent to
	// ECPay lapses, while it is pending; AllowanceAttention is the reason one
	// that was sent now needs a person.
	AllowanceAwaitingUntil string
	AllowanceAttention     string
	Committed              bool
	// Funded is Committed, or a pending order store credit paid in full, which
	// the database does not count as committed until it is picked. A funded order
	// is cancelled only by refunding it.
	Funded                 bool
	OwedCents, CreditCents int64
	Payment                Payment
	Refunds                []Refund
	// Unpaid is a pending order that still owes money and has no payment: the
	// database refuses to move it into picking.
	Unpaid                    bool
	Next                      []Transition
	CanShip                   bool
	Shippable                 []ShippableLine
	Notice                    components.Result
	Timeline                  []TimelineEntry
	MailKept                  time.Duration
	Shipments                 []Shipment
	DeliveryErrors            map[string]string
	ShipCarrier, ShipTracking string
	// ShipCarriers are the carriers the dispatch form lists, which is the ones
	// that can carry this order's parcel. ShipCarrier holds the one the order
	// implies until staff choose, or what they chose when a dispatch was refused.
	ShipCarriers      []carrier.Carrier
	TrackingError     string
	ShipCarrierError  string
	ShipQtyError      string
	ShipQty           map[string]string
	Delivery          Delivery
	Correctable       bool
	PickupDestination bool
	PickupChains      []pages.PickupChainChoice

	// RefundOffered is a paid order nothing has shipped from and no return
	// exists for.
	RefundOffered bool
	// RefundOpen is one whose refund before shipment Resume finishes.
	RefundOpen bool
	// RefundCreditPaid is an offered one store credit alone paid, which the
	// refund cancels at once.
	RefundCreditPaid bool
}

type Delivery struct {
	Email     string
	Recipient string
	Phone     string

	PostalCode string
	City       string
	District   string
	Street     string

	PickupChain     pickup.Chain
	PickupStoreCode string
	PickupStoreName string
}

// ActorKind is who made a timeline entry happen.
type ActorKind string

const (
	ActorStaff    ActorKind = "staff"
	ActorCustomer ActorKind = "customer"
	ActorSystem   ActorKind = "system"
	ActorProvider ActorKind = "provider"
)

// TimelineEntry is one line of an order's history: one of its events, an
// invoice operation, a notice from the payment provider, or mail about it.
// It names staff, so it never reaches the storefront.
type TimelineEntry struct {
	// At is when the entry happened; for one with a Status, when it was created.
	At     string
	Label  i18n.Key
	Status i18n.Key
	// DoneAt is when a Status entry reached it, empty while unknown.
	DoneAt    string
	Note      string
	ActorKind ActorKind
	// Actor is the staff member's name, empty once their account is erased.
	Actor string
	// Unrecognized holds the source, kind and status of an entry this build has
	// no label for; Label then says so.
	Unrecognized string
}

// When is the entry's time, saying "created" for an operation or mail whose
// Status is where it stands now.
func (e TimelineEntry) When(ctx context.Context) string {
	if e.Status == "" {
		return e.At
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminTimelineCreatedAt), e.At)
}

// StatusText is where a Status entry stands now and, when known, since when.
func (e TimelineEntry) StatusText(ctx context.Context) string {
	if e.DoneAt == "" {
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminTimelineNow), i18n.T(ctx, e.Status))
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminTimelineNowSince), i18n.T(ctx, e.Status), e.DoneAt)
}

func (e TimelineEntry) By(ctx context.Context) string {
	switch e.ActorKind {
	case ActorStaff:
		if e.Actor == "" {
			return i18n.T(ctx, i18n.KeyAdminErasedAccountPlain)
		}
		return e.Actor
	case ActorCustomer:
		return i18n.T(ctx, i18n.KeyAdminActorCustomer)
	case ActorProvider:
		return i18n.T(ctx, i18n.KeyAdminActorProvider)
	default:
		return i18n.T(ctx, i18n.KeyAdminActorSystem)
	}
}

type Shipment struct {
	Carrier     carrier.Carrier
	Tracking    string
	ShippedAt   string
	DeliveredAt string
}

func (s Shipment) Delivered() bool { return s.DeliveredAt != "" }

type ShippableLine struct {
	OrderLineID string
	SKU         string
	Name        string
	Label       string
	Remaining   int32
	Held        int32
}

func (l ShippableLine) Line() string {
	name := l.Name
	if l.Label != "" {
		name += " · " + l.Label
	}
	return name
}

func (l ShippableLine) RemainingText() string {
	return strconv.FormatInt(int64(l.Remaining), 10)
}

// Short reports a hold smaller than the line still owes; that dispatch posts no movement.
func (l ShippableLine) Short() bool { return l.Held < l.Remaining }

func (l ShippableLine) HeldText() string {
	return strconv.FormatInt(int64(l.Held), 10)
}

func (v *OrderView) Subtotal() string { return money.TWD(v.SubtotalCents) }

func (v *OrderView) Shipping() string { return money.TWD(v.ShippingCents) }

func (v *OrderView) Total() string {
	return money.TWD(v.SubtotalCents - v.DiscountCents + v.ShippingCents + v.TaxCents)
}

func (v *OrderView) UsedCredit() bool { return v.CreditCents > 0 }

func (v *OrderView) Credit() string { return "-" + money.TWD(v.CreditCents) }

func (v *OrderView) Owed() string { return money.TWD(v.OwedCents) }

func (v *OrderView) Discounted() bool { return v.DiscountCents > 0 }

func (v *OrderView) Discount() string {
	return "-" + money.TWD(v.DiscountCents)
}

// StartsPicking reports that the only move this order has is into picking: a
// paid order awaiting fulfilment. It is one action, so the page shows it as a
// button and not as a menu with one entry.
func (v *OrderView) StartsPicking() bool {
	return len(v.Next) == 1 && v.Next[0].Value == order.FulfillmentPicking
}

func (v *OrderView) CanAdvance() bool { return len(v.Next) > 0 }

// NextIsDestructive reports that the first move offered is the cancellation, so
// the menu must not preselect it.
func (v *OrderView) NextIsDestructive() bool {
	return len(v.Next) > 0 && v.Next[0].Value == order.FulfillmentCancelled
}

// QtyValue is what a dispatch quantity field holds: what staff typed on a
// refused dispatch, otherwise everything still outstanding.
func (v *OrderView) QtyValue(l *ShippableLine) string {
	if typed, ok := v.ShipQty[l.OrderLineID]; ok {
		return typed
	}
	return l.RemainingText()
}

// Final reports whether the order has ended. A paid order in picking has no
// status move left either, and is not final: it ships or is refunded.
func (v *OrderView) Final() bool {
	return v.Status == order.FulfillmentCompleted || v.Status == order.FulfillmentCancelled
}

func (v *OrderView) RecipientText(ctx context.Context) string {
	if v.Recipient == "" {
		return i18n.T(ctx, i18n.KeyAdminErasedRecipient)
	}
	return v.Recipient
}

func (v *OrderView) HasInvoice() bool { return v.InvoiceType != "" }

func (v *OrderView) InvoiceText(ctx context.Context) string {
	switch v.InvoiceType {
	case invoice.PreferenceMember:
		return i18n.T(ctx, i18n.KeyAdminInvoiceCarrierMember)
	case invoice.PreferenceMobile:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminInvoiceCarrierMobileBarcode), v.InvoiceMobileBarcode)
	case invoice.PreferenceDonate:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminInvoiceDonate), v.InvoiceDonationCode)
	case invoice.PreferenceCompany:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminInvoiceTaxID), v.InvoiceTaxID)
	default:
		panic("pages: no label for invoice type " + string(v.InvoiceType))
	}
}

type InvoiceDocument struct {
	Kind   invoice.DocumentKind
	Number string
	// ProviderRef is the four-digit random code a void needs alongside the number.
	ProviderRef string
	AmountCents int64
	Status      invoice.DocumentStatus
	IssuedAt    string
	Lines       []InvoiceLine
}

type InvoiceLine struct {
	Description string
	Quantity    int32
	AmountCents int64
}

func (l InvoiceLine) Line() string {
	return l.Description + " × " + strconv.FormatInt(int64(l.Quantity), 10) + " · " + money.TWD(l.AmountCents)
}

func (d InvoiceDocument) KindText(ctx context.Context) string {
	if d.Kind == invoice.DocumentAllowance {
		return i18n.T(ctx, i18n.KeyAdminDocAllowance)
	}
	return i18n.T(ctx, i18n.KeyAdminDocInvoice)
}

func (d InvoiceDocument) Amount() string { return money.TWD(d.AmountCents) }

func (d InvoiceDocument) Voided() bool { return d.Status == invoice.DocumentVoided }

// CanIssueInvoice reports whether to offer the issue button. A pending order
// store credit paid in full is not committed until it is picked, and its
// invoice is owed all the same.
func (v *OrderView) CanIssueInvoice() bool {
	funded := v.Committed || (v.Status == order.FulfillmentPending && !v.Unpaid)
	if !v.InvoicingEnabled || !funded {
		return false
	}
	for _, d := range v.InvoiceDocuments {
		if d.Kind == invoice.DocumentInvoice && !d.Voided() {
			return false
		}
	}
	return true
}

func (v *OrderView) LiveInvoice() (InvoiceDocument, bool) {
	for _, d := range v.InvoiceDocuments {
		if d.Kind == invoice.DocumentInvoice && !d.Voided() {
			return d, true
		}
	}
	return InvoiceDocument{}, false
}

func (v *OrderView) CanVoidInvoice() bool {
	_, ok := v.LiveInvoice()
	return ok && v.InvoicingEnabled
}

// allowanceOutstandingCents derives the presentation estimate from the same
// cumulative facts the database locks and re-derives authoritatively. Filing is
// whole-dollar, capped by the rounded original invoice.
func (v *OrderView) allowanceOutstandingCents() int64 {
	live, ok := v.LiveInvoice()
	if !ok {
		return 0
	}
	outstanding := min((v.RefundedCents/100)*100, live.AmountCents)
	for _, d := range v.InvoiceDocuments {
		if d.Kind == invoice.DocumentAllowance && !d.Voided() {
			outstanding -= d.AmountCents
		}
	}
	return max(outstanding, 0)
}

// CanAllowInvoice reports whether the current read model has a whole-dollar
// refunded delta not already relieved. The database rechecks under lock.
func (v *OrderView) CanAllowInvoice() bool {
	return v.CanVoidInvoice() && v.allowanceOutstandingCents() > 0 && !v.AllowanceOpen()
}

// AllowanceOpen reports an allowance sent to ECPay and not yet settled, which
// holds the invoice's one claim.
func (v *OrderView) AllowanceOpen() bool {
	return v.AllowanceAwaitingUntil != "" || v.AllowanceAttention != ""
}

// AllowanceAttentionText says why a sent allowance needs a person.
func (v *OrderView) AllowanceAttentionText(ctx context.Context) string {
	switch v.AllowanceAttention {
	case invoice.CategoryBuyerUnconfirmed:
		return i18n.T(ctx, i18n.KeyAdminQueueAllowanceUnconfirmed)
	case invoice.CategoryAmountStillHeld:
		return i18n.T(ctx, i18n.KeyAdminQueueAllowanceAmountHeld)
	case invoice.CategorySuccessMismatch:
		return i18n.T(ctx, i18n.KeyAdminQueueAllowanceMismatch)
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminQueueAllowanceAttention), v.AllowanceAttention)
}

// AllowanceAmount is display only; no amount is posted back to the server.
func (v *OrderView) AllowanceAmount() string { return money.TWD(v.allowanceOutstandingCents()) }

func (v *OrderView) HasCustomerNote() bool { return v.CustomerNote != "" }

func (v *OrderView) MailKeptText(ctx context.Context) string {
	days := int(v.MailKept.Hours() / 24)
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminTimelineMailKept), i18n.Count(ctx, i18n.KeyAdminDays, int64(days), days))
}

type VariantsView struct {
	web.Bound

	Variants    []Variant
	SoldOutOnly bool
	Term        string
	Notice      components.Result
	// ShowCover is set on the desk's opening page only: not on a search, the
	// sold out filter or a later page of the list.
	ShowCover   bool
	AtRisk      []StockRisk
	MoreSoldOut int
	// Return is this page's own address, filter and position, which each form
	// posts back so a write returns to the page it was made on.
	Return string
}

func (v VariantsView) AllHref() string { return web.ScopeURL("/admin/stock", "q", v.Term) }

func (v VariantsView) SoldOutHref() string {
	return web.ScopeURL("/admin/stock", "soldout", "1", "q", v.Term)
}

func (v VariantsView) Empty() bool { return len(v.Variants) == 0 }

func Meta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeyAdminPageDashboard)}
}

func OrdersMeta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeyAdminPageOrderList)}
}

func VariantsMeta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeyAdminPageStockList)}
}

func (v *Variant) PriceText() string { return strconv.FormatInt(v.PriceCents/100, 10) }

func (v *Variant) CompareText() string {
	if v.CompareCents <= 0 {
		return ""
	}
	return strconv.FormatInt(v.CompareCents/100, 10)
}

// AdjustKey is the adjustment form's idempotency key. It is spent for good in
// the ledger, so it names the rendered form and not the stock level: stock
// returns to an earlier figure, and a key built from it would then be refused.
func (v *Variant) AdjustKey() string {
	return "adj:" + v.SKU + ":" + v.FormID
}

type Movement struct {
	At          string
	Delta       int32
	Reason      inventory.MovementReason
	OrderNumber string
	Actor       string
	Running     int32
}

func (m Movement) DeltaText() string {
	if m.Delta > 0 {
		return "+" + strconv.FormatInt(int64(m.Delta), 10)
	}
	return strconv.FormatInt(int64(m.Delta), 10)
}

func (m Movement) RunningText() string { return strconv.FormatInt(int64(m.Running), 10) }

func (m Movement) In() bool { return m.Delta > 0 }

func (m Movement) HasOrder() bool { return m.OrderNumber != "" }

func (m Movement) By(ctx context.Context) string {
	if m.Actor == "" {
		return i18n.T(ctx, i18n.KeyAdminActorSystem)
	}
	return m.Actor
}

func (m Movement) ReasonText(ctx context.Context) string {
	switch m.Reason {
	case inventory.ReasonReceipt:
		return i18n.T(ctx, i18n.KeyAdminMoveReceipt)
	case inventory.ReasonHold:
		return i18n.T(ctx, i18n.KeyAdminMoveHold)
	case inventory.ReasonSale:
		return i18n.T(ctx, i18n.KeyAdminMoveSale)
	case inventory.ReasonRelease:
		return i18n.T(ctx, i18n.KeyAdminMoveRelease)
	case inventory.ReasonReturn:
		return i18n.T(ctx, i18n.KeyAdminMoveReturn)
	case inventory.ReasonAdjustment:
		return i18n.T(ctx, i18n.KeyAdminMoveAdjustment)
	default:
		panic("pages: no label for inventory movement reason " + string(m.Reason))
	}
}

type MovementsView struct {
	web.Bound

	SKU         string
	ProductName string
	Slug        string
	Stock       int32
	Safety      int32
	Rows        []Movement
	Days        []StockDay
	Notice      components.Result
	FormID      string
}

// ReceiveKey is the goods-receipt form's idempotency key, named by the rendered
// form for the reason AdjustKey is. Its prefix differs from AdjustKey's so the
// two forms never share a key.
func (v *MovementsView) ReceiveKey() string {
	return "rcv:" + v.SKU + ":" + v.FormID
}

func (v *MovementsView) Empty() bool { return len(v.Rows) == 0 }

func (v *MovementsView) StockText() string { return strconv.FormatInt(int64(v.Stock), 10) }

func (v *MovementsView) SafetyText() string { return strconv.FormatInt(int64(v.Safety), 10) }

// Payment is how an order was paid. Method is empty for an order nothing has
// paid yet.
type Payment struct {
	Method string
	// Card is "Visa •••• 4242", or empty when Stripe reported none.
	Card     string
	Captured string
	PaidAt   string
}

func (p Payment) Paid() bool { return p.Method != "" }

type Refund struct {
	Channel string
	Amount  string
	At      string
	Reason  string
	Staff   string
}
