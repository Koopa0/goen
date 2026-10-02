package admin

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/carrier"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/pickup"
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
	Number     string
	Status     pages.FulfillmentStatus
	StatusText string
	PlacedAt   string
	Recipient  string
	TotalCents int64
	Committed  bool
}

func (o OrderRow) Total() string { return money.TWD(o.TotalCents) }

type Transition struct {
	Value pages.FulfillmentStatus
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
	PendingOrders  int64
	ReadyOrders    int64
	PickingOrders  int64
	LowStock       int64
	ActiveProducts int64
	OpenMessages   int64
	// PendingReturns is the requests nobody has decided, and OldestReturnDays how
	// many shop days ago the oldest of them was filed. It is the operator's own
	// wait, not the consumer's seven days: for a request already filed that window
	// is no clock of theirs.
	PendingReturns      int64
	OldestReturnDays    int64
	UnansweredQuestions int64
	Recent              []OrderRow
	Low                 []Variant
}

func (v DashboardView) PendingText() string { return strconv.FormatInt(v.PendingOrders, 10) }

func (v DashboardView) ReadyText() string { return strconv.FormatInt(v.ReadyOrders, 10) }

func (v DashboardView) PickingText() string { return strconv.FormatInt(v.PickingOrders, 10) }

func (v DashboardView) LowStockText() string { return strconv.FormatInt(v.LowStock, 10) }

func (v DashboardView) ActiveProductsText() string {
	return strconv.FormatInt(v.ActiveProducts, 10)
}

func (v DashboardView) OpenMessagesText() string {
	return strconv.FormatInt(v.OpenMessages, 10)
}

func (v DashboardView) PendingReturnsText() string {
	return strconv.FormatInt(v.PendingReturns, 10)
}

func (v DashboardView) ReturnsAgeNote(ctx context.Context) string {
	switch {
	case v.PendingReturns == 0:
		return ""
	case v.OldestReturnDays <= 0:
		return i18n.T(ctx, i18n.KeyAdminQueueStatReturnsToday)
	default:
		return i18n.Count(ctx, i18n.KeyAdminQueueStatReturnsAge, v.OldestReturnDays, v.OldestReturnDays)
	}
}

func (v DashboardView) UnansweredQuestionsText() string {
	return strconv.FormatInt(v.UnansweredQuestions, 10)
}

func (v DashboardView) HasLow() bool { return len(v.Low) > 0 }

type OrdersView struct {
	pages.ListBound

	Term     string
	Searched bool
	Status   QueueFilter
	Orders   []OrderRow
	Tabs     []StatusTab
	Notice   string
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

func (v OrdersView) HasNotice() bool { return v.Notice != "" }

func (o OrderRow) RecipientText(ctx context.Context) string {
	if o.Recipient == "" {
		return i18n.T(ctx, i18n.KeyAdminErasedRecipient)
	}
	return o.Recipient
}

type OrderView struct {
	Number               string
	Status               pages.FulfillmentStatus
	StatusText           string
	PlacedAt             string
	ShippingName         string
	Lines                []pages.OrderLine
	SubtotalCents        int64
	ShippingCents        int64
	DiscountCents        int64
	DiscountReason       string
	TaxCents             int64
	Email                string
	Recipient            string
	Phone                string
	Address              string
	CustomerNote         string
	StaffNote            string
	InvoiceType          invoice.Preference
	InvoiceMobileBarcode string
	InvoiceDonationCode  string
	InvoiceTaxID         string
	InvoiceDocuments     []InvoiceDocument
	InvoicingEnabled     bool
	// RefundedCents is what has actually gone back, and what a 折讓 relieves.
	RefundedCents int64
	// AllowanceOperationID identifies one rendered allowance form across HTTP
	// retries without collapsing a later, legitimate equal partial allowance.
	AllowanceOperationID string
	Committed            bool
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
	Notice                    string
	Timeline                  []OrderEvent
	Shipments                 []Shipment
	DeliveryError             string
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
	// exists for; RefundOpen is one whose refund before shipment Resume finishes.
	RefundOffered bool
	RefundOpen    bool
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

type OrderEvent struct {
	Kind  pages.OrderEventKind
	Note  string
	At    string
	Actor string
	// System is an event no person made, such as the payment-deadline cancel.
	System bool
}

func (e OrderEvent) LabelKey() i18n.Key { return pages.OrderEvent{Kind: e.Kind}.LabelKey() }

func (e OrderEvent) By(ctx context.Context) string {
	switch {
	case e.Actor != "":
		return e.Actor
	case e.System:
		return i18n.T(ctx, i18n.KeyAdminActorSystem)
	case e.Kind == pages.EventCancelled:
		return i18n.T(ctx, i18n.KeyAdminActorCustomer)
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
	return len(v.Next) == 1 && v.Next[0].Value == pages.FulfillmentPicking
}

func (v *OrderView) CanAdvance() bool { return len(v.Next) > 0 }

// NextIsDestructive reports that the first move offered is the cancellation, so
// the menu must not preselect it.
func (v *OrderView) NextIsDestructive() bool {
	return len(v.Next) > 0 && v.Next[0].Value == pages.FulfillmentCancelled
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
	return v.Status == pages.FulfillmentCompleted || v.Status == pages.FulfillmentCancelled
}

func (v *OrderView) HasNotice() bool { return v.Notice != "" }

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

// Pending reports a CLAIM: a row holding its request key while the provider is
// asked, with no number yet because allocating one is the 加值中心's job. It
// must render as a claim and not as a filed document — nothing is at the
// 加值中心 under it yet.
func (d InvoiceDocument) Pending() bool { return d.Status == "pending" }

// CanIssueInvoice reports whether to offer the issue button. A pending order
// store credit paid in full is not committed until it is picked, and its
// invoice is owed all the same.
func (v *OrderView) CanIssueInvoice() bool {
	funded := v.Committed || (v.Status == pages.FulfillmentPending && !v.Unpaid)
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
	return v.CanVoidInvoice() && v.allowanceOutstandingCents() > 0
}

// AllowanceAmount is display only; no amount is posted back to the server.
func (v *OrderView) AllowanceAmount() string { return money.TWD(v.allowanceOutstandingCents()) }

func (v *OrderView) HasCustomerNote() bool { return v.CustomerNote != "" }

type VariantsView struct {
	pages.ListBound

	Variants []Variant
	LowOnly  bool
	Term     string
	Notice   string
	// Return is this page's own address, filter and position, which each form
	// posts back so a write returns to the page it was made on.
	Return string
}

func (v VariantsView) AllHref() string { return web.ScopeURL("/admin/stock", "q", v.Term) }

func (v VariantsView) LowHref() string { return web.ScopeURL("/admin/stock", "low", "1", "q", v.Term) }

func (v VariantsView) Empty() bool { return len(v.Variants) == 0 }

func (v VariantsView) HasNotice() bool { return v.Notice != "" }

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
	Reason      string
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
	case "receipt":
		return i18n.T(ctx, i18n.KeyAdminMoveReceipt)
	case "hold":
		return i18n.T(ctx, i18n.KeyAdminMoveHold)
	case "sale":
		return i18n.T(ctx, i18n.KeyAdminMoveSale)
	case "release":
		return i18n.T(ctx, i18n.KeyAdminMoveRelease)
	case "return":
		return i18n.T(ctx, i18n.KeyAdminMoveReturn)
	case "adjustment":
		return i18n.T(ctx, i18n.KeyAdminMoveAdjustment)
	default:
		panic("pages: no label for inventory movement reason " + m.Reason)
	}
}

type MovementsView struct {
	pages.ListBound

	SKU         string
	ProductName string
	Slug        string
	Stock       int32
	Safety      int32
	Rows        []Movement
	Notice      string
	FormID      string
}

func (v *MovementsView) HasNotice() bool { return v.Notice != "" }

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
