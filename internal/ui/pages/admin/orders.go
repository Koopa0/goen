package admin

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/carrier"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/pickup"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
)

// Variant is one row of the stock list.
type Variant struct {
	SKU           string
	Slug          string
	ProductName   string
	Brand         string
	PriceCents    int64
	CompareCents  int64
	Stock         int32
	Safety        int32
	Active        bool
	ProductStatus string
	// FormID is unique to one rendering of this row's adjust form.
	FormID string
	// DraftDelta is what staff typed in a refused adjustment and DeltaError the
	// sentence under it.
	DraftDelta, DeltaError string
}

// StockText is the stock on hand, as text.
func (v Variant) StockText() string { return strconv.FormatInt(int64(v.Stock), 10) }

// SafetyText is the floor below which nothing may be sold.
func (v Variant) SafetyText() string { return strconv.FormatInt(int64(v.Safety), 10) }

// SellableText is how many may actually be sold.
func (v Variant) SellableText() string {
	n := max(v.Stock-v.Safety, 0)
	return strconv.FormatInt(int64(n), 10)
}

// Low reports whether this variant is at or under its safety floor.
func (v Variant) Low() bool { return v.Stock <= v.Safety }

// OrderRow is one row of the order queue.
type OrderRow struct {
	Number     string
	Status     pages.FulfillmentStatus
	StatusText string
	PlacedAt   string
	Recipient  string
	TotalCents int64
	Committed  bool
}

// Total is what the order came to.
func (o OrderRow) Total() string { return money.TWD(o.TotalCents) }

// Transition is one legal next state for an order.
type Transition struct {
	Value pages.FulfillmentStatus
	Label string
}

// QueueFilter is one view of the orders queue. It is not a fulfilment status:
// pending orders are split by whether money is still owed, so the filters are
// their own closed set. The values are the ?status= query values.
type QueueFilter string

// The orders queue's filters; QueueAll is every order.
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

// StatusTab is one filter in the order queue.
type StatusTab struct {
	Value    QueueFilter
	Label    string
	Count    int64
	Selected bool
}

// CountText is how many orders are in this state.
func (t StatusTab) CountText() string { return strconv.FormatInt(t.Count, 10) }

// DashboardView is the back office landing page.
//
// Recent is the queue the page is read for: the newest orders, whatever state
// they are in. Filtering it to one state would answer a question the tiles
// above it already answer, and hide the order somebody walked over to ask
// about.
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

// PendingText is how many orders are waiting to be paid.
func (v DashboardView) PendingText() string { return strconv.FormatInt(v.PendingOrders, 10) }

// ReadyText is how many funded orders are waiting to be picked.
func (v DashboardView) ReadyText() string { return strconv.FormatInt(v.ReadyOrders, 10) }

// PickingText is how many orders are being packed.
func (v DashboardView) PickingText() string { return strconv.FormatInt(v.PickingOrders, 10) }

// LowStockText is how many variants are at or under their floor.
func (v DashboardView) LowStockText() string { return strconv.FormatInt(v.LowStock, 10) }

// ActiveProductsText is how many products are on sale.
func (v DashboardView) ActiveProductsText() string {
	return strconv.FormatInt(v.ActiveProducts, 10)
}

// OpenMessagesText is how many contact messages are unanswered.
func (v DashboardView) OpenMessagesText() string {
	return strconv.FormatInt(v.OpenMessages, 10)
}

// PendingReturnsText is how many return requests wait for a decision.
func (v DashboardView) PendingReturnsText() string {
	return strconv.FormatInt(v.PendingReturns, 10)
}

// ReturnsAgeNote is the line under the returns tile: how long the oldest open
// request has waited. Empty when nothing is waiting.
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

// UnansweredQuestionsText is how many questions the shop still owes an answer.
func (v DashboardView) UnansweredQuestionsText() string {
	return strconv.FormatInt(v.UnansweredQuestions, 10)
}

// HasLow reports whether anything needs restocking.
func (v DashboardView) HasLow() bool { return len(v.Low) > 0 }

// OrdersView is the order queue.
type OrdersView struct {
	pages.ListBound

	Term     string
	Searched bool
	Status   QueueFilter
	Orders   []OrderRow
	Tabs     []StatusTab
	Notice   string
}

// Searching reports whether this page is showing search results.
func (v OrdersView) Searching() bool { return v.Searched }

// TermTooShort reports that something was typed and it was not enough to search with.
func (v OrdersView) TermTooShort() bool { return v.Term != "" && !v.Searched }

// Empty reports whether the queue has nothing in this state.
func (v OrdersView) Empty() bool { return len(v.Orders) == 0 }

// EmptyText is the empty-state headline: a search miss, an empty shop, or an empty status tab.
func (v OrdersView) EmptyText(ctx context.Context) string {
	if v.Searching() {
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminQueueNoneFound), v.Term)
	}
	if v.Status == "" {
		return i18n.T(ctx, i18n.KeyAdminQueueNoneYet)
	}
	return i18n.T(ctx, i18n.KeyAdminQueueEmpty)
}

// HasNotice reports whether to show the banner.
func (v OrdersView) HasNotice() bool { return v.Notice != "" }

// RecipientText is who it is going to, or a note that erase_user has been here.
func (o OrderRow) RecipientText(ctx context.Context) string {
	if o.Recipient == "" {
		return i18n.T(ctx, i18n.KeyAdminErasedRecipient)
	}
	return o.Recipient
}

// OrderView is one order in the back office.
type OrderView struct {
	Number              string
	Status              pages.FulfillmentStatus
	StatusText          string
	PlacedAt            string
	ShippingName        string
	Lines               []pages.OrderLine
	SubtotalCents       int64
	ShippingCents       int64
	DiscountCents       int64
	DiscountReason      string
	TaxCents            int64
	Email               string
	Recipient           string
	Phone               string
	Address             string
	CustomerNote        string
	StaffNote           string
	InvoiceType         invoice.Preference
	InvoiceCarrier      string
	InvoiceDonationCode string
	InvoiceTaxID        string
	InvoiceDocuments    []InvoiceDocument
	InvoicingEnabled    bool
	// RefundedCents is what has actually gone back, and what a 折讓 relieves.
	RefundedCents int64
	// AllowanceOperationID identifies one rendered allowance form across HTTP
	// retries without collapsing a later, legitimate equal partial allowance.
	AllowanceOperationID string
	Committed            bool
	// Unpaid is a pending order that still owes money and has no payment: the
	// database refuses to move it into picking.
	Unpaid        bool
	Next          []Transition
	CanShip       bool
	Shippable     []ShippableLine
	Notice        string
	Timeline      []OrderEvent
	Shipments     []Shipment
	DeliveryError string
	// ShipCarrier and ShipTracking keep what staff typed when the dispatch was
	// refused; TrackingError marks the tracking field invalid.
	ShipCarrier, ShipTracking string
	TrackingError             string
	ShipCarrierError          string
	// ShipQtyError marks every quantity field of a refused dispatch, and
	// ShipQty keeps what was typed in each, by order line id.
	ShipQtyError      string
	ShipQty           map[string]string
	Delivery          Delivery
	Correctable       bool
	PickupDestination bool
	PickupBrands      []pages.PickupBrandChoice

	// RefundOffered is a paid order nothing has shipped from and no return
	// exists for; RefundOpen is one whose refund before shipment Resume finishes.
	RefundOffered bool
	RefundOpen    bool
}

// Delivery is the editable delivery detail of one order.
type Delivery struct {
	Email     string
	Recipient string
	Phone     string

	PostalCode string
	City       string
	District   string
	Street     string

	PickupBrand     pickup.Brand
	PickupStoreCode string
	PickupStoreName string
}

// OrderEvent is one step in an order's history, as the shop sees it.
type OrderEvent struct {
	Kind  string
	Note  string
	At    string
	Actor string
	// System is an event no person made, such as the payment-deadline cancel.
	System bool
}

// LabelKey names the step's message.
func (e OrderEvent) LabelKey() i18n.Key { return pages.OrderEvent{Kind: e.Kind}.LabelKey() }

// By is who did it, in words.
func (e OrderEvent) By(ctx context.Context) string {
	switch {
	case e.Actor != "":
		return e.Actor
	case e.System:
		return i18n.T(ctx, i18n.KeyAdminActorSystem)
	case e.Kind == "cancelled":
		return i18n.T(ctx, i18n.KeyAdminActorCustomer)
	default:
		return i18n.T(ctx, i18n.KeyAdminActorSystem)
	}
}

// Shipment is one parcel.
type Shipment struct {
	Carrier     carrier.Carrier
	Tracking    string
	ShippedAt   string
	DeliveredAt string
}

// Delivered reports whether the parcel has arrived.
func (s Shipment) Delivered() bool { return s.DeliveredAt != "" }

// ShippableLine is one line still owed a dispatch.
type ShippableLine struct {
	OrderLineID string
	SKU         string
	Name        string
	Label       string
	Remaining   int32
	Held        int32
}

// Line is the item as one row of text.
func (l ShippableLine) Line() string {
	name := l.Name
	if l.Label != "" {
		name += " · " + l.Label
	}
	return name
}

// RemainingText is how many are left to send.
func (l ShippableLine) RemainingText() string {
	return strconv.FormatInt(int64(l.Remaining), 10)
}

// Short reports a hold smaller than the line still owes; that dispatch posts no movement.
func (l ShippableLine) Short() bool { return l.Held < l.Remaining }

// HeldText is how many units the order still has reserved for this line.
func (l ShippableLine) HeldText() string {
	return strconv.FormatInt(int64(l.Held), 10)
}

// Subtotal is what the lines came to.
func (v *OrderView) Subtotal() string { return money.TWD(v.SubtotalCents) }

// Shipping is what delivery cost.
func (v *OrderView) Shipping() string { return money.TWD(v.ShippingCents) }

// Total is what the order came to.
func (v *OrderView) Total() string {
	return money.TWD(v.SubtotalCents - v.DiscountCents + v.ShippingCents + v.TaxCents)
}

// Discounted reports whether anything came off this order.
func (v *OrderView) Discounted() bool { return v.DiscountCents > 0 }

// Discount is what came off, as a negative figure.
func (v *OrderView) Discount() string {
	return "-" + money.TWD(v.DiscountCents)
}

// CanAdvance reports whether this order has any legal move left.
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

// HasNotice reports whether to show the banner.
func (v *OrderView) HasNotice() bool { return v.Notice != "" }

// RecipientText is who it is going to, or a note that erase_user has been here.
func (v *OrderView) RecipientText(ctx context.Context) string {
	if v.Recipient == "" {
		return i18n.T(ctx, i18n.KeyAdminErasedRecipient)
	}
	return v.Recipient
}

// HasInvoice reports whether the customer stated an invoice preference.
func (v *OrderView) HasInvoice() bool { return v.InvoiceType != "" }

// InvoiceText is the preference in words, with the detail that goes with it.
func (v *OrderView) InvoiceText(ctx context.Context) string {
	switch v.InvoiceType {
	case invoice.PreferenceMember:
		return i18n.T(ctx, i18n.KeyAdminCarrierMember)
	case invoice.PreferenceMobile:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminCarrierMobile), v.InvoiceCarrier)
	case invoice.PreferenceDonate:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminInvoiceDonate), v.InvoiceDonationCode)
	case invoice.PreferenceCompany:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminCarrierTaxID), v.InvoiceTaxID)
	default:
		panic("pages: no label for invoice type " + string(v.InvoiceType))
	}
}

// InvoiceDocument is one uniform invoice or credit note filed against an order.
type InvoiceDocument struct {
	Kind   string
	Number string
	// ProviderRef is the four-digit random code a void needs alongside the number.
	ProviderRef string
	AmountCents int64
	Status      string
	IssuedAt    string
	Lines       []InvoiceLine
}

// InvoiceLine is one item on a filed document.
type InvoiceLine struct {
	Description string
	Quantity    int32
	AmountCents int64
}

// Line is the item as one row of text.
func (l InvoiceLine) Line() string {
	return l.Description + " × " + strconv.FormatInt(int64(l.Quantity), 10) + " · " + money.TWD(l.AmountCents)
}

// KindText names the document.
func (d InvoiceDocument) KindText(ctx context.Context) string {
	if d.Kind == "allowance" {
		return i18n.T(ctx, i18n.KeyAdminDocAllowance)
	}
	return i18n.T(ctx, i18n.KeyAdminDocInvoice)
}

// Amount is what it is for.
func (d InvoiceDocument) Amount() string { return money.TWD(d.AmountCents) }

// Voided reports whether it has been cancelled.
func (d InvoiceDocument) Voided() bool { return d.Status == "voided" }

// Pending reports a CLAIM: a row holding its request key while the provider is
// asked, with no number yet because allocating one is the 加值中心's job. It
// must render as a claim and not as a filed document — nothing is at the
// 加值中心 under it yet.
func (d InvoiceDocument) Pending() bool { return d.Status == "pending" }

// CanIssueInvoice reports whether to offer the issue button.
func (v *OrderView) CanIssueInvoice() bool {
	if !v.InvoicingEnabled || !v.Committed {
		return false
	}
	for _, d := range v.InvoiceDocuments {
		if d.Kind == "invoice" && !d.Voided() {
			return false
		}
	}
	return true
}

// LiveInvoice is the invoice standing against this order, if any.
func (v *OrderView) LiveInvoice() (InvoiceDocument, bool) {
	for _, d := range v.InvoiceDocuments {
		if d.Kind == "invoice" && !d.Voided() {
			return d, true
		}
	}
	return InvoiceDocument{}, false
}

// CanVoidInvoice reports whether there is a live invoice to cancel.
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
		if d.Kind == "allowance" && !d.Voided() {
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

// HasCustomerNote reports whether the customer left one.
func (v *OrderView) HasCustomerNote() bool { return v.CustomerNote != "" }

// VariantsView is the stock list.
type VariantsView struct {
	pages.ListBound

	Variants []Variant
	LowOnly  bool
	Notice   string
	// Return is this page's own address, filter and position, which each form
	// posts back so a write returns to the page it was made on.
	Return string
}

// Empty reports whether the list has nothing in it.
func (v VariantsView) Empty() bool { return len(v.Variants) == 0 }

// HasNotice reports whether to show the banner.
func (v VariantsView) HasNotice() bool { return v.Notice != "" }

// Meta is the dashboard's chrome.
func Meta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeyAdminPageDashboard)}
}

// OrdersMeta is the order queue's chrome.
func OrdersMeta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeyAdminPageOrderList)}
}

// VariantsMeta is the stock list's chrome.
func VariantsMeta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeyAdminPageStockList)}
}

// PriceText is the price in whole dollars, which is what the form's number input carries.
func (v Variant) PriceText() string { return strconv.FormatInt(v.PriceCents/100, 10) }

// CompareText is the compare-at price, empty when the variant is not on sale.
func (v Variant) CompareText() string {
	if v.CompareCents <= 0 {
		return ""
	}
	return strconv.FormatInt(v.CompareCents/100, 10)
}

// AdjustKey is the adjustment form's idempotency key. It is spent for good in
// the ledger, so it names the rendered form and not the stock level: stock
// returns to an earlier figure, and a key built from it would then be refused.
func (v Variant) AdjustKey() string {
	return "adj:" + v.SKU + ":" + v.FormID
}

// Movement is one row of a variant's stock ledger.
type Movement struct {
	At          string
	Delta       int32
	Reason      string
	OrderNumber string
	Actor       string
	Running     int32
}

// DeltaText is the movement with its sign.
func (m Movement) DeltaText() string {
	if m.Delta > 0 {
		return "+" + strconv.FormatInt(int64(m.Delta), 10)
	}
	return strconv.FormatInt(int64(m.Delta), 10)
}

// RunningText is the stock after this movement.
func (m Movement) RunningText() string { return strconv.FormatInt(int64(m.Running), 10) }

// In reports whether stock came in.
func (m Movement) In() bool { return m.Delta > 0 }

// HasOrder reports whether this movement names an order.
func (m Movement) HasOrder() bool { return m.OrderNumber != "" }

// By is who caused it, in words.
func (m Movement) By(ctx context.Context) string {
	if m.Actor == "" {
		return i18n.T(ctx, i18n.KeyAdminActorSystem)
	}
	return m.Actor
}

// ReasonText is why, in the back office's language.
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

// MovementsView is one variant's stock ledger.
type MovementsView struct {
	pages.ListBound

	SKU         string
	ProductName string
	Slug        string
	Stock       int32
	Safety      int32
	Rows        []Movement
	Notice      string
	// FormID is unique to one rendering of the goods-receipt form.
	FormID string
}

// HasNotice reports whether to show the banner.
func (v *MovementsView) HasNotice() bool { return v.Notice != "" }

// ReceiveKey is the goods-receipt form's idempotency key, named by the rendered
// form for the reason AdjustKey is. Its prefix differs from AdjustKey's so the
// two forms never share a key.
func (v *MovementsView) ReceiveKey() string {
	return "rcv:" + v.SKU + ":" + v.FormID
}

// Empty reports whether nothing has ever moved.
func (v *MovementsView) Empty() bool { return len(v.Rows) == 0 }

// StockText is the current stock.
func (v *MovementsView) StockText() string { return strconv.FormatInt(int64(v.Stock), 10) }

// SafetyText is the floor below which nothing may be sold.
func (v *MovementsView) SafetyText() string { return strconv.FormatInt(int64(v.Safety), 10) }
