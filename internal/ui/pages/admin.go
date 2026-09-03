package pages

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/pickup"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// AdminVariant is one row of the stock list.
type AdminVariant struct {
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
}

// StockText is the stock on hand, as text.
func (v AdminVariant) StockText() string { return strconv.FormatInt(int64(v.Stock), 10) }

// SafetyText is the floor below which nothing may be sold.
func (v AdminVariant) SafetyText() string { return strconv.FormatInt(int64(v.Safety), 10) }

// SellableText is how many may actually be sold.
func (v AdminVariant) SellableText() string {
	n := max(v.Stock-v.Safety, 0)
	return strconv.FormatInt(int64(n), 10)
}

// Low reports whether this variant is at or under its safety floor.
func (v AdminVariant) Low() bool { return v.Stock <= v.Safety }

// AdminOrderRow is one row of the order queue.
type AdminOrderRow struct {
	Number     string
	Status     string
	StatusText string
	PlacedAt   string
	Recipient  string
	TotalCents int64
	Committed  bool
}

// Total is what the order came to.
func (o AdminOrderRow) Total() string { return twd(o.TotalCents) }

// AdminTransition is one legal next state for an order.
type AdminTransition struct {
	Value string
	Label string
}

// AdminStatusTab is one filter in the order queue.
type AdminStatusTab struct {
	Value    string
	Label    string
	Count    int64
	Selected bool
}

// CountText is how many orders are in this state.
func (t AdminStatusTab) CountText() string { return strconv.FormatInt(t.Count, 10) }

// AdminDashboardView is the back office landing page.
type AdminDashboardView struct {
	PendingOrders  int64
	PickingOrders  int64
	LowStock       int64
	ActiveProducts int64
	OpenMessages   int64
	Low            []AdminVariant
}

// PendingText is how many orders are waiting to be paid.
func (v AdminDashboardView) PendingText() string { return strconv.FormatInt(v.PendingOrders, 10) }

// PickingText is how many orders are being packed.
func (v AdminDashboardView) PickingText() string { return strconv.FormatInt(v.PickingOrders, 10) }

// LowStockText is how many variants are at or under their floor.
func (v AdminDashboardView) LowStockText() string { return strconv.FormatInt(v.LowStock, 10) }

// ActiveProductsText is how many products are on sale.
func (v AdminDashboardView) ActiveProductsText() string {
	return strconv.FormatInt(v.ActiveProducts, 10)
}

// OpenMessagesText is how many contact messages are unanswered.
func (v AdminDashboardView) OpenMessagesText() string {
	return strconv.FormatInt(v.OpenMessages, 10)
}

// HasLow reports whether anything needs restocking.
func (v AdminDashboardView) HasLow() bool { return len(v.Low) > 0 }

// AdminOrdersView is the order queue.
type AdminOrdersView struct {
	Term     string
	Searched bool
	Status   string
	Orders   []AdminOrderRow
	Tabs     []AdminStatusTab
	Notice   string
}

// Searching reports whether this page is showing search results.
func (v AdminOrdersView) Searching() bool { return v.Searched }

// TermTooShort reports that something was typed and it was not enough to search with.
func (v AdminOrdersView) TermTooShort() bool { return v.Term != "" && !v.Searched }

// Empty reports whether the queue has nothing in this state.
func (v AdminOrdersView) Empty() bool { return len(v.Orders) == 0 }

// HasNotice reports whether to show the banner.
func (v AdminOrdersView) HasNotice() bool { return v.Notice != "" }

// RecipientText is who it is going to, or a note that erase_user has been here.
func (o AdminOrderRow) RecipientText(ctx context.Context) string {
	if o.Recipient == "" {
		return i18n.T(ctx, i18n.KeyAdminErasedRecipient)
	}
	return o.Recipient
}

// AdminOrderView is one order in the back office.
type AdminOrderView struct {
	Number           string
	Status           string
	StatusText       string
	PlacedAt         string
	ShippingName     string
	Lines            []OrderLine
	SubtotalCents    int64
	ShippingCents    int64
	DiscountCents    int64
	DiscountReason   string
	TaxCents         int64
	Email            string
	Recipient        string
	Phone            string
	Address          string
	CustomerNote     string
	StaffNote        string
	InvoiceType      invoice.Preference
	InvoiceCarrier   string
	InvoiceTaxID     string
	InvoiceDocuments []AdminInvoiceDocument
	InvoicingEnabled bool
	// RefundedCents is what has actually gone back, and what a 折讓 relieves.
	RefundedCents int64
	// AllowanceOperationID identifies one rendered allowance form across HTTP
	// retries without collapsing a later, legitimate equal partial allowance.
	AllowanceOperationID string
	Committed            bool
	Next                 []AdminTransition
	CanShip              bool
	Shippable            []AdminShippableLine
	Notice               string
	Timeline             []AdminOrderEvent
	Shipments            []AdminShipment
	Delivery             AdminDelivery
	Correctable          bool
	PickupDestination    bool
	PickupBrands         []PickupBrandChoice
}

// AdminDelivery is the editable delivery detail of one order.
type AdminDelivery struct {
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

// AdminOrderEvent is one step in an order's history, as the shop sees it.
type AdminOrderEvent struct {
	Kind  string
	Note  string
	At    string
	Actor string
}

// LabelKey names the step's message.
func (e AdminOrderEvent) LabelKey() i18n.Key { return OrderEvent{Kind: e.Kind}.LabelKey() }

// By is who did it, in words.
func (e AdminOrderEvent) By(ctx context.Context) string {
	switch {
	case e.Actor != "":
		return e.Actor
	case e.Kind == "cancelled":
		return i18n.T(ctx, i18n.KeyAdminActorCustomer)
	default:
		return i18n.T(ctx, i18n.KeyAdminActorSystem)
	}
}

// AdminShipment is one parcel.
type AdminShipment struct {
	Carrier     string
	Tracking    string
	ShippedAt   string
	DeliveredAt string
}

// Delivered reports whether the parcel has arrived.
func (s AdminShipment) Delivered() bool { return s.DeliveredAt != "" }

// AdminShippableLine is one line still owed a dispatch.
type AdminShippableLine struct {
	OrderLineID string
	SKU         string
	Name        string
	Label       string
	Remaining   int32
	Held        int32
}

// Line is the item as one row of text.
func (l AdminShippableLine) Line() string {
	name := l.Name
	if l.Label != "" {
		name += " · " + l.Label
	}
	return name
}

// RemainingText is how many are left to send.
func (l AdminShippableLine) RemainingText() string {
	return strconv.FormatInt(int64(l.Remaining), 10)
}

// Short reports a hold smaller than the line still owes; that dispatch posts no movement.
func (l AdminShippableLine) Short() bool { return l.Held < l.Remaining }

// HeldText is how many units the order still has reserved for this line.
func (l AdminShippableLine) HeldText() string {
	return strconv.FormatInt(int64(l.Held), 10)
}

// Subtotal is what the lines came to.
func (v *AdminOrderView) Subtotal() string { return twd(v.SubtotalCents) }

// Shipping is what delivery cost.
func (v *AdminOrderView) Shipping() string { return twd(v.ShippingCents) }

// Total is what the order came to.
func (v *AdminOrderView) Total() string {
	return twd(v.SubtotalCents - v.DiscountCents + v.ShippingCents + v.TaxCents)
}

// Discounted reports whether anything came off this order.
func (v *AdminOrderView) Discounted() bool { return v.DiscountCents > 0 }

// Discount is what came off, as a negative figure.
func (v *AdminOrderView) Discount() string {
	return "-" + twd(v.DiscountCents)
}

// CanAdvance reports whether this order has any legal move left.
func (v *AdminOrderView) CanAdvance() bool { return len(v.Next) > 0 }

// HasNotice reports whether to show the banner.
func (v *AdminOrderView) HasNotice() bool { return v.Notice != "" }

// RecipientText is who it is going to, or a note that erase_user has been here.
func (v *AdminOrderView) RecipientText(ctx context.Context) string {
	if v.Recipient == "" {
		return i18n.T(ctx, i18n.KeyAdminErasedRecipient)
	}
	return v.Recipient
}

// HasInvoice reports whether the customer stated an invoice preference.
func (v *AdminOrderView) HasInvoice() bool { return v.InvoiceType != "" }

// InvoiceText is the preference in words, with the detail that goes with it.
func (v *AdminOrderView) InvoiceText(ctx context.Context) string {
	switch v.InvoiceType {
	case invoice.PreferenceMember:
		return i18n.T(ctx, i18n.KeyAdminCarrierMember)
	case invoice.PreferenceMobile:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminCarrierMobile), v.InvoiceCarrier)
	case invoice.PreferenceCompany:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminCarrierTaxID), v.InvoiceTaxID)
	default:
		panic("pages: no label for invoice type " + string(v.InvoiceType))
	}
}

// AdminInvoiceDocument is one uniform invoice or credit note filed against an order.
type AdminInvoiceDocument struct {
	Kind   string
	Number string
	// ProviderRef is the four-digit random code a void needs alongside the number.
	ProviderRef string
	AmountCents int64
	Status      string
	IssuedAt    string
	Lines       []AdminInvoiceLine
}

// AdminInvoiceLine is one item on a filed document.
type AdminInvoiceLine struct {
	Description string
	Quantity    int32
	AmountCents int64
}

// Line is the item as one row of text.
func (l AdminInvoiceLine) Line() string {
	return l.Description + " × " + strconv.FormatInt(int64(l.Quantity), 10) + " · " + twd(l.AmountCents)
}

// KindText names the document.
func (d AdminInvoiceDocument) KindText(ctx context.Context) string {
	if d.Kind == "allowance" {
		return i18n.T(ctx, i18n.KeyAdminDocAllowance)
	}
	return i18n.T(ctx, i18n.KeyAdminDocInvoice)
}

// Amount is what it is for.
func (d AdminInvoiceDocument) Amount() string { return twd(d.AmountCents) }

// Voided reports whether it has been cancelled.
func (d AdminInvoiceDocument) Voided() bool { return d.Status == "voided" }

// Pending reports a CLAIM: a row holding its request key while the provider is
// asked, with no number yet because allocating one is the 加值中心's job. It
// must render as a claim and not as a filed document — nothing is at the
// 加值中心 under it yet.
func (d AdminInvoiceDocument) Pending() bool { return d.Status == "pending" }

// CanIssueInvoice reports whether to offer the issue button.
func (v *AdminOrderView) CanIssueInvoice() bool {
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
func (v *AdminOrderView) LiveInvoice() (AdminInvoiceDocument, bool) {
	for _, d := range v.InvoiceDocuments {
		if d.Kind == "invoice" && !d.Voided() {
			return d, true
		}
	}
	return AdminInvoiceDocument{}, false
}

// CanVoidInvoice reports whether there is a live invoice to cancel.
func (v *AdminOrderView) CanVoidInvoice() bool {
	_, ok := v.LiveInvoice()
	return ok && v.InvoicingEnabled
}

// allowanceOutstandingCents derives the presentation estimate from the same
// cumulative facts the database locks and re-derives authoritatively. Filing is
// whole-dollar, capped by the rounded original invoice.
func (v *AdminOrderView) allowanceOutstandingCents() int64 {
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
func (v *AdminOrderView) CanAllowInvoice() bool {
	return v.CanVoidInvoice() && v.allowanceOutstandingCents() > 0
}

// AllowanceAmount is display only; no amount is posted back to the server.
func (v *AdminOrderView) AllowanceAmount() string { return twd(v.allowanceOutstandingCents()) }

// HasCustomerNote reports whether the customer left one.
func (v *AdminOrderView) HasCustomerNote() bool { return v.CustomerNote != "" }

// AdminVariantsView is the stock list.
type AdminVariantsView struct {
	Variants []AdminVariant
	LowOnly  bool
	Notice   string
}

// Empty reports whether the list has nothing in it.
func (v AdminVariantsView) Empty() bool { return len(v.Variants) == 0 }

// HasNotice reports whether to show the banner.
func (v AdminVariantsView) HasNotice() bool { return v.Notice != "" }

// AdminMeta is the dashboard's chrome.
func AdminMeta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeyAdminPageDashboard)}
}

// AdminOrdersMeta is the order queue's chrome.
func AdminOrdersMeta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeyAdminPageOrderList)}
}

// AdminVariantsMeta is the stock list's chrome.
func AdminVariantsMeta(ctx context.Context) layouts.Page {
	return layouts.Page{Title: i18n.T(ctx, i18n.KeyAdminPageStockList)}
}

// PriceText is the price in whole dollars, which is what the form's number input carries.
func (v AdminVariant) PriceText() string { return strconv.FormatInt(v.PriceCents/100, 10) }

// CompareText is the compare-at price, empty when the variant is not on sale.
func (v AdminVariant) CompareText() string {
	if v.CompareCents <= 0 {
		return ""
	}
	return strconv.FormatInt(v.CompareCents/100, 10)
}

// AdjustKey is the adjustment form's idempotency key.
func (v AdminVariant) AdjustKey() string {
	return "adj:" + v.SKU + ":" + strconv.FormatInt(int64(v.Stock), 10)
}

// AdminMovement is one row of a variant's stock ledger.
type AdminMovement struct {
	At          string
	Delta       int32
	Reason      string
	OrderNumber string
	Actor       string
	Running     int32
}

// DeltaText is the movement with its sign.
func (m AdminMovement) DeltaText() string {
	if m.Delta > 0 {
		return "+" + strconv.FormatInt(int64(m.Delta), 10)
	}
	return strconv.FormatInt(int64(m.Delta), 10)
}

// RunningText is the stock after this movement.
func (m AdminMovement) RunningText() string { return strconv.FormatInt(int64(m.Running), 10) }

// In reports whether stock came in.
func (m AdminMovement) In() bool { return m.Delta > 0 }

// HasOrder reports whether this movement names an order.
func (m AdminMovement) HasOrder() bool { return m.OrderNumber != "" }

// By is who caused it, in words.
func (m AdminMovement) By(ctx context.Context) string {
	if m.Actor == "" {
		return i18n.T(ctx, i18n.KeyAdminActorSystem)
	}
	return m.Actor
}

// ReasonText is why, in the back office's language.
func (m AdminMovement) ReasonText(ctx context.Context) string {
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

// AdminMovementsView is one variant's stock ledger.
type AdminMovementsView struct {
	SKU         string
	ProductName string
	Slug        string
	Stock       int32
	Safety      int32
	Rows        []AdminMovement
	Notice      string
}

// HasNotice reports whether to show the banner.
func (v *AdminMovementsView) HasNotice() bool { return v.Notice != "" }

// ReceiveKey is the goods-receipt form's idempotency key. Its prefix differs from
// AdjustKey's, or a correction and a delivery against the same figure collide.
func (v *AdminMovementsView) ReceiveKey() string {
	return "rcv:" + v.SKU + ":" + strconv.FormatInt(int64(v.Stock), 10)
}

// Empty reports whether nothing has ever moved.
func (v *AdminMovementsView) Empty() bool { return len(v.Rows) == 0 }

// StockText is the current stock.
func (v *AdminMovementsView) StockText() string { return strconv.FormatInt(int64(v.Stock), 10) }

// SafetyText is the floor below which nothing may be sold.
func (v *AdminMovementsView) SafetyText() string { return strconv.FormatInt(int64(v.Safety), 10) }
