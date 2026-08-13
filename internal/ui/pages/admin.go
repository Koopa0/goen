package pages

import (
	"context"
	"fmt"
	"strconv"

	"github.com/koopa0/goen/internal/i18n"
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

// Price is the variant's price.
func (v AdminVariant) Price() string { return twd(v.PriceCents) }

// StockText and SafetyText are the numbers as text.
func (v AdminVariant) StockText() string { return strconv.FormatInt(int64(v.Stock), 10) }

// SafetyText is the floor below which nothing may be sold.
func (v AdminVariant) SafetyText() string { return strconv.FormatInt(int64(v.Safety), 10) }

// SellableText is how many may actually be sold — stock above the floor, which
// is what record_inventory_movement will allow.
func (v AdminVariant) SellableText() string {
	n := v.Stock - v.Safety
	if n < 0 {
		n = 0
	}
	return strconv.FormatInt(int64(n), 10)
}

// Low reports whether this variant is at or under its floor, which is what the
// back office is looking for.
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
	// Term is what was TYPED, echoed so the box keeps it.
	Term string
	// Searched reports whether a search actually RAN. Distinct from Term being
	// non-empty, because a term below the minimum is a term nobody searched for —
	// collapsing the two made the page claim results it had never looked for.
	Searched bool
	Status   string
	Orders   []AdminOrderRow
	Counts   map[string]int64
	Notice   string
}

// Searching reports whether this page is showing search RESULTS.
func (v AdminOrdersView) Searching() bool { return v.Searched }

// TermTooShort reports that something was typed and it was not enough to search
// with. Said out loud rather than silently falling back to the queue, which is a
// page that looks like an answer and is not.
func (v AdminOrdersView) TermTooShort() bool { return v.Term != "" && !v.Searched }

// Tabs is the status filter, with counts.
func (v AdminOrdersView) Tabs(ctx context.Context) []AdminStatusTab {
	labels := []struct {
		value string
		label i18n.Key
	}{
		// 全部 carries an EMPTY value because it is not a status — it is the
		// absence of the filter.
		{"", i18n.KeyAdminTabAll},
		{"pending", i18n.KeyAdminStatusPending},
		{"picking", i18n.KeyAdminStatusPicking},
		{"shipped", i18n.KeyAdminStatusShipped},
		{"delivered", i18n.KeyAdminStatusDelivered},
		{"completed", i18n.KeyAdminStatusCompleted},
		{"cancelled", i18n.KeyAdminStatusCancelled},
	}
	tabs := make([]AdminStatusTab, 0, len(labels))
	for _, l := range labels {
		var n int64
		if l.value == "" {
			for _, c := range v.Counts {
				n += c
			}
		} else {
			n = v.Counts[l.value]
		}
		tabs = append(tabs, AdminStatusTab{
			Value: l.value, Label: i18n.T(ctx, l.label), Count: n, Selected: l.value == v.Status,
		})
	}
	return tabs
}

// Empty reports whether the queue has nothing in this state.
func (v AdminOrdersView) Empty() bool { return len(v.Orders) == 0 }

// HasNotice reports whether to show the banner.
func (v AdminOrdersView) HasNotice() bool { return v.Notice != "" }

// AdminOrderView is one order in the back office.
type AdminOrderView struct {
	Number        string
	Status        string
	StatusText    string
	PlacedAt      string
	ShippingName  string
	Lines         []OrderLine
	SubtotalCents int64
	ShippingCents int64
	DiscountCents int64
	// DiscountReason is which coupon, or "" for an order that had none.
	DiscountReason string
	TaxCents       int64
	Email          string
	Recipient      string
	Phone          string
	Address        string
	CustomerNote   string
	StaffNote      string
	// The 發票 the customer asked for. Collected at checkout and read by nothing
	// until now, which meant somebody issuing one by hand — the only way, until the
	// 加值中心 integration exists — could not see what to issue.
	InvoiceType    string
	InvoiceCarrier string
	InvoiceTaxID   string
	// InvoiceDocuments is what has actually been FILED — the preference above
	// says what the customer asked for, and these are the 統一發票 and 折讓 that
	// exist because of it. The distinction is the whole feature: the preference
	// was collected from the day checkout shipped and nothing ever acted on it.
	InvoiceDocuments []AdminInvoiceDocument
	// InvoicingEnabled is whether this deployment has 加值中心 credentials at
	// all. False renders no controls and says why, the way the payment page says
	// 金流尚未啟用 — never a button that can only fail.
	InvoicingEnabled bool
	Committed        bool
	Next             []AdminTransition
	// CanShip is whether a parcel can go out: the order is picking, or it has
	// already shipped one and something is still outstanding. Dispatch is its
	// own form because it carries the carrier and tracking number, and because
	// it settles stock — a status dropdown cannot express either.
	//
	// It used to be `status == "picking"` alone, and nothing returns an order TO
	// picking, so one order could hold exactly one parcel ever — against tables
	// that model several with per-line quantities.
	CanShip bool
	// Shippable is what is still outstanding, per line, with how many of each.
	// Empty on an order that has gone out in full.
	Shippable []AdminShippableLine
	Notice    string
	// Timeline is the order's history WITH the staff member who caused each
	// step. The customer's own page has had a timeline since checkout shipped
	// and the back office had none, which is backwards: the actor is the whole
	// reason these are two different queries.
	Timeline  []AdminOrderEvent
	Shipments []AdminShipment
	// The delivery details as fields rather than one line, so the back office
	// can CORRECT them. A customer who typed the wrong street had no way to fix
	// it and neither did the shop: the only option was cancel and re-order,
	// which loses the payment and the stock hold with it.
	Delivery AdminDelivery
	// Correctable is false once the parcel has left. Rewriting the address then
	// makes the record lie about where it went.
	Correctable bool
	// PickupDestination decides which half of the form is shown, from the
	// order's own shipping method rather than from anything submitted.
	PickupDestination bool
	PickupBrands      []PickupBrandChoice
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

	PickupBrand     string
	PickupStoreCode string
	PickupStoreName string
}

// AdminOrderEvent is one step in an order's history, as the shop sees it.
type AdminOrderEvent struct {
	Kind string
	Note string
	At   string
	// Actor is the staff member who caused it, or "" for something the system
	// did — a webhook capture, a customer's own cancellation.
	Actor string
}

// LabelKey names the step's message. It delegates to OrderEvent rather than
// repeating the switch: two copies of a nine-case mapping is how one of them
// comes to be missing the tenth.
func (e AdminOrderEvent) LabelKey() i18n.Key { return OrderEvent{Kind: e.Kind}.LabelKey() }

// By is who did it, in words. "系統" rather than an empty column, because a
// blank reads as missing data and this is a fact: nobody at the shop did it.
//
// A CANCELLATION with no actor is the customer's own, and says so. The back
// office always writes an actor when it cancels, so the absence is the
// distinction — it used to be a Chinese sentence stored in the note, which the
// customer's own order page then rendered at them whatever language they read.
// The fact is structural now, and each audience is told it in their own words.
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
	// Remaining is how many of this line have not gone out yet, which is what
	// the form's quantity box defaults to and is bounded by.
	Remaining int32
	// Held is how many the order still has reserved for it. Fewer than Remaining
	// means somebody released part of the hold, and the form says so rather than
	// letting the dispatch fail at the write.
	Held int32
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

// Short reports whether this line holds less stock than it still owes.
//
// It means part of the hold went back on the shelf — the sweeper, or a
// cancellation that did not finish — and the dispatch of that part would post no
// inventory movement. The form says it rather than letting the write refuse with
// a constraint name.
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

// HasInvoice reports whether the customer stated a 發票 preference.
func (v *AdminOrderView) HasInvoice() bool { return v.InvoiceType != "" }

// InvoiceText is the preference in words, with the detail that goes with it.
//
// A closed set — invoice_preferences_type_known has already refused anything else —
// so an unknown value is a programming error and panics rather than printing a code
// at a staff member who has to act on it.
func (v *AdminOrderView) InvoiceText(ctx context.Context) string {
	switch v.InvoiceType {
	case "member_carrier":
		return i18n.T(ctx, i18n.KeyAdminCarrierMember)
	case "mobile_carrier":
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminCarrierMobile), v.InvoiceCarrier)
	case "company":
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminCarrierTaxID), v.InvoiceTaxID)
	default:
		panic("pages: no label for invoice type " + v.InvoiceType)
	}
}

// AdminInvoiceDocument is one 統一發票 or 折讓 filed against an order.
type AdminInvoiceDocument struct {
	Kind   string
	Number string
	// ProviderRef is the four-digit 隨機碼, which a customer needs to look the
	// invoice up on the 財政部 platform and a void needs alongside the number.
	ProviderRef string
	AmountCents int64
	Status      string
	IssuedAt    string
	// Lines is what the document says was sold. A shop reconciling an invoice
	// against an order compares lines rather than totals, which is why
	// invoice_document_lines exists at all.
	Lines []AdminInvoiceLine
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

// CanIssueInvoice reports whether to offer the issue button.
//
// A COMMITTED order only: issuing for a checkout nobody paid for files a tax
// document for a sale that did not happen, and undoing that is a correction with
// the 財政部 rather than a delete. The database says the same thing — the store
// checks committed_orders before it calls the 加值中心 — and this only decides
// whether to show the control, because one that can only be refused is worse
// than none.
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
//
// The three admin metas are FUNCTIONS rather than package-level values, and the
// reason is the locale: a var is built once at startup, where there is no
// request and therefore no language to build it in. They were the last
// hard-coded titles in the back office for exactly that reason, and ListingMeta
// and ProductMeta already had this shape on the storefront.
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

// PriceText and CompareText are the prices in whole New Taiwan dollars, which
// is what the form's number inputs carry.
func (v AdminVariant) PriceText() string { return strconv.FormatInt(v.PriceCents/100, 10) }

// CompareText is the compare-at price, empty when the variant is not on sale.
func (v AdminVariant) CompareText() string {
	if v.CompareCents <= 0 {
		return ""
	}
	return strconv.FormatInt(v.CompareCents/100, 10)
}

// AdjustKey is the idempotency key this row's adjustment form carries.
//
// It is derived from the SKU and the stock the page was rendered with, so
// pressing the same button twice is ONE adjustment: inventory_movements has a
// unique index on the key, and the second write is refused rather than doubling
// the correction. Reloading the page produces a new key, because the stock it
// shows has changed.
func (v AdminVariant) AdjustKey() string {
	return "adj:" + v.SKU + ":" + strconv.FormatInt(int64(v.Stock), 10)
}

// AdminMovement is one row of a variant's stock ledger.
type AdminMovement struct {
	At     string
	Delta  int32
	Reason string
	// OrderNumber is the order a sale, hold or release belongs to, or "" for a
	// receipt or a hand adjustment — which belong to nothing but the person who
	// made them.
	OrderNumber string
	// Actor is the staff member, or "" for a movement the system made. A sale is
	// not somebody's decision; a hand adjustment is.
	Actor string
	// Running is the stock this movement left behind, summed over the ledger up to
	// and including it. Computed in SQL because the answer is a fact about the whole
	// ledger and only the last page of it is shown.
	Running int32
}

// DeltaText is the movement with its sign, because +3 and -3 are the whole story of
// a row and a bare 3 is half of it.
func (m AdminMovement) DeltaText() string {
	if m.Delta > 0 {
		return "+" + strconv.FormatInt(int64(m.Delta), 10)
	}
	return strconv.FormatInt(int64(m.Delta), 10)
}

// RunningText is the stock after this movement.
func (m AdminMovement) RunningText() string { return strconv.FormatInt(int64(m.Running), 10) }

// In reports whether stock came in, which is what decides the row's colour.
func (m AdminMovement) In() bool { return m.Delta > 0 }

// HasOrder reports whether this movement names an order.
func (m AdminMovement) HasOrder() bool { return m.OrderNumber != "" }

// By is who caused it, in words. "系統" rather than blank: a sale is the shop doing
// its work, not a missing value.
func (m AdminMovement) By(ctx context.Context) string {
	if m.Actor == "" {
		return i18n.T(ctx, i18n.KeyAdminActorSystem)
	}
	return m.Actor
}

// ReasonText is why, in the back office's language.
//
// A closed set, and a movement with an unknown reason is a programming error rather
// than a runtime condition — inventory_movements_reason_known has already refused
// anything else, so this panics instead of printing a code at somebody.
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

// ReceiveKey is the idempotency key the 進貨 form carries.
//
// Derived from the SKU and the stock the page was rendered with, exactly like
// AdjustKey and for the same reason: pressing the button twice is ONE receipt,
// because inventory_movements is unique on the key and the second write is
// refused rather than booking the delivery in twice. Prefixed differently from
// an adjustment so a correction and a delivery posted against the same figure
// are two distinct facts rather than one swallowed by the other.
func (v *AdminMovementsView) ReceiveKey() string {
	return "rcv:" + v.SKU + ":" + strconv.FormatInt(int64(v.Stock), 10)
}

// Empty reports whether nothing has ever moved. Possible: a variant is created with
// no stock at all, and record_inventory_movement is the only way any arrives.
func (v *AdminMovementsView) Empty() bool { return len(v.Rows) == 0 }

// StockText and SafetyText are the current figures.
func (v *AdminMovementsView) StockText() string { return strconv.FormatInt(int64(v.Stock), 10) }

// SafetyText is the floor below which nothing may be sold.
func (v *AdminMovementsView) SafetyText() string { return strconv.FormatInt(int64(v.Safety), 10) }
