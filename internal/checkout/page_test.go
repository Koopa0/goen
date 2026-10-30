package checkout

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/shipping"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
)

// TestPageChoicesInsideForm: Page renders the choosers as radio groups inside
// the checkout form, so changing one keeps what was typed into the others and
// still works with scripting off.
func TestPageChoicesInsideForm(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	typed := View{
		Cart:     cart.View{Lines: []cart.Line{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping: []cart.ShippingChoice{{VersionID: "ship-1", Code: "home", Name: "宅配到府"}},
		Chosen:   "ship-1",
		Address: Address{
			Name: "王小明", Phone: "0912345678", Street: "松高路 99 號", Note: "放管理室",
		},
		CouponCode:     "SAVE10",
		Invoice:        InvoiceView{Type: "mobile_carrier", MobileBarcode: "/ABC+123"},
		InvoiceChoices: []InvoiceChoice{{Value: "mobile_carrier", Label: "手機條碼載具"}},
	}
	markup := renderToString(t, Page(Meta(ctx), &typed))

	// Everything typed is still in the document, so a re-render returns it.
	for _, want := range []string{"王小明", "0912345678", "松高路 99 號", "放管理室", "SAVE10", "/ABC+123"} {
		if !strings.Contains(markup, want) {
			t.Errorf("the checkout does not carry %q back", want)
		}
	}

	// And the choosers are inside the form rather than links beside it.
	for _, want := range []string{
		`type="radio" name="shipping"`,
		`type="radio" name="invoice_type"`,
		// Each 更新 names the chooser it applies, so the handler can tell which
		// one was pressed.
		`name="update" value="shipping"`,
		`name="update" value="invoice"`,
	} {
		if !strings.Contains(markup, want) {
			t.Errorf("the checkout does not carry %s — a chooser outside the form "+
				"cannot preserve what has been typed into it", want)
		}
	}
	if strings.Contains(markup, "/checkout?ship=") || strings.Contains(markup, "/checkout?invoice=") {
		t.Error("a chooser is still a link carrying only its own parameter")
	}
}

// TestPageChoiceAppliesOnChange: Page makes changing a chooser's radio send
// what its 更新 button sends and replace the form in place, with a focusable id
// on each radio. It holds the scripting-on half of the chooser: changing a
// radio sends exactly what the 更新 button beside it sends — everything typed,
// plus the name of the chooser — and the answer replaces the form where it
// stands. Without it the dot moves and the fields that choice decides are not
// asked for until a button the page never mentioned is pressed.
func TestPageChoiceAppliesOnChange(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	view := View{
		Cart:           cart.View{Lines: []cart.Line{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping:       []cart.ShippingChoice{{VersionID: "ship-1", Code: "home", Name: "宅配到府"}},
		Chosen:         "ship-1",
		SavedAddresses: []SavedAddress{{ID: "addr-1", Label: "家"}},
		InvoiceChoices: []InvoiceChoice{{Value: "mobile_carrier", Label: "手機條碼載具"}},
	}
	markup := renderToString(t, Page(Meta(ctx), &view))

	if !strings.Contains(markup, `id="checkout-form"`) {
		t.Fatal("the checkout form has no id, so a swap has nothing to select or to replace")
	}
	for _, which := range []string{"shipping", "address", "invoice"} {
		group := tagCarrying(t, markup, `hx-vals="{&#34;update&#34;:&#34;`+which+`&#34;}"`)
		for _, want := range []string{
			`hx-post="/checkout"`,
			`hx-trigger="change"`,
			`hx-include="#checkout-form"`,
			`hx-target="#checkout-region"`,
			`hx-select="#checkout-region"`,
			"show:none",
		} {
			if !strings.Contains(group, want) {
				t.Errorf("the %s chooser does not carry %s:\n%s", which, want, group)
			}
		}
		// The same request the button makes, or the two ways of applying one
		// choice have drifted apart and only one of them is tested.
		if !strings.Contains(markup, `name="update" value="`+which+`"`) {
			t.Errorf("a chooser sends update=%s, which no button sends", which)
		}
	}
	// A swap restores the focus to the element whose id it finds again, so each
	// radio carries one.
	for _, want := range []string{
		`id="shipping-ship-1"`,
		`id="address-book"`,
		`id="invoice_type-mobile_carrier"`,
	} {
		if !strings.Contains(markup, want) {
			t.Errorf("no radio carries %s, so the swap answers with the focus lost", want)
		}
	}
}

// TestPageApplyButtonSection: Each 更新 button's formaction on Page names its own
// section, and that section exists, so a scripting-off answer opens where the
// customer pressed. It holds the scripting-off half. The answer is the whole
// form again, and a form that opens at its top has taken the customer away from
// the control they just pressed.
func TestPageApplyButtonSection(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	view := View{
		Cart:           cart.View{Lines: []cart.Line{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping:       []cart.ShippingChoice{{VersionID: "ship-1", Code: "home", Name: "宅配到府"}},
		Chosen:         "ship-1",
		SavedAddresses: []SavedAddress{{ID: "addr-1", Label: "家"}},
		InvoiceChoices: []InvoiceChoice{{Value: "mobile_carrier", Label: "手機條碼載具"}},
	}
	markup := renderToString(t, Page(Meta(ctx), &view))

	for _, which := range []string{"shipping", "address", "invoice"} {
		if !strings.Contains(markup, `formaction="/checkout#`+which+`"`) {
			t.Errorf("the %s 更新 button does not name its own section, so its answer "+
				"opens at the top of the form", which)
		}
		// The fragment names a section that exists; #376 is what a fragment
		// aimed at nothing costs.
		if !strings.Contains(markup, `id="`+which+`"`) {
			t.Errorf("no section carries id=%q for the fragment to land on", which)
		}
	}
}

// TestPagePickupAsksForChain: Page asks for a convenience-store order's chain
// as radio cards and asks for no store number or name. It holds what 超商取貨 asks
// for: the chain, as radio cards. Nobody types a store number — shoppers pick a
// store from the carrier's map — and until that map is integrated the field
// could only produce refusals.
func TestPagePickupAsksForChain(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	view := View{
		Cart:         cart.View{Lines: []cart.Line{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping:     []cart.ShippingChoice{{VersionID: "ship-1", Code: "pickup", Name: "超商取貨"}},
		Chosen:       "ship-1",
		Destination:  "pickup_point",
		PickupChains: pickupChains(),
	}
	markup := renderToString(t, Page(Meta(ctx), &view))

	for _, want := range []string{
		`type="radio" name="pickup_chain" value="seven_eleven"`,
		`type="radio" name="pickup_chain" value="family_mart"`,
		"7-ELEVEN",
		"全家",
		// The selected look is the checked radio's, on the same card the
		// shipping and invoice choosers use.
		`<label class="goen-checkout__ship">`,
	} {
		if !strings.Contains(markup, want) {
			t.Errorf("the pickup form does not offer the chain as a radio card: %s", want)
		}
	}
	for _, gone := range []string{`name="pickup_store_code"`, `name="pickup_store_name"`, "<select"} {
		if strings.Contains(markup, gone) {
			t.Errorf("the pickup form still asks for %s", gone)
		}
	}
}

// tagCarrying is the opening tag of the element that carries needle.
func tagCarrying(t *testing.T, markup, needle string) string {
	t.Helper()

	at := strings.Index(markup, needle)
	if at < 0 {
		t.Fatalf("the rendered checkout carries no %s", needle)
	}
	start := strings.LastIndex(markup[:at], "<")
	end := strings.Index(markup[at:], ">")
	if start < 0 || end < 0 {
		t.Fatalf("%s is not inside a tag", needle)
	}
	return markup[start : at+end+1]
}

// TestView_OffersTheAddressBook: OffersTheAddressBook offers saved street
// addresses for an address order with a book, and not for a pickup order or an
// empty book. It holds that a saved street address is not offered for a
// convenience-store pickup, whose form has no such fields.
func TestView_OffersTheAddressBook(t *testing.T) {
	t.Parallel()

	book := []SavedAddress{{ID: "a"}}
	for _, tt := range []struct {
		name        string
		destination shipping.Destination
		book        []SavedAddress
		want        bool
	}{
		{"an address order with a book", "address", book, true},
		{"a pickup order with a book", "pickup_point", book, false},
		{"an address order with no book", "address", nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v := &View{Destination: tt.destination, SavedAddresses: tt.book}
			if got := v.OffersTheAddressBook(); got != tt.want {
				t.Errorf("OffersTheAddressBook() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestView_ShippingFeeCentsRequoted: ShippingFeeCents shows a re-quoted fee
// instead of the mainland estimate the method chooser shows before an address
// is typed.
func TestView_ShippingFeeCentsRequoted(t *testing.T) {
	t.Parallel()

	choices := []cart.ShippingChoice{{VersionID: "v1", FeeCents: 8000}}

	before := &View{Shipping: choices, Chosen: "v1"}
	if got := before.ShippingFeeCents(); got != 8000 {
		t.Errorf("before an address is typed the estimate is %d, want 8000", got)
	}

	after := &View{Shipping: choices, Chosen: "v1", QuotedShippingCents: 28000}
	if got := after.ShippingFeeCents(); got != 28000 {
		t.Errorf("after re-quoting the summary shows %d, want 28000 — it is still "+
			"showing the mainland estimate", got)
	}
}

// TestView_TotalCentsSurcharge: TotalCents adds up the way PlaceOrder prices: a
// free-delivery coupon pays the base rate and never the outlying-island
// surcharge, which the fee row states on its own line.
func TestView_TotalCentsSurcharge(t *testing.T) {
	t.Parallel()

	base := func() *View {
		return &View{
			Cart:                cart.View{SubtotalCents: 100000},
			Shipping:            []cart.ShippingChoice{{VersionID: "v1", FeeCents: 8000}},
			Chosen:              "v1",
			QuotedShippingCents: 28000,
			SurchargeCents:      20000,
		}
	}

	t.Run("the two summary rows do not double-count the surcharge", func(t *testing.T) {
		t.Parallel()
		v := base()
		if got := v.BaseShippingCents(); got != 8000 {
			t.Errorf("the 運費 row shows %d, want 8000 — the 離島加價 row states the "+
				"other 20000 on its own line, so printing the combined figure here "+
				"reads as 28000 + 20000", got)
		}
		if got := v.TotalCents(); got != 128000 {
			t.Errorf("total = %d, want 128000 (100000 + 8000 + 20000)", got)
		}
	})

	t.Run("a 免運 coupon pays the base rate and not the crossing", func(t *testing.T) {
		t.Parallel()
		v := base()
		v.CouponFreeShipping = true
		if got := v.TotalCents(); got != 120000 {
			t.Errorf("total with a free-shipping coupon = %d, want 120000 "+
				"(100000 + 0 base + 20000 surcharge) — zeroing the surcharge too "+
				"makes the page promise a figure PlaceOrder will not write, and the "+
				"customer meets the difference on the payment page", got)
		}
		if !v.ShipsFree() {
			t.Error("the fee row does not read 免運 with a free-shipping coupon")
		}
	})

	t.Run("no surcharge, no coupon", func(t *testing.T) {
		t.Parallel()
		v := &View{
			Cart:     cart.View{SubtotalCents: 100000},
			Shipping: []cart.ShippingChoice{{VersionID: "v1", FeeCents: 8000}},
			Chosen:   "v1",
		}
		if got := v.TotalCents(); got != 108000 {
			t.Errorf("total = %d, want 108000", got)
		}
	})
}

// TestPageSummaryVariant: Page names the variant of a line in the order
// summary, the one screen built to confirm what is about to be paid for.
func TestPageSummaryVariant(t *testing.T) {
	t.Parallel()

	view := View{
		Cart: cart.View{Lines: []cart.Line{{
			Name: "Pixelight 9 Pro", Label: "星霧藍 / 512GB",
			Quantity: 1, UnitCents: 3190000,
		}}},
		Shipping: []cart.ShippingChoice{{Code: "home", Name: "宅配到府", FeeCents: 8000}},
		Chosen:   "home",
	}
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	markup := renderToString(t, Page(Meta(ctx), &view))

	// The summary specifically, not the page: the line above the fold carries
	// the label too.
	_, summary, ok := strings.Cut(markup, "goen-checkout__lines")
	if !ok {
		t.Fatal("the checkout rendered no summary list")
	}
	summary, _, _ = strings.Cut(summary, "</ul>")
	if !strings.Contains(summary, "星霧藍 / 512GB") {
		t.Errorf("the checkout summary does not name the variant:\n%s", summary)
	}
}

// TestPageInvoiceFields: Page renders only the invoice fields the chosen
// preference needs (mobile barcode, donation code, company name and tax ID),
// and keeps the three choosers in one form. It holds which half of the form
// exists. 會員載具, 手機條碼載具, 捐贈 and 公司統編 need different information, and Validate()
// blanks the field that does not apply, so rendering both would leave the form
// promising something the server will not demand.
func TestPageInvoiceFields(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	tests := []struct {
		name            string
		kind            invoice.Preference
		wantBarcode     bool
		wantDonation    bool
		wantCompanyName bool
		wantTaxID       bool
	}{
		{name: "the default keeps neither", kind: "", wantBarcode: false, wantTaxID: false},
		{name: "a mobile barcode needs its field", kind: "mobile_carrier", wantBarcode: true},
		{name: "a donation needs the donation code and nothing else", kind: "donation", wantDonation: true},
		{name: "a company invoice needs its registered buyer", kind: "company", wantCompanyName: true, wantTaxID: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			view := View{
				Cart:           cart.View{Lines: []cart.Line{{Name: "x", Quantity: 1, UnitCents: 100}}},
				Shipping:       []cart.ShippingChoice{{Code: "home", Name: "宅配到府"}},
				Invoice:        InvoiceView{Type: tt.kind},
				InvoiceChoices: []InvoiceChoice{{Value: "member_carrier", Label: "會員載具"}},
			}
			markup := renderToString(t, Page(Meta(ctx), &view))
			if got := strings.Contains(markup, `id="invoice_carrier"`); got != tt.wantBarcode {
				t.Errorf("invoice=%q renders the mobile barcode field = %v, want %v", tt.kind, got, tt.wantBarcode)
			}
			if got := strings.Contains(markup, `id="invoice_donation_code"`); got != tt.wantDonation {
				t.Errorf("invoice=%q renders the donation code field = %v, want %v", tt.kind, got, tt.wantDonation)
			}
			if got := strings.Contains(markup, `id="invoice_tax_id"`); got != tt.wantTaxID {
				t.Errorf("invoice=%q renders the 統編 field = %v, want %v", tt.kind, got, tt.wantTaxID)
			}
			if got := strings.Contains(markup, `id="invoice_company_name"`); got != tt.wantCompanyName {
				t.Errorf("invoice=%q renders the company-name field = %v, want %v",
					tt.kind, got, tt.wantCompanyName)
			}
		})
	}

	// The three choosers compose because they are three radio groups in ONE
	// form, so a submission carries all of them.
	full := View{
		Cart:           cart.View{Lines: []cart.Line{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping:       []cart.ShippingChoice{{VersionID: "ship-1", Code: "home", Name: "宅配到府"}},
		Chosen:         "ship-1",
		Invoice:        InvoiceView{Type: "company"},
		InvoiceChoices: []InvoiceChoice{{Value: "company", Label: "公司統編"}},
	}
	markup := renderToString(t, Page(Meta(ctx), &full))
	for _, want := range []string{
		`type="radio" name="shipping"`,
		`type="radio" name="invoice_type"`,
		// Each 更新 names the chooser it applies, so the handler can tell which
		// one was pressed.
		`name="update" value="shipping"`,
		`name="update" value="invoice"`,
	} {
		if !strings.Contains(markup, want) {
			t.Errorf("the checkout does not carry %s — a chooser outside the form "+
				"cannot preserve what has been typed into it", want)
		}
	}
}

// TestPageRepricedNotice: Page shows a re-quote as a status notice, not as an
// error the customer made. It holds a re-quote as a notice rather than a field
// error — the method is chosen above the address, so the fee on screen is a
// mainland estimate and nothing the customer typed was wrong — while still
// answering 422, because the submission was not accepted.
func TestPageRepricedNotice(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	view := View{
		Cart:     cart.View{Lines: []cart.Line{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping: []cart.ShippingChoice{{Code: "home", Name: "宅配到府"}},
		Repriced: "離島運費另計,已更新為 NT$280。確認後再送出一次。",
	}
	markup := renderToString(t, Page(Meta(ctx), &view))

	if !strings.Contains(markup, view.Repriced) {
		t.Fatal("the re-quote is not shown at all")
	}
	// The element carrying it, not merely the words: the string appears the
	// same either way.
	_, after, ok := strings.Cut(markup, view.Repriced)
	if !ok {
		t.Fatal("could not locate the message")
	}
	before := markup[:len(markup)-len(after)-len(view.Repriced)]
	i := strings.LastIndex(before, "<p ")
	if i < 0 {
		t.Fatal("the message is not in a paragraph")
	}
	tag := before[i:]
	if strings.Contains(tag, "ui-error-text") || strings.Contains(tag, "ui-alert--error") {
		t.Errorf("a re-quote is announced as a mistake the customer made: %s", tag)
	}
	if !strings.Contains(tag, `role="status"`) {
		t.Errorf("the re-quote is not announced at all: %s", tag)
	}
}

// TestPageCreditChangedNotice: Page shows a changed store-credit balance as an
// informational status notice of its own, not through the shipping Repriced
// field.
func TestPageCreditChangedNotice(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	//nolint:gosec // G101: a customer-facing balance-change notice, not a credential
	view := View{
		Cart:          cart.View{Lines: []cart.Line{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping:      []cart.ShippingChoice{{Code: "home", Name: "宅配到府"}},
		CreditChanged: "可用購物金已變更為 NT$10。請確認後再送出一次。",
	}
	markup := renderToString(t, Page(Meta(ctx), &view))

	if !strings.Contains(markup, view.CreditChanged) {
		t.Fatal("the refreshed credit balance is not shown")
	}
	_, after, ok := strings.Cut(markup, view.CreditChanged)
	if !ok {
		t.Fatal("could not locate the refreshed credit message")
	}
	before := markup[:len(markup)-len(after)-len(view.CreditChanged)]
	i := strings.LastIndex(before, "<p ")
	if i < 0 {
		t.Fatal("the refreshed credit message is not in a paragraph")
	}
	tag := before[i:]
	if !strings.Contains(tag, "ui-alert--info") || !strings.Contains(tag, `role="status"`) {
		t.Errorf("the refreshed balance is not an informational status notice: %s", tag)
	}
	if view.Repriced != "" {
		t.Error("the credit notice reused the shipping Repriced field")
	}
}

// TestPageEnterPlacesOrder: Page puts an inert order button first in the form,
// so Enter in any text field places the order rather than a chooser's 更新
// re-rendering the form. It holds a rule the browser applies and no linter can
// see: HTML makes the FIRST submit button in tree order the one Enter activates
// in any text field, so a leading submit must place the order rather than a
// chooser's 更新 re-rendering the form. Asserting ORDER is the whole point: both
// buttons exist either way.
func TestPageEnterPlacesOrder(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	view := View{
		Cart:           cart.View{Lines: []cart.Line{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping:       []cart.ShippingChoice{{VersionID: "s1", Code: "home", Name: "宅配到府"}},
		Chosen:         "s1",
		InvoiceChoices: []InvoiceChoice{{Value: "member_carrier", Label: "會員載具"}},
	}
	markup := renderToString(t, Page(Meta(ctx), &view))

	_, form, ok := strings.Cut(markup, `action="/checkout"`)
	if !ok {
		t.Fatal("the checkout rendered no form")
	}
	form, _, _ = strings.Cut(form, "</form>")

	place := strings.Index(form, i18n.T(ctx, i18n.KeyPlaceOrder))
	update := strings.Index(form, i18n.T(ctx, i18n.KeyApplyChoice))
	if place < 0 || update < 0 {
		t.Fatalf("the form is missing a button: place=%d update=%d", place, update)
	}
	if place > update {
		t.Error("a chooser's 更新 button comes before the order button, so Enter in " +
			"any field re-renders the form instead of placing the order")
	}

	// Inert, or the page has two order buttons for anyone using a screen reader.
	lead := form[:place+40]
	if !strings.Contains(lead, `tabindex="-1"`) || !strings.Contains(lead, `aria-hidden="true"`) {
		t.Error("the leading submit is reachable by keyboard or announced, so it is a " +
			"duplicate control rather than a default")
	}
}

// TestPagePlacementNote: Page labels the order button and explains what placing
// the order does for an amount owed, a part-credited order, a credit-covered
// order and a zero-by-discount order, in each locale.
func TestPagePlacementNote(t *testing.T) {
	t.Parallel()
	for _, locale := range []struct {
		locale                                i18n.Locale
		pay, place, paying, coveredNote, zero string
	}{
		{locale: i18n.ZhHant, pay: "送出訂單並付款", place: "送出訂單", paying: "送出後會為你保留商品，請在 28 分鐘內開始付款。", coveredNote: "購物金已全額折抵，送出後訂單就完成付款。", zero: "這筆訂單不需付款，送出後就完成。"},
		{locale: i18n.En, pay: "Place order and pay", place: "Place order", paying: "Placing the order holds the goods; start paying within 28 minutes.", coveredNote: "Store credit covers this order; placing it completes payment.", zero: "No payment is needed; placing the order completes it."},
	} {
		for _, tt := range []struct {
			name             string
			credit, discount int64
			label, note      string
		}{
			{name: "amount owed", label: locale.pay, note: locale.paying},
			{name: "partially credited", credit: 1000, label: locale.pay, note: locale.paying},
			{name: "covered by credit", credit: 10000, label: locale.place, note: locale.coveredNote},
			{name: "zero by discount with unused credit", credit: 10000, discount: 10000, label: locale.place, note: locale.zero},
		} {
			t.Run(locale.locale.Tag()+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale.locale)
				view := View{
					Cart: cart.View{
						Lines: []cart.Line{{Name: "Item", Quantity: 1, UnitCents: 10000}}, SubtotalCents: 10000,
						PayStartMinutes: 28,
					},
					Shipping: []cart.ShippingChoice{{VersionID: "shipping", Code: "home", Name: "Home delivery"}}, Chosen: "shipping",
					AvailableCreditCents: tt.credit, CouponDiscountCents: tt.discount,
				}
				if tt.discount > 0 {
					view.CouponApplied = "FULL"
				}
				body := renderComponent(t, ctx, Page(Meta(ctx), &view))
				doc, err := html.Parse(strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				type placementFacts struct {
					Default, Visible, Note string
					Notes                  int
					NoteBeforeVisible      bool
				}
				var got placementFacts
				var visible *html.Node
				var form *html.Node
				for n := range doc.Descendants() {
					if n.Type == html.ElementNode && n.Data == "form" && attrValue(n, "id") == "checkout-form" {
						form = n
						break
					}
				}
				if form == nil {
					t.Fatal("checkout has no placement form")
				}
				for n := range form.Descendants() {
					if n.Type != html.ElementNode || n.Data != "button" || attrValue(n, "type") != "submit" {
						continue
					}
					if attrValue(n, "aria-hidden") == "true" {
						got.Default = strings.TrimSpace(nodeText(n))
					}
					if strings.Contains(attrValue(n, "class"), "goen-btn--primary") {
						visible = n
						got.Visible = strings.TrimSpace(nodeText(n))
					}
				}
				if visible == nil {
					t.Fatal("checkout has no visible placement button")
				}
				seenVisible := false
				for n := visible.Parent.FirstChild; n != nil; n = n.NextSibling {
					if n == visible {
						seenVisible = true
					}
					if n.Type == html.ElementNode && n.Data == "p" && attrValue(n, "class") == "goen-checkout__note" {
						got.Note = strings.TrimSpace(nodeText(n))
						got.Notes++
						got.NoteBeforeVisible = !seenVisible
					}
				}
				want := placementFacts{Default: tt.label, Visible: tt.label, Note: tt.note, Notes: 1, NoteBeforeVisible: true}
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("checkout placement explanation (-want +got):\n%s", diff)
				}
			})
		}
	}
}

// TestPageChosenOptionChecked: Page marks the chosen shipping and invoice
// option with the radio's checked attribute alone, with no second server-side
// class. It holds the single source for "this is the one chosen". The page used
// to say it twice — a class the server rendered onto the label, and a
// :has(input:checked) rule following the live control — and the two disagree
// the moment a shopper presses one, which is #378's first symptom. The checked
// attribute is what a browser, a screen reader and the stylesheet all read, so
// it is the one that stays.
func TestPageChosenOptionChecked(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	view := View{
		Cart: cart.View{Lines: []cart.Line{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping: []cart.ShippingChoice{
			{VersionID: "ship-1", Code: "home", Name: "宅配到府"},
			{VersionID: "ship-2", Code: "store_pickup", Name: "超商取貨"},
		},
		Chosen:         "ship-2",
		Invoice:        InvoiceView{Type: "mobile_carrier"},
		InvoiceChoices: []InvoiceChoice{{Value: "member_carrier", Label: "會員載具"}, {Value: "mobile_carrier", Label: "手機條碼載具"}},
	}
	markup := renderToString(t, Page(Meta(ctx), &view))

	for _, want := range []string{`value="ship-2" checked`, `value="mobile_carrier" checked`} {
		if !strings.Contains(markup, want) {
			t.Errorf("the chosen option does not carry %s — nothing in the document "+
				"says which one it is, so the card cannot show it and a screen "+
				"reader cannot announce it", want)
		}
	}
	for _, unwanted := range []string{`value="ship-1" checked`, `value="member_carrier" checked`} {
		if strings.Contains(markup, unwanted) {
			t.Errorf("an option nobody picked renders %s", unwanted)
		}
	}

	// And no second marker beside it. A class the server paints onto the chosen
	// label is a fact stored twice.
	if strings.Contains(markup, "--on") {
		t.Error("the chooser still renders a server-side selected class beside the " +
			"checked radio; two sources for one fact disagree after a press")
	}
}

// TestPageCouponApplyButton: Page gives the coupon field its own apply button,
// an update submit that follows the order button so Enter still places the
// order. It holds the control that makes a discount code checkable. The field
// on its own left the shopper one button — the one that buys — so the only way
// to learn whether a code worked was to place the order and read the total
// afterwards.
//
// It is an `update` submit like the three chooser buttons, because it wants the
// same thing: re-render this form with one more decision applied.
func TestPageCouponApplyButton(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	view := View{
		Cart:     cart.View{Lines: []cart.Line{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping: []cart.ShippingChoice{{VersionID: "s1", Code: "home", Name: "宅配到府"}},
		Chosen:   "s1",
	}
	markup := renderToString(t, Page(Meta(ctx), &view))

	_, form, ok := strings.Cut(markup, `action="/checkout"`)
	if !ok {
		t.Fatal("the checkout rendered no form")
	}
	form, _, _ = strings.Cut(form, "</form>")

	_, coupon, ok := strings.Cut(form, `id="coupon"`)
	if !ok {
		t.Fatal("the checkout rendered no coupon field")
	}
	button, _, ok := strings.Cut(coupon, "</button>")
	if !ok {
		t.Fatal("no control follows the coupon field, so a code can only be tried by buying")
	}
	for _, want := range []string{`name="update"`, `value="coupon"`} {
		if !strings.Contains(button, want) {
			t.Errorf("the control beside the coupon field is missing %s:\n%s", want, button)
		}
	}

	// And it must not become the form's default: Enter in any field still
	// places the order, which TestPageEnterPlacesOrder locks from
	// the other side.
	if strings.Index(form, `value="coupon"`) < strings.Index(form, i18n.T(ctx, i18n.KeyPlaceOrder)) {
		t.Error("the coupon button comes before the order button, so Enter applies a code instead of buying")
	}
}

// TestPageRecipientControls: Page gives a signed-in customer the recipient box
// and the saved-address chooser, with the data each fills from rendered beside
// it. It holds the two controls a signed-in customer gets above the recipient
// fields, with the data each fills from rendered beside it so choosing costs no
// request.
func TestPageRecipientControls(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	view := View{
		Cart:     cart.View{Lines: []cart.Line{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping: []cart.ShippingChoice{{VersionID: "ship-1", Code: "home", Name: "宅配到府"}},
		Chosen:   "ship-1",
		SavedAddresses: []SavedAddress{{
			ID: "addr-1", Label: "家", Name: "王小明", Phone: "0912345678",
			PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 68 號",
		}},
		ChosenAddress: "addr-1",
		Profile:       Profile{Email: "me@example.com", Name: "王小明", Phone: "0912345678"},
		RecipientMe:   true,
	}
	markup := renderToString(t, Page(Meta(ctx), &view))

	box := tagCarrying(t, markup, "data-recipient-me")
	for _, want := range []string{
		`name="recipient_me"`, "checked", `data-name="王小明"`,
		`data-phone="0912345678"`, `data-email="me@example.com"`,
	} {
		if !strings.Contains(box, want) {
			t.Errorf("the recipient box lacks %s:\n%s", want, box)
		}
	}
	if !strings.Contains(markup, i18n.T(ctx, i18n.KeyRecipientIsMe)) {
		t.Error("the recipient box has no label")
	}
	if !strings.Contains(markup, `name="update" value="recipient"`) {
		t.Error("no button applies the recipient box without scripting")
	}

	option := tagCarrying(t, markup, `value="addr-1"`)
	if !strings.Contains(option, "selected") {
		t.Errorf("the chosen saved address is not selected:\n%s", option)
	}
	// Choosing applies on the server, which re-quotes delivery for the postal code.
	if !strings.Contains(tagCarrying(t, markup, `name="address"`), `hx-post="/checkout"`) {
		t.Error("choosing a saved address does not re-render the form, so delivery keeps the old price")
	}
	for _, want := range []string{`name="recipient_prev_name"`, `name="recipient_prev_phone"`} {
		if !strings.Contains(markup, want) {
			t.Errorf("the form does not carry %s, so unticking cannot restore what was there", want)
		}
	}
	if !strings.Contains(markup, `value="`+OtherAddress+`"`) || !strings.Contains(markup, i18n.T(ctx, i18n.KeyOtherAddress)) {
		t.Error("there is no 「其他地址」 option for typing a new address")
	}
	if !strings.Contains(markup, i18n.T(ctx, i18n.KeyChooseSavedAddress)) {
		t.Error("the address select has no label")
	}
}

// TestPageGuestNoControls: Page offers a guest neither the recipient box nor
// the address book, so the form is the one a guest has always had. With no
// account there is no profile and no address book, so the form is the one a
// guest has always had.
func TestPageGuestNoControls(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	view := View{
		Cart:     cart.View{Lines: []cart.Line{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping: []cart.ShippingChoice{{VersionID: "ship-1", Code: "home", Name: "宅配到府"}},
		Chosen:   "ship-1",
	}
	markup := renderToString(t, Page(Meta(ctx), &view))
	for _, gone := range []string{
		"data-recipient-me", "recipient_me", "recipient_prev", `name="address"`,
		i18n.T(ctx, i18n.KeyRecipientIsMe), i18n.T(ctx, i18n.KeyChooseSavedAddress),
	} {
		if strings.Contains(markup, gone) {
			t.Errorf("a guest's checkout carries %q", gone)
		}
	}
}

func couponTestView() View {
	return View{
		Cart:     cart.View{Lines: []cart.Line{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping: []cart.ShippingChoice{{VersionID: "s1", Code: "home", Name: "宅配到府"}},
		Chosen:   "s1",
	}
}

// TestPageCouponSwap: Page's coupon button swaps only the code, the totals, the
// quote, the figures posted beside it and the moved-rows notice when 套用 is
// pressed, and each id it names exists in the page. It holds what a script
// replaces when 套用 is pressed. Each named id must exist in the page, or the
// swap silently drops it; the quote is among them because the code changes it
// and a stale one fails the next 送出訂單, and the figures are because stale
// ones would name rows that never moved. The whole summary is not named: it
// holds the buttons, and replacing them drops the focus.
func TestPageCouponSwap(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := couponTestView()
	markup := renderToString(t, Page(Meta(ctx), &view))

	button := tagCarrying(t, markup, `value="coupon"`)
	for _, want := range []string{
		`hx-post="/checkout"`,
		`hx-swap="none"`,
		`hx-select-oob="#coupon,#coupon-message:innerHTML,#summary-totals,#summary-moved,#checkout-quote,#checkout-shown"`,
		`formaction="/checkout#coupon-field"`,
		`data-coupon-apply`,
		`data-busy="` + i18n.T(ctx, i18n.KeyTooManyRequests) + `"`,
		`data-failed="` + i18n.T(ctx, i18n.KeyCouponUnavailable) + `"`,
	} {
		if !strings.Contains(button, want) {
			t.Errorf("the coupon button omits %s:\n%s", want, button)
		}
	}
	for _, id := range []string{
		"coupon", "coupon-message", "summary-totals", "summary-moved", "checkout-quote", "checkout-shown", "coupon-field",
	} {
		if !strings.Contains(markup, `id="`+id+`"`) {
			t.Errorf("the page has no element with id %q for the coupon swap", id)
		}
	}
	for _, bad := range []string{`#summary,`, `#checkout-region`, `hx-target`} {
		if strings.Contains(button, bad) {
			t.Errorf("the coupon button names %s, which would replace more than the code and the totals", bad)
		}
	}
	if !strings.Contains(markup, `id="coupon-message" aria-live="polite"`) {
		t.Error("the coupon's message is not in a live region, so a refusal is not announced")
	}
}

// TestPageCouponRefusalBanner: Page's banner names a coupon refusal when the
// code is the form's only problem, and keeps the general banner for any other
// refusal. It holds that a form whose only problem is the code does not send
// the shopper through the fields, while any other refusal keeps the general
// banner.
func TestPageCouponRefusalBanner(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	general := i18n.T(ctx, i18n.KeyCheckoutHasErrors)

	only := couponTestView()
	only.Errors = map[string]string{"coupon": "找不到這組折扣碼。"}
	markup := renderToString(t, Page(Meta(ctx), &only))
	if !strings.Contains(markup, "折扣碼無法套用：找不到這組折扣碼。") || strings.Contains(markup, general) {
		t.Error("the banner does not name the coupon refusal")
	}

	both := couponTestView()
	both.Errors = map[string]string{"coupon": "找不到這組折扣碼。", "phone": "請填寫聯絡電話"}
	markup = renderToString(t, Page(Meta(ctx), &both))
	if !strings.Contains(markup, general) {
		t.Error("a form with other refusals lost the general banner")
	}
}

// TestPageFormNovalidateFocus: Page leaves every field to the server to refuse
// (novalidate), and a refused page asks the script to move focus to its first
// refusal. It holds that no field is refused by the browser before the server
// has answered for all of them, and that a refused page says so for the script
// that moves focus.
func TestPageFormNovalidateFocus(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	fresh := couponTestView()
	markup := renderToString(t, Page(Meta(ctx), &fresh))
	form := tagCarrying(t, markup, `id="checkout-form"`)
	if !strings.Contains(form, "novalidate") {
		t.Error("the browser can block a submit with its own bubble for one field and not the others")
	}
	if strings.Contains(form, "data-focus-refused") {
		t.Error("a form nobody has submitted asks for focus on a refusal")
	}

	refused := couponTestView()
	refused.Errors = map[string]string{"phone": "請填寫聯絡電話"}
	markup = renderToString(t, Page(Meta(ctx), &refused))
	if !strings.Contains(tagCarrying(t, markup, `id="checkout-form"`), "data-focus-refused") {
		t.Error("a refused form does not ask for focus on its first refusal")
	}

	src, err := os.ReadFile(filepath.Join("..", "..", "assets", "js", "goen.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `button.dataset.busy`) || !strings.Contains(string(src), `[data-coupon-apply]`) {
		t.Error("the script does not show a refused or failed coupon request in the coupon's message region")
	}
	if !strings.Contains(string(src), `form[data-focus-refused] :is(input, select, textarea, button)[aria-invalid="true"]`) {
		t.Error("the script does not focus the first refused control of a refused checkout")
	}
}
