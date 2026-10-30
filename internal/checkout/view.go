package checkout

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/shipping"
	"github.com/koopa0/goen/internal/ui/fieldrule"
	"github.com/koopa0/goen/internal/ui/frame"
)

// pickupChains are the chains a shopper chooses between: those whose store
// picker checkout integrates. The back office offers every chain
// (shipping.Chains), so an order placed at one checkout no longer takes stays
// correctable.
func pickupChains() []shipping.Chain {
	return slices.DeleteFunc(shipping.Chains(), func(c shipping.Chain) bool { return !c.AtCheckout() })
}

type View struct {
	Cart                cart.View
	Shipping            []cart.ShippingChoice
	Chosen              string
	QuotedShippingCents int64
	SurchargeCents      int64
	// Repriced is a quote the customer has not seen yet, not an Errors entry: nothing
	// they typed was wrong. Still a 422, so nobody is charged for an unconfirmed quote.
	Repriced string
	// Moved are the summary rows whose figure is not the one the form was showing when it was
	// refused for a changed quote. Empty means the notice has no row to name and Repriced speaks.
	Moved []MovedRow
	// CreditChanged is a notice like Repriced: the customer must see the new figure before submitting again.
	CreditChanged string
	ZoneName      string
	// Destination is decided by the server; no field carries it back.
	Destination    shipping.Destination
	Address        Address
	Errors         map[string]string
	Invoice        InvoiceView
	InvoiceChoices []InvoiceChoice
	PickupChains   []shipping.Chain
	SavedAddresses []SavedAddress
	ChosenAddress  string
	Profile        Profile
	RecipientMe    bool
	// What the recipient fields held just before the box was ticked, which
	// unticking puts back.
	RecipientPrevName    string
	RecipientPrevPhone   string
	CouponCode           string
	CouponApplied        string
	CouponDiscountCents  int64
	CouponFreeShipping   bool
	AvailableCreditCents int64
	// QuoteID is opaque to the page: this package creates it and the page carries it back unchanged.
	QuoteID        string
	IdempotencyKey string
	// MapOffered needs a configured carrier that serves the chosen chain. The picker's
	// own form is built only where it is opened, by PickupStart.
	MapOffered bool
	// PickupNonce decides whether the store was honoured: it travels to the carrier in
	// ExtraData and back in the URL, and is checked against a cookie only this browser holds.
	PickupNonce string
	// PickupStoreAddr is DISPLAY ONLY: read from the URL for the summary, never a
	// hidden field, never posted, never stored (the schema keeps a code and a name).
	PickupStoreAddr string
	// PickupRefused: a store arrived that this browser cannot vouch for; the page echoes none of it.
	PickupRefused bool
	// Partial is an answer htmx swaps into a page already showing. Nothing in it takes
	// focus: the control the shopper just changed has it.
	Partial bool
	// PostalNote is set on a refused form whose postcode covers none of the city and district
	// typed with it. It refuses nothing.
	PostalNote string
	// SignedIn is false for a guest, who is offered signing in.
	SignedIn bool
}

func (v *View) HasShipping() bool { return len(v.Shipping) > 0 }

// PickupStartAction is goen's own route, so what the shopper typed reaches only
// goen; the carrier's map form is built on the page it answers.
const PickupStartAction = "/checkout/pickup/start"

// PickupMapPath is a GET holding the carrier's form, so Back from the map lands on
// a page and not on a POST.
const PickupMapPath = "/checkout/pickup/map"

// storeButtonID is the store button's id, where a refused store is linked.
const storeButtonID = "pickup_store"

// formnovalidate: a shopper who has not typed an email yet must still be able to
// choose a store; nothing is placed by this submit. The button carries the store
// refusal because the store is chosen through the picker, not typed.
func checkoutStoreButtonAttrs(invalid string, refused bool) templ.Attributes {
	attrs := templ.Attributes{
		"id":         storeButtonID,
		"formaction": PickupStartAction, "formmethod": "post", "formnovalidate": true,
		"aria-invalid": invalid,
	}
	if refused {
		attrs["aria-describedby"] = "pickup_store-error"
	}
	return attrs
}

func (v *View) OffersTheStoreMap() bool {
	return v.ToPickupPoint() && v.MapOffered
}

// HasPickupStore needs both halves, because order_private_data refuses a row carrying one without the other.
func (v *View) HasPickupStore() bool {
	return v.Address.PickupStoreCode != "" && v.Address.PickupStoreName != ""
}

type checkoutFieldHint struct {
	Autocomplete      string
	InputMode         string
	AutoCapitalize    string
	SpellCheck        string
	Pattern           string
	ConstraintMessage i18n.Key
	Rule              *fieldrule.Rule
}

func (h checkoutFieldHint) Constrained() bool { return h.Pattern != "" || h.Rule != nil }

func (h checkoutFieldHint) ConstraintText(ctx context.Context) string {
	if h.Rule != nil {
		return i18n.T(ctx, h.Rule.Message)
	}
	return i18n.T(ctx, h.ConstraintMessage)
}

// Every helper-rendered control must make an explicit autofill decision.
//
// Do not add enterkeyhint: Enter in any checkout field places the order, so a
// "next" hint would label a key that charges the customer.
// The patterns accept what delivery.Details.Trim accepts (the server trims first) and repeat
// its bounds (maxPostalCodeRunes, maxCityRunes).
var checkoutFieldHints = map[string]checkoutFieldHint{
	"email":       {Autocomplete: "email", Rule: &fieldrule.Email},
	"name":        {Autocomplete: "name"},
	"phone":       {Autocomplete: "tel", Rule: &fieldrule.Phone},
	"postal_code": {Autocomplete: "postal-code", Rule: &fieldrule.PostalCode},
	"city":        {Autocomplete: "address-level1", Pattern: `\s*\S(?:.{0,18}\S)?\s*`, ConstraintMessage: i18n.KeyCheckoutRegionLength},
	"district":    {Autocomplete: "address-level2", Pattern: `\s*\S(?:.{0,18}\S)?\s*`, ConstraintMessage: i18n.KeyCheckoutRegionLength},
}

func checkoutHintsFor(name string) checkoutFieldHint { return checkoutFieldHints[name] }

// The request is the same POST as the 更新 button's, so the write face is unchanged
// and the button remains the path with scripting off.
// show:none keeps the swap where the customer is looking: they pressed a radio
// half way down the form.
func checkoutChoiceSwap(which string) templ.Attributes {
	return templ.Attributes{
		"hx-include": "#checkout-form",
		"hx-post":    "/checkout",
		"hx-select":  "#checkout-region",
		"hx-swap":    "outerHTML show:none",
		"hx-target":  "#checkout-region",
		"hx-trigger": "change",
		"hx-vals":    `{"update":"` + which + `"}`,
	}
}

// With scripting, 套用 swaps only the code's field, the totals and the quote; without
// it the same submit comes back at the coupon field.
// The hidden quote and the figures beside it are swapped too: a stale quote would make
// the next 送出訂單 answer "the checkout changed", and stale figures would name rows
// that never moved. The moved-rows notice goes with them so it cannot outlive its figures.
func couponButtonAttrs(ctx context.Context) templ.Attributes {
	return templ.Attributes{
		"name":              "update",
		"value":             "coupon",
		"formaction":        "/checkout#coupon-field",
		"hx-post":           "/checkout",
		"hx-include":        "#checkout-form",
		"hx-vals":           `{"update":"coupon"}`,
		"hx-swap":           "none",
		"hx-select-oob":     "#coupon,#coupon-message:innerHTML,#summary-totals,#summary-moved,#checkout-quote,#checkout-shown",
		"data-coupon-apply": true,
		"data-busy":         i18n.T(ctx, i18n.KeyTooManyRequests),
		"data-failed":       i18n.T(ctx, i18n.KeyCouponUnavailable),
	}
}

type InvoiceChoice struct {
	Value invoice.Preference
	Label string
}

type Address struct {
	Email      string
	Name       string
	Phone      string
	PostalCode string
	City       string
	District   string
	Street     string

	PickupChain     shipping.Chain
	PickupStoreCode string
	PickupStoreName string

	Note string
}

func (v *View) ToPickupPoint() bool { return v.Destination == shipping.DestinationPickupPoint }

type SavedAddress struct {
	ID         string
	Label      string
	Name       string
	Phone      string
	PostalCode string
	City       string
	District   string
	Street     string
	Default    bool
}

func (a SavedAddress) Line() string {
	return strings.TrimSpace(a.PostalCode + " " + a.City + a.District + a.Street)
}

func (a SavedAddress) DisplayLabel(ctx context.Context) string {
	if a.Label == "" {
		return i18n.T(ctx, i18n.KeyDeliveryToAddress)
	}
	return a.Label
}

const OtherAddress = "new"

type Profile struct {
	Email string
	Name  string
	Phone string
}

func (v *View) OffersTheProfile() bool {
	return v.Profile.Email != "" && (v.Profile.Name != "" || v.Profile.Phone != "")
}

func (v *View) OffersTheAddressBook() bool {
	return !v.ToPickupPoint() && len(v.SavedAddresses) > 0
}

func Meta(ctx context.Context) frame.Page {
	return frame.Page{Title: i18n.T(ctx, i18n.KeyCheckoutTitle)}
}

func (v *View) Err(field string) string { return v.Errors[field] }

func (v *View) HasErr(field string) bool { return v.Errors[field] != "" }

// NoteFor is the note under a field that is not a refusal: only the postcode has one.
func (v *View) NoteFor(field string) string {
	if field == "postal_code" {
		return v.PostalNote
	}
	return ""
}

// describedBy names what a screen reader reads with a field: the sentence under it, then its note.
func describedBy(field string, sentence, note bool) string {
	var ids []string
	if sentence {
		ids = append(ids, field+"-error")
	}
	if note {
		ids = append(ids, field+"-note")
	}
	return strings.Join(ids, " ")
}

func (v *View) Invalid(field string) string {
	if v.HasErr(field) {
		return "true"
	}
	return "false"
}

// RefusedField is a link in the refusal summary: where the field is and what it is called.
type RefusedField struct{ ID, Label string }

// refusedFields are the controls a refused checkout can name, in the order the page shows
// them, with the id a link to each goes to. A group of radios has no control of its own and
// is reached through the card holding it; the pickup store's own control is the store button,
// and the two keys typed into no control, which only a forged post reaches, go to the chooser.
// The discount code is not here: a code refused is reported under its field alone.
var refusedFields = []struct {
	key, id string
	label   i18n.Key
}{
	{"shipping", "shipping", i18n.KeySectionShipping},
	{"address", "address-book", i18n.KeyChooseSavedAddress},
	{"email", "email", i18n.KeyFieldEmail},
	{"name", "name", i18n.KeyFieldName},
	{"phone", "phone", i18n.KeyFieldPhone},
	{"pickup_chain", "pickup_chain", i18n.KeyFieldPickupChain},
	{"pickup_store", storeButtonID, i18n.KeyPickupChooseStore},
	{"pickup_store_code", "pickup_chain", i18n.KeyFieldStoreCode},
	{"pickup_store_name", "pickup_chain", i18n.KeyFieldStoreName},
	{"postal_code", "postal_code", i18n.KeyFieldPostalCode},
	{"city", "city", i18n.KeyFieldCity},
	{"district", "district", i18n.KeyFieldDistrict},
	{"street", "street", i18n.KeyFieldStreet},
	{"note", "note", i18n.KeyFieldNote},
	{"invoice_type", "invoice", i18n.KeySectionInvoice},
	{"invoice_carrier", "invoice_carrier", i18n.KeyFieldMobileBarcode},
	{"invoice_donation_code", "invoice_donation_code", i18n.KeyFieldDonationCode},
	{"invoice_company_name", "invoice_company_name", i18n.KeyFieldCompanyName},
	{"invoice_tax_id", "invoice_tax_id", i18n.KeyFieldTaxID},
}

// RefusedFields is what the refusal summary links to, one entry per refused field. The store
// button exists only where the carrier's map is offered; elsewhere a refused store is linked to
// the chooser, so no link goes to an id the page lacks.
func (v *View) RefusedFields(ctx context.Context) []RefusedField {
	var out []RefusedField
	for _, f := range refusedFields {
		if !v.HasErr(f.key) {
			continue
		}
		id := f.id
		if id == storeButtonID && !v.OffersTheStoreMap() {
			id = "pickup_chain"
		}
		out = append(out, RefusedField{ID: id, Label: i18n.T(ctx, f.label)})
	}
	return out
}

// OnlyCouponRefused says the code is the form's only refusal, so there is no summary to
// take focus and the code's own field does.
func (v *View) OnlyCouponRefused() bool {
	return len(v.Errors) == 1 && v.HasErr("coupon")
}

func (v *View) HasSurcharge() bool { return v.SurchargeCents > 0 }

func (v *View) ShippingFeeCents() int64 {
	if v.QuotedShippingCents > 0 {
		return v.QuotedShippingCents
	}
	for i := range v.Shipping {
		if v.Shipping[i].VersionID == v.Chosen {
			return v.Shipping[i].FeeCents
		}
	}
	if len(v.Shipping) > 0 {
		return v.Shipping[0].FeeCents
	}
	return 0
}

func (v *View) BaseShippingCents() int64 {
	return v.ShippingFeeCents() - v.SurchargeCents
}

func (v *View) ShipsFree() bool {
	return v.CouponFreeShipping || v.BaseShippingCents() == 0
}

// ShippingRow is the one line that says what delivery costs: free or the base
// rate, then the zone's surcharge, which a free-delivery code does not waive.
func (v *View) ShippingRow(ctx context.Context) string {
	text := twd(v.BaseShippingCents())
	if v.ShipsFree() {
		text = i18n.T(ctx, i18n.KeyFreeShipping)
	}
	return text + v.zoneExtra(ctx)
}

// ChoiceFee is a method's price on its card. The chosen method is the one that
// was quoted for the address, so only its card adds the surcharge the summary states.
func (v *View) ChoiceFee(ctx context.Context, c cart.ShippingChoice) string {
	text := c.Fee()
	if c.Free {
		text = i18n.T(ctx, i18n.KeyFreeShipping)
	}
	if c.VersionID == v.Chosen {
		text += v.zoneExtra(ctx)
	}
	return text
}

func (v *View) zoneExtra(ctx context.Context) string {
	if !v.HasSurcharge() {
		return ""
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyShippingZoneExtra), v.ZoneName, twd(v.SurchargeCents))
}

// ChargedShippingCents is what PlaceOrder stores: a free-shipping coupon removes
// the base rate but deliberately not a remote-zone surcharge.
func (v *View) ChargedShippingCents() int64 {
	if v.CouponFreeShipping {
		return v.SurchargeCents
	}
	return v.ShippingFeeCents()
}

func (v *View) Total() string {
	return twd(v.TotalCents())
}

func (v *View) GrossCents() int64 {
	return v.Cart.SubtotalCents + v.ChargedShippingCents() - v.CouponDiscountCents
}

// CreditCents is the credit the displayed quote applies: a rise in balance after
// rendering never spends more; a fall that cannot fund it makes placement refresh the quote.
func (v *View) CreditCents() int64 {
	return min(v.AvailableCreditCents, v.GrossCents())
}

func (v *View) UsesCredit() bool { return v.CreditCents() > 0 }

func (v *View) Credit() string { return "-" + twd(v.CreditCents()) }

// CreditNote gives the balance, what this order takes of it and what is left,
// because the summary's negative figure alone does not say how much remains.
func (v *View) CreditNote(ctx context.Context) string {
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyCreditRemaining),
		twd(v.AvailableCreditCents), twd(v.CreditCents()), twd(v.AvailableCreditCents-v.CreditCents()))
}

func (v *View) TotalCents() int64 { return v.GrossCents() - v.CreditCents() }

func (v *View) PlaceOrderKey() i18n.Key {
	if v.TotalCents() > 0 {
		return i18n.KeyPlaceOrderAndPay
	}
	return i18n.KeyPlaceOrder
}

func (v *View) PlacementNote(ctx context.Context) string {
	if v.TotalCents() > 0 {
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyCheckoutSubmitNote), strconv.FormatInt(v.Cart.PayStartMinutes, 10))
	}
	if v.UsesCredit() {
		return i18n.T(ctx, i18n.KeyCheckoutCreditSubmitNote)
	}
	return i18n.T(ctx, i18n.KeyCheckoutZeroSubmitNote)
}

type InvoiceView struct {
	Type          invoice.Preference
	MobileBarcode string
	DonationCode  string
	CompanyName   string
	TaxID         string
}

func (i InvoiceView) Is(t invoice.Preference) bool { return i.Chosen() == t }

func (i InvoiceView) Chosen() invoice.Preference {
	if i.Type == "" {
		return invoice.PreferenceMember
	}
	return i.Type
}

func (v *View) HasCoupon() bool {
	return v.CouponApplied != "" && (v.CouponDiscountCents > 0 || v.CouponFreeShipping)
}

func (v *View) CouponDiscount() string { return "-" + twd(v.CouponDiscountCents) }

// summaryRow is a row of the totals that the notice for a changed quote can name and mark.
type summaryRow uint8

const (
	rowSubtotal summaryRow = iota + 1
	rowShipping
	rowDiscount
	rowCredit
	rowTotal
)

// shownRows are the figures the form carries back, in the order the summary lists them.
var shownRows = [...]struct {
	row   summaryRow
	field string
	label i18n.Key
}{
	{rowSubtotal, "shown_subtotal", i18n.KeySubtotal},
	{rowShipping, "shown_shipping", i18n.KeyShippingFee},
	{rowDiscount, "shown_discount", i18n.KeyDiscount},
	{rowCredit, "shown_credit", i18n.KeyStoreCredit},
	{rowTotal, "shown_total", i18n.KeyTotal},
}

// figure is what the summary now states for a row, as a size and not a sign.
func (v *View) figure(row summaryRow) int64 {
	switch row {
	case rowSubtotal:
		return v.Cart.SubtotalCents
	case rowShipping:
		return v.ChargedShippingCents()
	case rowDiscount:
		return v.CouponDiscountCents
	case rowCredit:
		return v.CreditCents()
	default:
		return v.TotalCents()
	}
}

// ShownField is one of the figures the page was showing, posted back so a refusal can
// say which of them moved. They are not read for anything else: the quote ID alone decides
// whether a submit is accepted, so a field edited by hand changes only the notice's words.
type ShownField struct {
	Name  string
	Value string
}

func (v *View) ShownFields() []ShownField {
	out := make([]ShownField, len(shownRows))
	for i, r := range shownRows {
		out[i] = ShownField{Name: r.field, Value: strconv.FormatInt(v.figure(r.row), 10)}
	}
	return out
}

// MovedRow is a summary row whose figure is not the one the shopper was shown.
type MovedRow struct {
	row  summaryRow
	Text string
}

func (v *View) HasMoved() bool { return len(v.Moved) > 0 }

func (v *View) RowMoved(row summaryRow) bool {
	return slices.ContainsFunc(v.Moved, func(m MovedRow) bool { return m.row == row })
}

// shownFrom reads the figures a posted form was showing. ok is false when one is missing
// or is not an amount, and the refusal then falls back to its general sentence.
func shownFrom(r *http.Request) (figures [len(shownRows)]int64, ok bool) {
	for i, f := range shownRows {
		n, err := strconv.ParseInt(r.PostFormValue(f.field), 10, 64)
		if err != nil || n < 0 {
			return figures, false
		}
		figures[i] = n
	}
	return figures, true
}

// noteQuoteChanged words a refusal for a changed quote. Each row whose figure differs from
// the one the form showed is named with both figures. When none does (a new rate version at
// the same price changes the quote without moving an amount), or the form did not say what it
// showed, or another notice already stands, or no method can deliver the order (its figures
// would read as free delivery and a lower total), the general sentence speaks instead.
func (v *View) noteQuoteChanged(ctx context.Context, r *http.Request) {
	v.Moved = nil
	if shown, ok := shownFrom(r); ok && v.Repriced == "" && v.HasShipping() {
		for i, f := range shownRows {
			now := v.figure(f.row)
			if now == shown[i] {
				continue
			}
			v.Moved = append(v.Moved, MovedRow{row: f.row, Text: fmt.Sprintf(
				i18n.T(ctx, i18n.KeyCheckoutMovedRow), i18n.T(ctx, f.label), twd(shown[i]), twd(now))})
		}
	}
	if len(v.Moved) == 0 {
		v.Repriced = i18n.T(ctx, i18n.KeyCheckoutChanged)
	}
}
