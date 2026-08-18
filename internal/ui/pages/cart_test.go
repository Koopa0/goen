package pages

import (
	"strings"
	"testing"

	"github.com/a-h/templ"

	"fmt"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// TestEveryCheckoutChoiceSurvivesChangingAnother holds what the chooser links
// used to buy, and what they cost.
//
// Each was an <a> carrying only the chooser parameters, so pressing one
// discarded the name, phone, address, note, coupon and carrier already typed.
// The 發票 chooser sits BELOW the address fields, which makes it the sharpest
// case: choosing how to be invoiced threw away a whole recipient.
//
// They are radio groups in the checkout form now, with a submit button that
// re-renders. The form carries everything, so composition is structural rather
// than something each link has to remember to rebuild — and it still works with
// scripting off, which is what the link shape was protecting.
func TestEveryCheckoutChoiceSurvivesChangingAnother(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	typed := CheckoutView{
		Cart:     CartView{Lines: []CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping: []ShippingChoice{{VersionID: "ship-1", Code: "home", Name: "宅配到府"}},
		Chosen:   "ship-1",
		Address: CheckoutAddress{
			Name: "王小明", Phone: "0912345678", Street: "松高路 99 號", Note: "放管理室",
		},
		CouponCode:     "SAVE10",
		Invoice:        CheckoutInvoice{Type: "mobile_carrier", Carrier: "/ABC+123"},
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
		`name="update" value="1"`,
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

// TestTheAddressBookIsOnlyOfferedForAnAddress holds that a saved street address
// is not offered for a convenience-store pickup, whose form has no such fields.
func TestTheAddressBookIsOnlyOfferedForAnAddress(t *testing.T) {
	t.Parallel()

	book := []SavedAddress{{ID: "a"}}
	for _, tt := range []struct {
		name        string
		destination string
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

// TestCanCancelIsAboutFundingNotJustStatus holds that a captured order still at
// pending is not offered cancel, which CancelOrderByCustomer would refuse.
func TestCanCancelIsAboutFundingNotJustStatus(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name      string
		status    string
		committed bool
		want      bool
	}{
		{"unpaid and not started", "pending", false, true},
		{"paid but not yet picked", "pending", true, false},
		{"being picked", "picking", false, false},
		{"already cancelled", "cancelled", false, false},
		{"shipped", "shipped", true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v := &OrderView{Status: tt.status, Committed: tt.committed}
			if got := v.CanCancel(); got != tt.want {
				t.Errorf("CanCancel() = %v, want %v", got, tt.want)
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

// TestTheOrderPageShowsTheDiscountAndWhy asserts the HTML, because the view
// model can hold every number correctly while the template renders one of them
// nowhere.
func TestTheOrderPageShowsTheDiscountAndWhy(t *testing.T) {
	v := &OrderView{
		Number: "GO-260101-000001", Status: "pending",
		SubtotalCents: 100000, ShippingCents: 6000,
		DiscountCents: 20000, DiscountReason: "SAVE200 · 滿額折抵",
		ShippingName: "宅配",
	}

	html := renderToString(t, Order(layouts.Page{Title: "訂單"}, v))
	for _, want := range []string{"SAVE200", "滿額折抵", "-NT$200"} {
		if !strings.Contains(html, want) {
			t.Errorf("the order summary does not mention %q", want)
		}
	}

	plain := &OrderView{
		Number: "GO-260101-000002", Status: "pending",
		SubtotalCents: 100000, ShippingCents: 6000, ShippingName: "宅配",
	}
	if got := renderToString(t, Order(layouts.Page{Title: "訂單"}, plain)); strings.Contains(got, "-NT$0") {
		t.Error("an order with no discount renders a zero discount row")
	}
}

// TestOnlyAnOrderThatOwesMoneyIsOfferedPayment holds the two funded cases apart:
// a captured card leaves the order committed and still owing, and a wholly
// store-credited one owes nothing and is not committed until it leaves pending.
func TestOnlyAnOrderThatOwesMoneyIsOfferedPayment(t *testing.T) {
	tests := []struct {
		name      string
		status    string
		committed bool
		owed      int64
		want      bool
	}{
		{name: "placed and unpaid", status: "pending", committed: false, owed: 106000, want: true},
		{name: "card captured, not yet picked", status: "pending", committed: true, owed: 106000, want: false},
		{name: "wholly paid from store credit", status: "pending", committed: false, owed: 0, want: false},
		{name: "cancelled", status: "cancelled", committed: false, owed: 106000, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &OrderView{
				Number: "GO-260101-000009", Status: tt.status,
				SubtotalCents: 100000, ShippingCents: 6000, ShippingName: "宅配",
				Committed: tt.committed, OwedCents: tt.owed,
			}
			html := renderToString(t, Order(layouts.Page{Title: "訂單"}, v))

			gotNotice := strings.Contains(html, "尚未付款")
			gotLink := strings.Contains(html, "/orders/GO-260101-000009/pay")
			if gotNotice != tt.want {
				t.Errorf("the 尚未付款 notice renders = %v, want %v", gotNotice, tt.want)
			}
			if gotLink != tt.want {
				t.Errorf("the payment link renders = %v, want %v", gotLink, tt.want)
			}
		})
	}
}

// TestAPaidOrderIsNotBadgedAwaitingPaymentInTheAccount holds the same fact on
// both signed-in surfaces: the history badge and the detail page's notice.
func TestAPaidOrderIsNotBadgedAwaitingPaymentInTheAccount(t *testing.T) {
	paid := AccountOrder{
		Number: "GO-260101-000010", Status: "pending", PlacedAt: "2026-01-01",
		TotalCents: 106000, LineCount: 1, Committed: true, OwedCents: 106000,
	}
	unpaid := AccountOrder{
		Number: "GO-260101-000011", Status: "pending", PlacedAt: "2026-01-01",
		TotalCents: 106000, LineCount: 1, Committed: false, OwedCents: 106000,
	}
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	if got := paid.StatusText(ctx); got != "付款完成" {
		t.Errorf("a captured order is badged %q in the history, want 付款完成", got)
	}
	if got := unpaid.StatusText(ctx); got != "待付款" {
		t.Errorf("an unpaid order is badged %q, want 待付款", got)
	}

	view := &AccountOrderView{
		Number: "GO-260101-000010", Status: "pending",
		SubtotalCents: 100000, ShippingCents: 6000, ShippingName: "宅配",
		Committed: true, OwedCents: 106000,
	}
	html := renderToString(t, AccountOrderPage(layouts.Page{Title: "訂單"}, view))
	if strings.Contains(html, "尚未付款") {
		t.Error("the account's own order page tells a paid customer their order is unpaid")
	}
	if got := view.StatusText(ctx); got != "付款完成" {
		t.Errorf("the detail page badges the order %q, want 付款完成 — the funding "+
			"fields did not travel into the AccountOrder literal", got)
	}
}

// TestTheOrderNotFoundPageOffersAWayThrough holds that this 404 offers both
// /orders/find and /signin, since the reader is a guest or an account holder
// and the page cannot tell which.
func TestTheOrderNotFoundPageOffersAWayThrough(t *testing.T) {
	html := renderToString(t, OrderNotFound(layouts.Page{Title: "404"}))

	for _, want := range []string{"/orders/find", "/signin"} {
		if !strings.Contains(html, want) {
			t.Errorf("the order 404 does not link to %s; the reader is one of two "+
				"people and the page cannot tell which", want)
		}
	}
}

// TestADeliveredOrderLinksToItsWarrantyForm holds that the order page carries
// the only link to the registration form, offered from delivery onward because
// cover starts when the goods reach somebody.
func TestADeliveredOrderLinksToItsWarrantyForm(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   bool
	}{
		{name: "pending", status: "pending", want: false},
		{name: "picking", status: "picking", want: false},
		{name: "shipped", status: "shipped", want: false},
		{name: "delivered", status: "delivered", want: true},
		// Convenience-store pickup moves shipped to completed with nobody at the
		// counter to witness a handover, so completed also ends a delivery.
		{name: "completed", status: "completed", want: true},
		{name: "cancelled", status: "cancelled", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &AccountOrderView{
				Number: "GO-260101-000012", Status: tt.status,
				SubtotalCents: 100000, ShippingCents: 6000, ShippingName: "宅配",
				Committed: true,
			}
			html := renderToString(t, AccountOrderPage(layouts.Page{Title: "訂單"}, v))

			got := strings.Contains(html, "/account/warranty/GO-260101-000012")
			if got != tt.want {
				t.Errorf("the order page links its warranty form = %v, want %v", got, tt.want)
			}
		})
	}
}

// renderToString runs a component and returns its HTML.
func renderToString(t *testing.T, c templ.Component) string {
	t.Helper()
	var b strings.Builder
	if err := c.Render(i18n.WithLocale(t.Context(), i18n.ZhHant), &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}

// TestANamelessReviewerIsNotBadgedAsABuyer holds the byline apart from the badge.
//
// A name is optional at registration and erase_user blanks it, so the fallback
// is the common case rather than the rare one: 23 of 24 seeded reviews had no
// name, and every one of them was unverified. The byline said 已購買的顧客 —
// borrowing the heading over the verified section — while the badge that carries
// the real claim sat two lines below, guarded by
// product_reviews_verified_is_real. The constraint cannot see a claim made in
// the author slot.
func TestANamelessReviewerIsNotBadgedAsABuyer(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	bought := i18n.T(ctx, i18n.KeyVerifiedBuyer)

	nameless := ProductReview{Author: "", Rating: 1}
	if got := nameless.DisplayAuthor(ctx); strings.Contains(got, bought) {
		t.Errorf("a nameless reviewer is bylined %q, which contains the verified "+
			"claim %q — the badge is what says somebody bought, and this review "+
			"has not", got, bought)
	}
	if named := (ProductReview{Author: "王小明"}).DisplayAuthor(ctx); named != "王小明" {
		t.Errorf("a named reviewer rendered as %q", named)
	}
}

// TestTheProductPageSaysWhetherItAddedAnything asserts the HTML, because the
// view model can carry the outcome correctly while the template renders it
// nowhere — which is exactly what happened.
//
// backToProduct has carried added/unavailable/unknown since it was written, and
// reservedParam listed "added" only to ignore it. So pressing 加入購物車
// re-rendered a page identical to the one before, and identical again when the
// variant had just sold out and nothing was added. The same template already
// shows an outcome for its other three writes.
func TestTheProductPageSaysWhetherItAddedAnything(t *testing.T) {
	base := func(outcome string) *ProductView {
		return &ProductView{
			Slug: "pixelight-9-pro", Name: "Pixelight 9 Pro",
			VariantID: "v1", PriceCents: 3690000, Available: 3,
			AddedOutcome: outcome,
		}
	}

	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	confirm := i18n.T(ctx, i18n.KeyAddedToCart)
	refusal := i18n.T(ctx, i18n.KeyAddRefused)

	// The exact words, not just role="status": the page carries other live
	// regions, so asserting the role alone stays green with this one deleted.
	added := renderToString(t, Product(layouts.Page{Title: "x"}, base("added")))
	if !strings.Contains(added, confirm) {
		t.Errorf("a successful add renders no confirmation; wanted %q", confirm)
	}

	refused := renderToString(t, Product(layouts.Page{Title: "x"}, base("unavailable")))
	if !strings.Contains(refused, refusal) {
		t.Errorf("a refused add renders no refusal; wanted %q", refusal)
	}
	if added == refused {
		t.Error("the success and the refusal render identically, which is the " +
			"defect: nothing added, and the page says the same thing either way")
	}

	// An ordinary visit shows neither.
	plain := renderToString(t, Product(layouts.Page{Title: "x"}, base("")))
	if strings.Contains(plain, confirm) || strings.Contains(plain, refusal) {
		t.Error("an ordinary page visit renders an add-to-cart message")
	}
}

// TestTheCheckoutSummaryNamesTheVariant holds the one page that dropped it.
//
// The cart names the variant beside each line, and so does the order page after
// the fact. Between them sits checkout, whose summary rendered "Name × qty" and
// nothing else — so at the moment of committing money, a customer buying the
// 星霧藍 512GB read the same line as one buying the 曜石黑 128GB, on the one
// screen built to confirm what they are about to pay for. The label was on the
// same struct the template was already ranging over.
func TestTheCheckoutSummaryNamesTheVariant(t *testing.T) {
	t.Parallel()

	view := CheckoutView{
		Cart: CartView{Lines: []CartLine{{
			Name: "Pixelight 9 Pro", Label: "星霧藍 / 512GB",
			Quantity: 1, UnitCents: 3190000,
		}}},
		Shipping: []ShippingChoice{{Code: "home", Name: "宅配到府", FeeCents: 8000}},
		Chosen:   "home",
	}
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Checkout(CheckoutMeta(ctx), &view))

	// The summary specifically, not the page: the line above the fold carries
	// the label too, so asserting the string against the whole document stays
	// green with the summary still saying nothing.
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
//
// 會員載具, 手機條碼載具 and 公司統編 need different information, and the page
// rendered BOTH the carrier field and the 統編 field whatever was chosen. So a
// customer taking the default read two fields neither of which applied to them,
// and one entering a 統編 was shown a carrier box the validator blanks — the
// server demanding one thing while the form offered two.
//
// The choice is a link for the reason the delivery method is: it decides which
// field the form asks for, so a chooser only a script could act on would leave
// the two disagreeing. Validate() already blanks the field that does not apply;
// this is the page catching up with it.
func TestTheInvoiceFormAsksForOneThing(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	tests := []struct {
		name        string
		kind        string
		wantCarrier bool
		wantTaxID   bool
	}{
		{name: "the default keeps neither", kind: "", wantCarrier: false, wantTaxID: false},
		{name: "a mobile barcode needs the carrier", kind: "mobile_carrier", wantCarrier: true},
		{name: "a company invoice needs the 統編", kind: "company", wantTaxID: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			view := CheckoutView{
				Cart:           CartView{Lines: []CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
				Shipping:       []ShippingChoice{{Code: "home", Name: "宅配到府"}},
				Invoice:        CheckoutInvoice{Type: tt.kind},
				InvoiceChoices: []InvoiceChoice{{Value: "member_carrier", Label: "會員載具"}},
			}
			html := renderToString(t, Checkout(CheckoutMeta(ctx), &view))
			if got := strings.Contains(html, `id="invoice_carrier"`); got != tt.wantCarrier {
				t.Errorf("invoice=%q renders the carrier field = %v, want %v", tt.kind, got, tt.wantCarrier)
			}
			if got := strings.Contains(html, `id="invoice_tax_id"`); got != tt.wantTaxID {
				t.Errorf("invoice=%q renders the 統編 field = %v, want %v", tt.kind, got, tt.wantTaxID)
			}
		})
	}

	// The three choosers compose because they are three radio groups in ONE
	// form, so a submission carries all of them. They used to be links, and
	// composing meant every link rebuilding the other two parameters — which
	// worked, and discarded everything the customer had typed.
	full := CheckoutView{
		Cart:           CartView{Lines: []CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping:       []ShippingChoice{{VersionID: "ship-1", Code: "home", Name: "宅配到府"}},
		Chosen:         "ship-1",
		Invoice:        CheckoutInvoice{Type: "company"},
		InvoiceChoices: []InvoiceChoice{{Value: "company", Label: "公司統編"}},
	}
	html := renderToString(t, Checkout(CheckoutMeta(ctx), &full))
	for _, want := range []string{
		`type="radio" name="shipping"`,
		`type="radio" name="invoice_type"`,
		`name="update" value="1"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the checkout does not carry %s — a chooser outside the form "+
				"cannot preserve what has been typed into it", want)
		}
	}
}

// TestAnOffshoreRequoteIsNotAnError holds a legitimate outcome that was dressed
// as a mistake.
//
// The delivery method is chosen above the address, so the fee on screen is a
// mainland estimate — there is no postal code yet. A parcel to 金門 is priced
// on the first submission that carries one, and the customer is shown the real
// figure before being charged it. That is the design, and nothing they typed
// was wrong.
//
// It rendered as ui-error-text under the shipping section, beside "enter the
// recipient's name" — so somebody who had filled the form correctly was told
// they had made a mistake and left looking for it. It is a notice now, and
// still a 422: the submission was not accepted, which is the half that must
// not change.
func TestAnOffshoreRequoteIsNotAnError(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	view := CheckoutView{
		Cart:     CartView{Lines: []CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping: []ShippingChoice{{Code: "home", Name: "宅配到府"}},
		Repriced: "離島運費另計,已更新為 NT$280。確認後再送出一次。",
	}
	html := renderToString(t, Checkout(CheckoutMeta(ctx), &view))

	if !strings.Contains(html, view.Repriced) {
		t.Fatal("the re-quote is not shown at all")
	}
	// The element carrying it, not merely the words: the whole point is which
	// one, and the string appears the same either way.
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

// TestAShortLineSaysWhatItIsPricedFor holds arithmetic a customer can check.
//
// A line the shelf cannot meet is priced for what CAN be supplied — the store
// and the view agree on that, and the subtotal is right. What the page showed
// was the quantity box holding 5 beside a figure for 2, so the multiplication
// anybody does in their head disagreed with the total next to it, on the page
// where they are deciding whether the number is correct.
func TestAShortLineSaysWhatItIsPricedFor(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	short := CartLine{
		VariantID: "v1", Slug: "x", Name: "Pixelight 9 Pro",
		UnitCents: 100000, Quantity: 5, Available: 2, Short: true,
	}
	if got, want := short.LineTotal(), "NT$2,000"; got != want {
		t.Errorf("LineTotal() = %q, want %q — the price is for what can be supplied", got, want)
	}

	html := renderToString(t, Cart(CartMeta(ctx), CartView{Lines: []CartLine{short}}))
	said := fmt.Sprintf(i18n.T(ctx, i18n.KeyPricedFor), "2")
	if !strings.Contains(html, said) {
		t.Errorf("a line asking for 5 and priced for 2 does not say so; wanted %q", said)
	}

	// A line the shelf can meet says nothing: "priced for 5" beside "× 5" is
	// noise on every ordinary row in the cart.
	ok := CartLine{VariantID: "v2", Slug: "y", Name: "y", UnitCents: 100000, Quantity: 2, Available: 9}
	full := renderToString(t, Cart(CartMeta(ctx), CartView{Lines: []CartLine{ok}}))
	if strings.Contains(full, i18n.T(ctx, i18n.KeyPricedFor)[:3]) {
		t.Error("an ordinary line is annotated with what it is priced for")
	}
}
