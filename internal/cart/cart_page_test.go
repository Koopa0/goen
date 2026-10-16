package cart

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/shipping"

	"github.com/google/go-cmp/cmp"
	htmlparse "golang.org/x/net/html"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestCartDeliveryAvailabilityKeepsTheCheckoutDraftEligible(t *testing.T) {
	t.Parallel()
	for _, locale := range []struct {
		locale  i18n.Locale
		message string
	}{
		{locale: i18n.ZhHant, message: "購物車中的商品目前沒有可用的配送方式。請調整商品，或聯絡我們。"},
		{locale: i18n.En, message: "No delivery method is available for this cart. Change the items or contact us."},
	} {
		t.Run(locale.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			for _, tt := range []struct {
				name       string
				noDelivery bool
				blocked    bool
				describes  string
			}{
				{name: "delivery offered"},
				{name: "no delivery", noDelivery: true, describes: "cart-delivery-unavailable"},
				{name: "stock short", blocked: true, describes: "cart-alert"},
				{name: "stock short and no delivery", noDelivery: true, blocked: true, describes: "cart-alert cart-delivery-unavailable"},
			} {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					ctx := i18n.WithLocale(t.Context(), locale.locale)
					v := CartView{Lines: []pages.CartLine{{VariantID: "item", Name: "Item", UnitCents: 100, Quantity: 1, Available: 2}}, SubtotalCents: 100, ItemCount: 1, NoDelivery: tt.noDelivery}
					if tt.blocked {
						v.Lines[0].Quantity, v.Lines[0].Short = 3, true
						v.SubtotalCents, v.ItemCount = 200, 2
					}
					if v.CanCheckout() != !tt.blocked {
						t.Error("delivery availability changed checkout eligibility, which would lose a mid-checkout draft")
					}
					body := renderComponent(t, ctx, Cart(CartMeta(ctx), v))
					doc, err := htmlparse.Parse(strings.NewReader(body))
					if err != nil {
						t.Fatal(err)
					}
					type controls struct {
						CheckoutLink, DisabledCheckout, DeliveryNotice bool
						DisabledRole, Describes                        string
						DeliveryNoticeIDs                              int
					}
					got := controls{DeliveryNotice: strings.Contains(body, locale.message)}
					for n := range doc.Descendants() {
						if n.Type != htmlparse.ElementNode {
							continue
						}
						attrs := map[string]string{}
						for _, a := range n.Attr {
							attrs[a.Key] = a.Val
						}
						if n.Data == "a" && attrs["href"] == "/checkout" {
							got.CheckoutLink = true
						}
						if n.Data == "span" && attrs["aria-disabled"] == "true" && strings.Contains(attrs["class"], "goen-btn--primary") {
							got.DisabledCheckout = true
							got.DisabledRole, got.Describes = attrs["role"], attrs["aria-describedby"]
						}
						if attrs["id"] == "cart-delivery-unavailable" {
							got.DeliveryNoticeIDs++
						}
					}
					want := controls{CheckoutLink: !tt.noDelivery && !tt.blocked, DisabledCheckout: tt.noDelivery || tt.blocked, DeliveryNotice: tt.noDelivery, Describes: tt.describes}
					if want.DisabledCheckout {
						want.DisabledRole = "link"
					}
					if tt.noDelivery {
						want.DeliveryNoticeIDs = 1
					}
					if diff := cmp.Diff(want, got); diff != "" {
						t.Errorf("cart delivery controls (-want +got):\n%s", diff)
					}
				})
			}
		})
	}
}

// TestEveryCheckoutChoiceSurvivesChangingAnother holds that the choosers are
// radio groups inside the checkout form, so changing one keeps what was typed
// into the others and still works with scripting off.
func TestEveryCheckoutChoiceSurvivesChangingAnother(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	typed := CheckoutView{
		Cart:     CartView{Lines: []pages.CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping: []ShippingChoice{{VersionID: "ship-1", Code: "home", Name: "宅配到府"}},
		Chosen:   "ship-1",
		Address: CheckoutAddress{
			Name: "王小明", Phone: "0912345678", Street: "松高路 99 號", Note: "放管理室",
		},
		CouponCode:     "SAVE10",
		Invoice:        CheckoutInvoice{Type: "mobile_carrier", MobileBarcode: "/ABC+123"},
		InvoiceChoices: []InvoiceChoice{{Value: "mobile_carrier", Label: "手機條碼載具"}},
	}
	html := renderToString(t, Checkout(CheckoutMeta(ctx), &typed))

	// Everything typed is still in the document, so a re-render returns it.
	for _, want := range []string{"王小明", "0912345678", "松高路 99 號", "放管理室", "SAVE10", "/ABC+123"} {
		if !strings.Contains(html, want) {
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
		if !strings.Contains(html, want) {
			t.Errorf("the checkout does not carry %s — a chooser outside the form "+
				"cannot preserve what has been typed into it", want)
		}
	}
	if strings.Contains(html, "/checkout?ship=") || strings.Contains(html, "/checkout?invoice=") {
		t.Error("a chooser is still a link carrying only its own parameter")
	}
}

// TestChangingACheckoutChoiceAppliesIt holds the scripting-on half of the
// chooser: changing a radio sends exactly what the 更新 button beside it sends —
// everything typed, plus the name of the chooser — and the answer replaces the
// form where it stands. Without it the dot moves and the fields that choice
// decides are not asked for until a button the page never mentioned is pressed.
func TestChangingACheckoutChoiceAppliesIt(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	view := CheckoutView{
		Cart:           CartView{Lines: []pages.CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping:       []ShippingChoice{{VersionID: "ship-1", Code: "home", Name: "宅配到府"}},
		Chosen:         "ship-1",
		SavedAddresses: []SavedAddress{{ID: "addr-1", Label: "家"}},
		InvoiceChoices: []InvoiceChoice{{Value: "mobile_carrier", Label: "手機條碼載具"}},
	}
	html := renderToString(t, Checkout(CheckoutMeta(ctx), &view))

	if !strings.Contains(html, `id="checkout-form"`) {
		t.Fatal("the checkout form has no id, so a swap has nothing to select or to replace")
	}
	for _, which := range []string{"shipping", "address", "invoice"} {
		group := tagCarrying(t, html, `hx-vals="{&#34;update&#34;:&#34;`+which+`&#34;}"`)
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
		if !strings.Contains(html, `name="update" value="`+which+`"`) {
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
		if !strings.Contains(html, want) {
			t.Errorf("no radio carries %s, so the swap answers with the focus lost", want)
		}
	}
}

func TestTheCartSummaryEndsOnTheTotal(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	v := CartView{Lines: []pages.CartLine{{VariantID: "item", Name: "Item", UnitCents: 100, Quantity: 1, Available: 2}}, SubtotalCents: 100, ItemCount: 1}
	body := renderComponent(t, ctx, Cart(CartMeta(ctx), v))
	_, summary, ok := strings.Cut(body, `id="cart-summary"`)
	if !ok {
		t.Fatal("the cart has no summary")
	}
	shippingAt := strings.Index(summary, "<dt>"+i18n.T(ctx, i18n.KeyShippingFee)+"</dt>")
	total := strings.Index(summary, `goen-summary__row--total"><dt>`+i18n.T(ctx, i18n.KeySubtotal)+"</dt>")
	if shippingAt < 0 || total < 0 {
		t.Fatalf("the summary lacks a row: shipping at %d, total at %d", shippingAt, total)
	}
	if total < shippingAt {
		t.Error("the cart summary opens on its total, so the total's rule has nothing above it")
	}
}

// TestTheApplyButtonAimsAtTheSectionItChanged holds the scripting-off half. The
// answer is the whole form again, and a form that opens at its top has taken the
// customer away from the control they just pressed.
func TestTheApplyButtonAimsAtTheSectionItChanged(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	view := CheckoutView{
		Cart:           CartView{Lines: []pages.CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping:       []ShippingChoice{{VersionID: "ship-1", Code: "home", Name: "宅配到府"}},
		Chosen:         "ship-1",
		SavedAddresses: []SavedAddress{{ID: "addr-1", Label: "家"}},
		InvoiceChoices: []InvoiceChoice{{Value: "mobile_carrier", Label: "手機條碼載具"}},
	}
	html := renderToString(t, Checkout(CheckoutMeta(ctx), &view))

	for _, which := range []string{"shipping", "address", "invoice"} {
		if !strings.Contains(html, `formaction="/checkout#`+which+`"`) {
			t.Errorf("the %s 更新 button does not name its own section, so its answer "+
				"opens at the top of the form", which)
		}
		// The fragment names a section that exists; #376 is what a fragment
		// aimed at nothing costs.
		if !strings.Contains(html, `id="`+which+`"`) {
			t.Errorf("no section carries id=%q for the fragment to land on", which)
		}
	}
}

// TestThePickupFormAsksForTheChainAndNothingElse holds what 超商取貨 asks for:
// the chain, as radio cards. Nobody types a store number — shoppers pick a store
// from the carrier's map — and until that map is integrated the field could only
// produce refusals.
func TestThePickupFormAsksForTheChainAndNothingElse(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	view := CheckoutView{
		Cart:         CartView{Lines: []pages.CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping:     []ShippingChoice{{VersionID: "ship-1", Code: "pickup", Name: "超商取貨"}},
		Chosen:       "ship-1",
		Destination:  "pickup_point",
		PickupChains: pages.CheckoutPickupChainChoices(),
	}
	html := renderToString(t, Checkout(CheckoutMeta(ctx), &view))

	for _, want := range []string{
		`type="radio" name="pickup_chain" value="seven_eleven"`,
		`type="radio" name="pickup_chain" value="family_mart"`,
		"7-ELEVEN",
		"全家",
		// The selected look is the checked radio's, on the same card the
		// shipping and invoice choosers use.
		`<label class="goen-checkout__ship">`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the pickup form does not offer the chain as a radio card: %s", want)
		}
	}
	for _, gone := range []string{`name="pickup_store_code"`, `name="pickup_store_name"`, "<select"} {
		if strings.Contains(html, gone) {
			t.Errorf("the pickup form still asks for %s", gone)
		}
	}
}

// tagCarrying is the opening tag of the element that carries needle.
func tagCarrying(t *testing.T, html, needle string) string {
	t.Helper()

	at := strings.Index(html, needle)
	if at < 0 {
		t.Fatalf("the rendered checkout carries no %s", needle)
	}
	start := strings.LastIndex(html[:at], "<")
	end := strings.Index(html[at:], ">")
	if start < 0 || end < 0 {
		t.Fatalf("%s is not inside a tag", needle)
	}
	return html[start : at+end+1]
}

// TestTheAddressBookIsOnlyOfferedForAnAddress holds that a saved street address
// is not offered for a convenience-store pickup, whose form has no such fields.
func TestTheAddressBookIsOnlyOfferedForAnAddress(t *testing.T) {
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
			v := &CheckoutView{Destination: tt.destination, SavedAddresses: tt.book}
			if got := v.OffersTheAddressBook(); got != tt.want {
				t.Errorf("OffersTheAddressBook() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestTheCheckoutShowsTheRequotedFigure holds that a re-quoted fee beats the
// mainland estimate the method chooser shows before an address is typed.
func TestTheCheckoutShowsTheRequotedFigure(t *testing.T) {
	t.Parallel()

	choices := []ShippingChoice{{VersionID: "v1", FeeCents: 8000}}

	before := &CheckoutView{Shipping: choices, Chosen: "v1"}
	if got := before.ShippingFeeCents(); got != 8000 {
		t.Errorf("before an address is typed the estimate is %d, want 8000", got)
	}

	after := &CheckoutView{Shipping: choices, Chosen: "v1", QuotedShippingCents: 28000}
	if got := after.ShippingFeeCents(); got != 28000 {
		t.Errorf("after re-quoting the summary shows %d, want 28000 — it is still "+
			"showing the mainland estimate", got)
	}
}

// TestTheCheckoutSummaryAddsUpTheWayPlaceOrderDoes holds the summary against
// cart.priceOrder: a free-delivery coupon pays the base rate and never the
// outlying-island surcharge, which the fee row states on a line of its own.
func TestTheCheckoutSummaryAddsUpTheWayPlaceOrderDoes(t *testing.T) {
	t.Parallel()

	base := func() *CheckoutView {
		return &CheckoutView{
			Cart:                CartView{SubtotalCents: 100000},
			Shipping:            []ShippingChoice{{VersionID: "v1", FeeCents: 8000}},
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
		v := &CheckoutView{
			Cart:     CartView{SubtotalCents: 100000},
			Shipping: []ShippingChoice{{VersionID: "v1", FeeCents: 8000}},
			Chosen:   "v1",
		}
		if got := v.TotalCents(); got != 108000 {
			t.Errorf("total = %d, want 108000", got)
		}
	})
}

// TestTheCheckoutSummaryNamesTheVariant holds that the summary names the
// variant, on the one screen built to confirm what is about to be paid for.
func TestTheCheckoutSummaryNamesTheVariant(t *testing.T) {
	t.Parallel()

	view := CheckoutView{
		Cart: CartView{Lines: []pages.CartLine{{
			Name: "Pixelight 9 Pro", Label: "星霧藍 / 512GB",
			Quantity: 1, UnitCents: 3190000,
		}}},
		Shipping: []ShippingChoice{{Code: "home", Name: "宅配到府", FeeCents: 8000}},
		Chosen:   "home",
	}
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Checkout(CheckoutMeta(ctx), &view))

	// The summary specifically, not the page: the line above the fold carries
	// the label too.
	_, summary, ok := strings.Cut(html, "goen-checkout__lines")
	if !ok {
		t.Fatal("the checkout rendered no summary list")
	}
	summary, _, _ = strings.Cut(summary, "</ul>")
	if !strings.Contains(summary, "星霧藍 / 512GB") {
		t.Errorf("the checkout summary does not name the variant:\n%s", summary)
	}
}

// TestTheInvoiceFormAsksForOneThing holds which half of the form exists.
// 會員載具, 手機條碼載具, 捐贈 and 公司統編 need different information, and Validate()
// blanks the field that does not apply, so rendering both would leave the form
// promising something the server will not demand.
func TestTheInvoiceFormAsksForOneThing(t *testing.T) {
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
			view := CheckoutView{
				Cart:           CartView{Lines: []pages.CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
				Shipping:       []ShippingChoice{{Code: "home", Name: "宅配到府"}},
				Invoice:        CheckoutInvoice{Type: tt.kind},
				InvoiceChoices: []InvoiceChoice{{Value: "member_carrier", Label: "會員載具"}},
			}
			html := renderToString(t, Checkout(CheckoutMeta(ctx), &view))
			if got := strings.Contains(html, `id="invoice_carrier"`); got != tt.wantBarcode {
				t.Errorf("invoice=%q renders the mobile barcode field = %v, want %v", tt.kind, got, tt.wantBarcode)
			}
			if got := strings.Contains(html, `id="invoice_donation_code"`); got != tt.wantDonation {
				t.Errorf("invoice=%q renders the donation code field = %v, want %v", tt.kind, got, tt.wantDonation)
			}
			if got := strings.Contains(html, `id="invoice_tax_id"`); got != tt.wantTaxID {
				t.Errorf("invoice=%q renders the 統編 field = %v, want %v", tt.kind, got, tt.wantTaxID)
			}
			if got := strings.Contains(html, `id="invoice_company_name"`); got != tt.wantCompanyName {
				t.Errorf("invoice=%q renders the company-name field = %v, want %v",
					tt.kind, got, tt.wantCompanyName)
			}
		})
	}

	// The three choosers compose because they are three radio groups in ONE
	// form, so a submission carries all of them.
	full := CheckoutView{
		Cart:           CartView{Lines: []pages.CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping:       []ShippingChoice{{VersionID: "ship-1", Code: "home", Name: "宅配到府"}},
		Chosen:         "ship-1",
		Invoice:        CheckoutInvoice{Type: "company"},
		InvoiceChoices: []InvoiceChoice{{Value: "company", Label: "公司統編"}},
	}
	html := renderToString(t, Checkout(CheckoutMeta(ctx), &full))
	for _, want := range []string{
		`type="radio" name="shipping"`,
		`type="radio" name="invoice_type"`,
		// Each 更新 names the chooser it applies, so the handler can tell which
		// one was pressed.
		`name="update" value="shipping"`,
		`name="update" value="invoice"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the checkout does not carry %s — a chooser outside the form "+
				"cannot preserve what has been typed into it", want)
		}
	}
}

// TestAnOffshoreRequoteIsNotAnError holds a re-quote as a notice rather than a
// field error — the method is chosen above the address, so the fee on screen is
// a mainland estimate and nothing the customer typed was wrong — while still
// answering 422, because the submission was not accepted.
func TestAnOffshoreRequoteIsNotAnError(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	view := CheckoutView{
		Cart:     CartView{Lines: []pages.CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping: []ShippingChoice{{Code: "home", Name: "宅配到府"}},
		Repriced: "離島運費另計,已更新為 NT$280。確認後再送出一次。",
	}
	html := renderToString(t, Checkout(CheckoutMeta(ctx), &view))

	if !strings.Contains(html, view.Repriced) {
		t.Fatal("the re-quote is not shown at all")
	}
	// The element carrying it, not merely the words: the string appears the
	// same either way.
	_, after, ok := strings.Cut(html, view.Repriced)
	if !ok {
		t.Fatal("could not locate the message")
	}
	before := html[:len(html)-len(after)-len(view.Repriced)]
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

func TestAChangedCreditBalanceHasItsOwnStatusNotice(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	//nolint:gosec // G101: a customer-facing balance-change notice, not a credential
	view := CheckoutView{
		Cart:          CartView{Lines: []pages.CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping:      []ShippingChoice{{Code: "home", Name: "宅配到府"}},
		CreditChanged: "可用購物金已變更為 NT$10。請確認後再送出一次。",
	}
	html := renderToString(t, Checkout(CheckoutMeta(ctx), &view))

	if !strings.Contains(html, view.CreditChanged) {
		t.Fatal("the refreshed credit balance is not shown")
	}
	_, after, ok := strings.Cut(html, view.CreditChanged)
	if !ok {
		t.Fatal("could not locate the refreshed credit message")
	}
	before := html[:len(html)-len(after)-len(view.CreditChanged)]
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

// TestAShortLineSaysWhatItIsPricedFor holds arithmetic a customer can check: a
// line the shelf cannot meet is priced for what CAN be supplied, and the page
// says so rather than leaving a quantity box that disagrees with the total.
func TestAShortLineSaysWhatItIsPricedFor(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	short := pages.CartLine{
		VariantID: "v1", Slug: "x", Name: "Pixelight 9 Pro",
		UnitCents: 100000, Quantity: 5, Available: 2, Short: true,
	}
	if got, want := short.LineTotal(), "NT$2,000"; got != want {
		t.Errorf("LineTotal() = %q, want %q — the price is for what can be supplied", got, want)
	}

	html := renderToString(t, Cart(CartMeta(ctx), CartView{Lines: []pages.CartLine{short}}))
	said := fmt.Sprintf(i18n.T(ctx, i18n.KeyPricedFor), "2")
	if !strings.Contains(html, said) {
		t.Errorf("a line asking for 5 and priced for 2 does not say so; wanted %q", said)
	}

	// A line the shelf can meet says nothing: "priced for 5" beside "× 5" is
	// noise on every ordinary row in the cart.
	ok := pages.CartLine{VariantID: "v2", Slug: "y", Name: "y", UnitCents: 100000, Quantity: 2, Available: 9}
	full := renderToString(t, Cart(CartMeta(ctx), CartView{Lines: []pages.CartLine{ok}}))
	if strings.Contains(full, i18n.T(ctx, i18n.KeyPricedFor)[:3]) {
		t.Error("an ordinary line is annotated with what it is priced for")
	}
}

// TestEnterInTheCheckoutPlacesTheOrder holds a rule the browser applies and no
// linter can see: HTML makes the FIRST submit button in tree order the one
// Enter activates in any text field, so a leading submit must place the order
// rather than a chooser's 更新 re-rendering the form. Asserting ORDER is the
// whole point: both buttons exist either way.
func TestEnterInTheCheckoutPlacesTheOrder(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	view := CheckoutView{
		Cart:           CartView{Lines: []pages.CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping:       []ShippingChoice{{VersionID: "s1", Code: "home", Name: "宅配到府"}},
		Chosen:         "s1",
		InvoiceChoices: []InvoiceChoice{{Value: "member_carrier", Label: "會員載具"}},
	}
	html := renderToString(t, Checkout(CheckoutMeta(ctx), &view))

	_, form, ok := strings.Cut(html, `action="/checkout"`)
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

func TestCheckoutExplainsWhatPlacingTheOrderDoes(t *testing.T) {
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
				view := CheckoutView{
					Cart: CartView{
						Lines: []pages.CartLine{{Name: "Item", Quantity: 1, UnitCents: 10000}}, SubtotalCents: 10000,
						PayStartMinutes: 28,
					},
					Shipping: []ShippingChoice{{VersionID: "shipping", Code: "home", Name: "Home delivery"}}, Chosen: "shipping",
					AvailableCreditCents: tt.credit, CouponDiscountCents: tt.discount,
				}
				if tt.discount > 0 {
					view.CouponApplied = "FULL"
				}
				body := renderComponent(t, ctx, Checkout(CheckoutMeta(ctx), &view))
				doc, err := htmlparse.Parse(strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				type placementFacts struct {
					Default, Visible, Note string
					Notes                  int
					NoteBeforeVisible      bool
				}
				var got placementFacts
				var visible *htmlparse.Node
				var form *htmlparse.Node
				for n := range doc.Descendants() {
					if n.Type == htmlparse.ElementNode && n.Data == "form" && attrValue(n, "id") == "checkout-form" {
						form = n
						break
					}
				}
				if form == nil {
					t.Fatal("checkout has no placement form")
				}
				for n := range form.Descendants() {
					if n.Type != htmlparse.ElementNode || n.Data != "button" || attrValue(n, "type") != "submit" {
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
					if n.Type == htmlparse.ElementNode && n.Data == "p" && attrValue(n, "class") == "goen-checkout__note" {
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

// TestTheChosenOptionIsMarkedOnTheRadioAlone holds the single source for "this
// is the one chosen". The page used to say it twice — a class the server
// rendered onto the label, and a :has(input:checked) rule following the live
// control — and the two disagree the moment a shopper presses one, which is
// #378's first symptom. The checked attribute is what a browser, a screen
// reader and the stylesheet all read, so it is the one that stays.
func TestTheChosenOptionIsMarkedOnTheRadioAlone(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	view := CheckoutView{
		Cart: CartView{Lines: []pages.CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping: []ShippingChoice{
			{VersionID: "ship-1", Code: "home", Name: "宅配到府"},
			{VersionID: "ship-2", Code: "store_pickup", Name: "超商取貨"},
		},
		Chosen:         "ship-2",
		Invoice:        CheckoutInvoice{Type: "mobile_carrier"},
		InvoiceChoices: []InvoiceChoice{{Value: "member_carrier", Label: "會員載具"}, {Value: "mobile_carrier", Label: "手機條碼載具"}},
	}
	html := renderToString(t, Checkout(CheckoutMeta(ctx), &view))

	for _, want := range []string{`value="ship-2" checked`, `value="mobile_carrier" checked`} {
		if !strings.Contains(html, want) {
			t.Errorf("the chosen option does not carry %s — nothing in the document "+
				"says which one it is, so the card cannot show it and a screen "+
				"reader cannot announce it", want)
		}
	}
	for _, unwanted := range []string{`value="ship-1" checked`, `value="member_carrier" checked`} {
		if strings.Contains(html, unwanted) {
			t.Errorf("an option nobody picked renders %s", unwanted)
		}
	}

	// And no second marker beside it. A class the server paints onto the chosen
	// label is a fact stored twice.
	if strings.Contains(html, "--on") {
		t.Error("the chooser still renders a server-side selected class beside the " +
			"checked radio; two sources for one fact disagree after a press")
	}
}

// TestACouponCanBeAppliedWithoutPlacingTheOrder holds the control that makes a
// discount code checkable. The field on its own left the shopper one button —
// the one that buys — so the only way to learn whether a code worked was to
// place the order and read the total afterwards.
//
// It is an `update` submit like the three chooser buttons, because it wants the
// same thing: re-render this form with one more decision applied.
func TestACouponCanBeAppliedWithoutPlacingTheOrder(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	view := CheckoutView{
		Cart:     CartView{Lines: []pages.CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping: []ShippingChoice{{VersionID: "s1", Code: "home", Name: "宅配到府"}},
		Chosen:   "s1",
	}
	html := renderToString(t, Checkout(CheckoutMeta(ctx), &view))

	_, form, ok := strings.Cut(html, `action="/checkout"`)
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
	// places the order, which TestEnterInTheCheckoutPlacesTheOrder locks from
	// the other side.
	if strings.Index(form, `value="coupon"`) < strings.Index(form, i18n.T(ctx, i18n.KeyPlaceOrder)) {
		t.Error("the coupon button comes before the order button, so Enter applies a code instead of buying")
	}
}

// TestCartLineLoadsTheSmallPhoto holds that a 96px thumbnail offers the -400
// derivative through the same srcset the product cards use, and is told its
// slot is 96px wide so the browser does not fall back to the 1600px source.
func TestCartLineLoadsTheSmallPhoto(t *testing.T) {
	t.Parallel()
	const key = "nimbus-buds-pro-01.webp"
	line := pages.CartLine{
		Slug: "buds", Name: "Buds", Quantity: 1, UnitCents: 100,
		ImageURL: assets.Image{BundledKey: key}.URL(), ImageSrcset: assets.Image{BundledKey: key}.Srcset(), ImageAlt: "Buds",
	}
	html := renderToString(t, cartLine(line))
	small := assets.URL("media/products/nimbus-buds-pro-01-400.webp")
	for _, want := range []string{`srcset="` + small + ` 400w`, `sizes="96px"`, `width="96"`, `height="96"`} {
		if !strings.Contains(html, want) {
			t.Errorf("cart line image omits %s:\n%s", want, html)
		}
	}
}

// TestCartLineStillWorksWithScriptingOff holds that the quantity form keeps the
// submit button and the remove control a browser without script relies on, next
// to the attribute that lets script apply a change on its own.
func TestCartLineStillWorksWithScriptingOff(t *testing.T) {
	t.Parallel()
	html := renderToString(t, cartLine(pages.CartLine{Slug: "buds", Name: "Buds", Quantity: 1, UnitCents: 100}))
	for _, want := range []string{
		`action="/cart/items/update" data-autosubmit`,
		`goen-line__update" type="submit">更新`,
		`name="remove" value="1"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("cart line omits %s:\n%s", want, html)
		}
	}
}

// TestCartLineUpdateSwapsOnlyWhatChanges holds that the quantity form is an
// htmx request that replaces the line's text and price, the summary, the
// notices and the header's cart link, and names neither the thumbnail, the
// stepper nor the whole list. Naming any of those would repaint what the
// shopper did not change.
func TestCartLineUpdateSwapsOnlyWhatChanges(t *testing.T) {
	t.Parallel()
	const id = "11111111-1111-4111-8111-111111111111"
	html := renderToString(t, cartLine(pages.CartLine{VariantID: id, Slug: "buds", Name: "Buds", Quantity: 1, UnitCents: 100}))
	for _, want := range []string{
		`hx-post="/cart/items/update"`,
		`hx-swap="none"`,
		`hx-select-oob="#line-body-` + id + `,#line-money-` + id + `,#cart-summary,#cart-notices,#cart-link,#cart-count:innerHTML"`,
		`hx-sync="closest .goen-cart__lines:queue all"`,
		`data-feedback-skip`,
		`id="line-body-` + id + `"`,
		`id="line-money-` + id + `"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("cart line omits %s:\n%s", want, html)
		}
	}
	for _, bad := range []string{`hx-target`, `hx-select=`} {
		if strings.Contains(html, bad) {
			t.Errorf("the line form carries %s, which would replace a whole region:\n%s", bad, html)
		}
	}
}

// TestARecipientCanBeTheMemberAndAnAddressTheirSavedOne holds the two controls a
// signed-in customer gets above the recipient fields, with the data each fills
// from rendered beside it so choosing costs no request.
func TestARecipientCanBeTheMemberAndAnAddressTheirSavedOne(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	view := CheckoutView{
		Cart:     CartView{Lines: []pages.CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping: []ShippingChoice{{VersionID: "ship-1", Code: "home", Name: "宅配到府"}},
		Chosen:   "ship-1",
		SavedAddresses: []SavedAddress{{
			ID: "addr-1", Label: "家", Name: "王小明", Phone: "0912345678",
			PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 68 號",
		}},
		ChosenAddress: "addr-1",
		Profile:       CheckoutProfile{Email: "me@example.com", Name: "王小明", Phone: "0912345678"},
		RecipientMe:   true,
	}
	html := renderToString(t, Checkout(CheckoutMeta(ctx), &view))

	box := tagCarrying(t, html, "data-recipient-me")
	for _, want := range []string{
		`name="recipient_me"`, "checked", `data-name="王小明"`,
		`data-phone="0912345678"`, `data-email="me@example.com"`,
	} {
		if !strings.Contains(box, want) {
			t.Errorf("the recipient box lacks %s:\n%s", want, box)
		}
	}
	if !strings.Contains(html, i18n.T(ctx, i18n.KeyRecipientIsMe)) {
		t.Error("the recipient box has no label")
	}
	if !strings.Contains(html, `name="update" value="recipient"`) {
		t.Error("no button applies the recipient box without scripting")
	}

	option := tagCarrying(t, html, `value="addr-1"`)
	if !strings.Contains(option, "selected") {
		t.Errorf("the chosen saved address is not selected:\n%s", option)
	}
	// Choosing applies on the server, which re-quotes delivery for the postal code.
	if !strings.Contains(tagCarrying(t, html, `name="address"`), `hx-post="/checkout"`) {
		t.Error("choosing a saved address does not re-render the form, so delivery keeps the old price")
	}
	for _, want := range []string{`name="recipient_prev_name"`, `name="recipient_prev_phone"`} {
		if !strings.Contains(html, want) {
			t.Errorf("the form does not carry %s, so unticking cannot restore what was there", want)
		}
	}
	if !strings.Contains(html, `value="`+OtherAddress+`"`) || !strings.Contains(html, i18n.T(ctx, i18n.KeyOtherAddress)) {
		t.Error("there is no 「其他地址」 option for typing a new address")
	}
	if !strings.Contains(html, i18n.T(ctx, i18n.KeyChooseSavedAddress)) {
		t.Error("the address select has no label")
	}
}

// TestAGuestIsOfferedNeitherControl: with no account there is no profile and no
// address book, so the form is the one a guest has always had.
func TestAGuestIsOfferedNeitherControl(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	view := CheckoutView{
		Cart:     CartView{Lines: []pages.CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping: []ShippingChoice{{VersionID: "ship-1", Code: "home", Name: "宅配到府"}},
		Chosen:   "ship-1",
	}
	html := renderToString(t, Checkout(CheckoutMeta(ctx), &view))
	for _, gone := range []string{
		"data-recipient-me", "recipient_me", "recipient_prev", `name="address"`,
		i18n.T(ctx, i18n.KeyRecipientIsMe), i18n.T(ctx, i18n.KeyChooseSavedAddress),
	} {
		if strings.Contains(html, gone) {
			t.Errorf("a guest's checkout carries %q", gone)
		}
	}
}

func couponTestView() CheckoutView {
	return CheckoutView{
		Cart:     CartView{Lines: []pages.CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping: []ShippingChoice{{VersionID: "s1", Code: "home", Name: "宅配到府"}},
		Chosen:   "s1",
	}
}

// TestApplyingACouponSwapsOnlyTheCodeTheTotalsAndTheQuote holds what a script
// replaces when 套用 is pressed. Each named id must exist in the page, or the
// swap silently drops it; the quote is among them because the code changes it
// and a stale one fails the next 送出訂單. The whole summary is not named: it
// holds the buttons, and replacing them drops the focus.
func TestApplyingACouponSwapsOnlyTheCodeTheTotalsAndTheQuote(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := couponTestView()
	html := renderToString(t, Checkout(CheckoutMeta(ctx), &view))

	button := tagCarrying(t, html, `value="coupon"`)
	for _, want := range []string{
		`hx-post="/checkout"`,
		`hx-swap="none"`,
		`hx-select-oob="#coupon,#coupon-message:innerHTML,#summary-totals,#checkout-quote"`,
		`formaction="/checkout#coupon-field"`,
		`data-coupon-apply`,
		`data-busy="` + i18n.T(ctx, i18n.KeyTooManyRequests) + `"`,
		`data-failed="` + i18n.T(ctx, i18n.KeyCouponUnavailable) + `"`,
	} {
		if !strings.Contains(button, want) {
			t.Errorf("the coupon button omits %s:\n%s", want, button)
		}
	}
	for _, id := range []string{"coupon", "coupon-message", "summary-totals", "checkout-quote", "coupon-field"} {
		if !strings.Contains(html, `id="`+id+`"`) {
			t.Errorf("the page has no element with id %q for the coupon swap", id)
		}
	}
	for _, bad := range []string{`#summary,`, `#checkout-region`, `hx-target`} {
		if strings.Contains(button, bad) {
			t.Errorf("the coupon button names %s, which would replace more than the code and the totals", bad)
		}
	}
	if !strings.Contains(html, `id="coupon-message" aria-live="polite"`) {
		t.Error("the coupon's message is not in a live region, so a refusal is not announced")
	}
}

// TestACouponRefusalNamesItselfInTheBanner holds that a form whose only problem
// is the code does not send the shopper through the fields, while any other
// refusal keeps the general banner.
func TestACouponRefusalNamesItselfInTheBanner(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	general := i18n.T(ctx, i18n.KeyCheckoutHasErrors)

	only := couponTestView()
	only.Errors = map[string]string{"coupon": "找不到這組折扣碼。"}
	html := renderToString(t, Checkout(CheckoutMeta(ctx), &only))
	if !strings.Contains(html, "折扣碼無法套用：找不到這組折扣碼。") || strings.Contains(html, general) {
		t.Error("the banner does not name the coupon refusal")
	}

	both := couponTestView()
	both.Errors = map[string]string{"coupon": "找不到這組折扣碼。", "phone": "請填寫聯絡電話"}
	html = renderToString(t, Checkout(CheckoutMeta(ctx), &both))
	if !strings.Contains(html, general) {
		t.Error("a form with other refusals lost the general banner")
	}
}

// TestTheCheckoutFormIsCheckedByTheServerAndFocusesTheFirstRefusal holds that no
// field is refused by the browser before the server has answered for all of
// them, and that a refused page says so for the script that moves focus.
func TestTheCheckoutFormIsCheckedByTheServerAndFocusesTheFirstRefusal(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	fresh := couponTestView()
	html := renderToString(t, Checkout(CheckoutMeta(ctx), &fresh))
	form := tagCarrying(t, html, `id="checkout-form"`)
	if !strings.Contains(form, "novalidate") {
		t.Error("the browser can block a submit with its own bubble for one field and not the others")
	}
	if strings.Contains(form, "data-focus-refused") {
		t.Error("a form nobody has submitted asks for focus on a refusal")
	}

	refused := couponTestView()
	refused.Errors = map[string]string{"phone": "請填寫聯絡電話"}
	html = renderToString(t, Checkout(CheckoutMeta(ctx), &refused))
	if !strings.Contains(tagCarrying(t, html, `id="checkout-form"`), "data-focus-refused") {
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

// cartFactsOf is the markup of the cart's fact line, the region a quantity
// update replaces, so a figure in it cannot go stale after a change.
func cartFactsOf(t *testing.T, v CartView) string {
	t.Helper()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Cart(CartMeta(ctx), v))
	at := strings.Index(html, `id="cart-count"`)
	end := strings.Index(html, `id="cart-notices"`)
	if at < 0 || end < at {
		t.Fatalf("the cart has no fact line before its notices: %s", html)
	}
	return html[at:end]
}

func TestTheCartFactLineStatesItemsSubtotalAndFreeDelivery(t *testing.T) {
	t.Parallel()
	lines := []pages.CartLine{{VariantID: "v", Slug: "s", Name: "x", Quantity: 2, UnitCents: 40000}}
	for _, tt := range []struct {
		name string
		v    CartView
		want []string
		not  []string
	}{
		{
			"short of the threshold",
			CartView{Lines: lines, ItemCount: 2, SubtotalCents: 80000,
				FreeDelivery: FreeDelivery{Kind: FreeDeliveryShort, ShortfallCents: 220000, ThresholdCents: 300000}},
			[]string{"商品", "2\u00a0<small>件</small>", "小計", `<small class="ui-statline__pre">NT$</small>800`,
				"免運還差", `<small class="ui-statline__pre">NT$</small>2,200`, "滿 NT$3,000 免運"},
			nil,
		},
		{
			"threshold reached",
			CartView{Lines: lines, ItemCount: 2, SubtotalCents: 360000,
				FreeDelivery: FreeDelivery{Kind: FreeDeliveryReached, ThresholdCents: 300000}},
			[]string{"運費", "<dd>免運<", "已滿 NT$3,000"},
			[]string{"免運還差", "NT$0"},
		},
		{
			"a method that never turns free",
			CartView{Lines: lines, ItemCount: 2, SubtotalCents: 80000},
			[]string{"商品", "小計"},
			[]string{"免運", "運費"},
		},
	} {
		got := cartFactsOf(t, tt.v)
		for _, w := range tt.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: fact line lacks %q: %s", tt.name, w, got)
			}
		}
		for _, w := range tt.not {
			if strings.Contains(got, w) {
				t.Errorf("%s: fact line holds %q: %s", tt.name, w, got)
			}
		}
	}
}

func TestAnEmptyCartHasNoFactLine(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	if html := renderToString(t, Cart(CartMeta(ctx), CartView{})); strings.Contains(html, "ui-statline") {
		t.Error("an empty cart prints a fact line")
	}
}

func TestASoldOutLineIsNotCountedAndOffersOnlyRemove(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	v := CartView{
		Lines: []pages.CartLine{
			{VariantID: "ok", Slug: "a", Name: "a", Quantity: 2, Available: 5, UnitCents: 40000},
			{VariantID: "gone", Slug: "b", Name: "b", Quantity: 1, UnitCents: 99900, Unavailable: true},
		},
		ItemCount: 2, SubtotalCents: 80000,
	}
	html := renderToString(t, Cart(CartMeta(ctx), v))

	if !strings.Contains(html, "2\u00a0<small>件</small>") || strings.Contains(html, "3\u00a0<small>件</small>") {
		t.Error("the sold-out line is counted in 商品 n 件")
	}
	if strings.Contains(html, "已無庫存") {
		t.Error("the sold-out line still says 已無庫存")
	}
	_, afterOpen, found := strings.Cut(html, `id="line-gone"`)
	gone, _, closed := strings.Cut(afterOpen, "</li>")
	if !found || !closed {
		t.Fatalf("the sold-out line is not in the cart: %s", html)
	}
	if !strings.Contains(gone, i18n.T(ctx, i18n.KeySoldOut)) {
		t.Error("the sold-out line itself does not say 已售完")
	}
	if strings.Contains(gone, "goen-stepper") || strings.Contains(gone, "goen-line__update") {
		t.Errorf("the sold-out line offers more than removing it: %s", gone)
	}
	if !strings.Contains(gone, "goen-line__remove") {
		t.Error("the sold-out line cannot be removed")
	}
	if !strings.Contains(html, `aria-disabled="true"`) || strings.Contains(html, `href="/checkout"`) {
		t.Error("checkout is offered while a line is sold out")
	}
	if !strings.Contains(html, `aria-describedby="cart-alert"`) || !strings.Contains(html, `id="cart-alert"`) {
		t.Error("the disabled checkout does not point at the sentence that says why")
	}
}

func TestTheCartSentenceSaysWhatBlocksCheckout(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	line := pages.CartLine{VariantID: "v", Slug: "s", Name: "x", Quantity: 1, UnitCents: 100}
	soldOut, short := line, line
	soldOut.Unavailable = true
	short.Short = true

	for _, tt := range []struct {
		name  string
		lines []pages.CartLine
		want  string
	}{
		{"sold out", []pages.CartLine{soldOut}, "有商品已售完，移除後才能結帳。"},
		{"short", []pages.CartLine{short}, "有商品的庫存不足，請先調整數量再結帳。"},
		{"both: the sold-out line is the one to deal with first", []pages.CartLine{short, soldOut}, "有商品已售完，移除後才能結帳。"},
		{"neither", []pages.CartLine{line}, ""},
	} {
		got := CartView{Lines: tt.lines}.StockNotice(ctx)
		if got != tt.want {
			t.Errorf("%s: StockNotice = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestTheCartShowsTheStockHoldUnderCheckout(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	lines := []pages.CartLine{{VariantID: "v", Slug: "s", Name: "x", Quantity: 1, UnitCents: 100}}
	html := renderToString(t, Cart(CartMeta(ctx), CartView{Lines: lines, HoldMinutes: 61}))
	if !strings.Contains(html, "庫存保留 61 分鐘") {
		t.Error("the cart does not say how long the stock is held")
	}
}

// TestRemovingALineIsNotBlockedByAQuantityTheShelfCannotMeet: the remove button
// shares a form with the quantity field, whose max is the stock, so a number
// typed above it would stop the removal behind a native validation bubble.
func TestRemovingALineIsNotBlockedByAQuantityTheShelfCannotMeet(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	line := pages.CartLine{VariantID: "v1", Slug: "x", Name: "x", UnitCents: 100000, Quantity: 2, Available: 4}
	html := renderToString(t, Cart(CartMeta(ctx), CartView{Lines: []pages.CartLine{line}}))
	i := strings.Index(html, `name="remove"`)
	if i < 0 {
		t.Fatal("the line has no remove button")
	}
	open := strings.LastIndex(html[:i], "<button")
	if tag := html[open : i+strings.Index(html[i:], ">")]; !strings.Contains(tag, "formnovalidate") {
		t.Errorf("the remove button does not skip the form's validation: %s", tag)
	}
}

func TestTheEnglishItemCountIsTheBareNumber(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	var b strings.Builder
	facts := CartView{ItemCount: 2, SubtotalCents: 200}.Facts(ctx)
	if err := components.StatLine(facts, components.StatLinePlain).Render(ctx, &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := b.String()
	if !strings.Contains(html, "<dt>Items</dt><dd>2</dd>") || strings.Contains(html, "pcs") {
		t.Errorf("English item count = %s, want the bare number under Items", html)
	}
}

func TestTheCartSummaryShippingRowAgreesWithTheFreeDeliveryFact(t *testing.T) {
	t.Parallel()
	lines := []pages.CartLine{{VariantID: "v", Slug: "s", Name: "x", Quantity: 2, UnitCents: 180000}}
	for _, tt := range []struct {
		name   string
		locale i18n.Locale
		kind   FreeDeliveryKind
		zones  []string
		want   string
		not    string
	}{
		{"reached with a surcharge zone zh", i18n.ZhHant, FreeDeliveryReached, []string{"離島", "外島"}, "免運（離島、外島另計）", "結帳時計算"},
		{"reached with a surcharge zone en", i18n.En, FreeDeliveryReached, []string{"Outlying islands"}, "Free (Outlying islands extra)", "Calculated at checkout"},
		{"reached zh", i18n.ZhHant, FreeDeliveryReached, nil, "免運", "結帳時計算"},
		{"reached en", i18n.En, FreeDeliveryReached, nil, ">Free<", "Calculated at checkout"},
		{"short zh", i18n.ZhHant, FreeDeliveryShort, nil, "結帳時計算", ">免運<"},
		{"short en", i18n.En, FreeDeliveryShort, nil, "Calculated at checkout", ">Free<"},
	} {
		ctx := i18n.WithLocale(t.Context(), tt.locale)
		v := CartView{Lines: lines, ItemCount: 2, SubtotalCents: 360000,
			FreeDelivery: FreeDelivery{Kind: tt.kind, ShortfallCents: 1, ThresholdCents: 300000, SurchargeZones: tt.zones}}
		var b strings.Builder
		if err := Cart(CartMeta(ctx), v).Render(ctx, &b); err != nil {
			t.Fatalf("%s: render: %v", tt.name, err)
		}
		html := b.String()
		if tt.kind == FreeDeliveryReached {
			facts := html[strings.Index(html, `id="cart-count"`):strings.Index(html, `id="cart-notices"`)]
			if !strings.Contains(facts, tt.want) {
				t.Errorf("%s: fact line lacks %q: %s", tt.name, tt.want, facts)
			}
		}
		at := strings.Index(html, `id="cart-summary"`)
		if at < 0 {
			t.Fatalf("%s: no cart summary: %s", tt.name, html)
		}
		html = html[at:]
		if end := strings.Index(html, "</dl>"); end > 0 {
			html = html[:end]
		}
		if !strings.Contains(html, tt.want) {
			t.Errorf("%s: summary lacks %q: %s", tt.name, tt.want, html)
		}
		if strings.Contains(html, tt.not) {
			t.Errorf("%s: summary holds %q: %s", tt.name, tt.not, html)
		}
		if len(tt.zones) == 0 && (strings.Contains(html, "另計") || strings.Contains(html, " extra)")) {
			t.Errorf("%s: summary names a surcharge zone where there is none: %s", tt.name, html)
		}
	}
}
