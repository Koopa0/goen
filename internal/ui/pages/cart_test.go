package pages

import (
	"fmt"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/google/go-cmp/cmp"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/pickup"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestPickupBrandChoicesMatchValidationAndReturnFreshStorage(t *testing.T) {
	want := []PickupBrandChoice{
		{Value: "seven_eleven", Label: "7-ELEVEN"},
		{Value: "family_mart", Label: "全家 FamilyMart"},
		{Value: "hi_life", Label: "萊爾富 Hi-Life"},
		{Value: "ok_mart", Label: "OK mart"},
	}
	got := PickupBrandChoices()
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("PickupBrandChoices() mismatch (-want +got):\n%s", diff)
	}
	for _, choice := range got {
		if !choice.Value.Known() {
			t.Errorf("PickupBrandChoices() offers %q, but KnownPickupBrand rejects it", choice.Value)
		}
	}

	got[0].Value = "other_chain"
	got[0].Label = "Other"
	if diff := cmp.Diff(want, PickupBrandChoices()); diff != "" {
		t.Errorf("mutating PickupBrandChoices() changed the next result (-want +got):\n%s", diff)
	}
	if pickup.Brand("other_chain").Known() {
		t.Error("mutating PickupBrandChoices() changed KnownPickupBrand")
	}
}

// TestEveryCheckoutChoiceSurvivesChangingAnother holds that the choosers are
// radio groups inside the checkout form, so changing one keeps what was typed
// into the others and still works with scripting off.
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
		status    FulfillmentStatus
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
		status    FulfillmentStatus
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

// TestAccountOrderHistoryLinksToCanonicalOrderPage holds that the account
// overview sends readers to /orders/{number}, not a second detail template.
func TestAccountOrderHistoryLinksToCanonicalOrderPage(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := &AccountView{
		Orders: []AccountOrder{{
			Number: "GO-260101-000012", Status: FulfillmentDelivered,
			PlacedAt: "2026-01-01", TotalCents: 106000, LineCount: 1,
		}},
	}
	html := renderToString(t, Account(AccountMeta(ctx), view))
	if !strings.Contains(html, `href="/orders/GO-260101-000012"`) {
		t.Error("the order history does not link to the canonical order page")
	}
	if strings.Contains(html, "/account/orders/") {
		t.Error("the order history still links to the retired account order page")
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

	view := &OrderView{
		Number: "GO-260101-000010", Status: FulfillmentPending,
		SubtotalCents: 100000, ShippingCents: 6000, ShippingName: "宅配",
		Committed: true, OwedCents: 106000,
	}
	html := renderToString(t, Order(layouts.Page{Title: "訂單"}, view))
	if strings.Contains(html, "尚未付款") {
		t.Error("the canonical order page tells a paid customer their order is unpaid")
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

// TestADeliveredOrderOffersReturnAndShowsCredit holds that the canonical order
// page carries return and store-credit summary for a delivered order.
func TestADeliveredOrderOffersReturnAndShowsCredit(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	v := &OrderView{
		Number: "GO-260101-000012", Status: FulfillmentDelivered,
		SubtotalCents: 100000, ShippingCents: 6000, ShippingName: "宅配",
		CreditCents: 50000,
	}
	html := renderToString(t, Order(layouts.Page{Title: "訂單"}, v))
	if !strings.Contains(html, "/orders/GO-260101-000012/return") {
		t.Error("a delivered order does not link to its return form")
	}
	if !strings.Contains(html, i18n.T(ctx, i18n.KeyOrderCreditApplied)) {
		t.Error("a store-credited order does not show the credit row in its summary")
	}
	if !strings.Contains(html, "-NT$500") {
		t.Error("the credit row does not show how much store credit was applied")
	}
}

// TestADeliveredOrderLinksToItsWarrantyForm holds that the order page carries
// the only link to the registration form, offered from delivery onward because
// cover starts when the goods reach somebody.
func TestADeliveredOrderLinksToItsWarrantyForm(t *testing.T) {
	tests := []struct {
		name   string
		status FulfillmentStatus
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
			v := &OrderView{
				Number: "GO-260101-000012", Status: tt.status,
				SubtotalCents: 100000, ShippingCents: 6000, ShippingName: "宅配",
				Committed: true, ShowWarrantyLink: true,
			}
			html := renderToString(t, Order(layouts.Page{Title: "訂單"}, v))

			got := strings.Contains(html, "/account/warranty/GO-260101-000012")
			if got != tt.want {
				t.Errorf("the order page links its warranty form = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestGuestTokenViewerDoesNotSeeWarrantyLink holds that a delivered order read
// through a guest access token must not expose the account-only registration door.
func TestGuestTokenViewerDoesNotSeeWarrantyLink(t *testing.T) {
	v := &OrderView{
		Number: "GO-260101-000012", Status: FulfillmentDelivered,
		SubtotalCents: 100000, ShippingCents: 6000, ShippingName: "宅配",
		ShowWarrantyLink: false,
	}
	html := renderToString(t, Order(layouts.Page{Title: "訂單"}, v))
	if strings.Contains(html, "/account/warranty/GO-260101-000012") {
		t.Error("a guest-token viewer sees the account warranty registration link")
	}
}

func renderToString(t *testing.T, c templ.Component) string {
	t.Helper()
	var b strings.Builder
	if err := c.Render(i18n.WithLocale(t.Context(), i18n.ZhHant), &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}

// TestANamelessReviewerIsNotBadgedAsABuyer holds the byline apart from the
// badge. A name is optional at registration and erase_user blanks it, so the
// fallback byline must not borrow the verified-buyer wording:
// product_reviews_verified_is_real guards the badge and cannot see a claim
// made in the author slot.
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
// nowhere.
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
	// regions.
	added := renderToString(t, Product(layouts.Page{Title: "x"}, base("added")))
	if !strings.Contains(added, confirm) {
		t.Errorf("a successful add renders no confirmation; wanted %q", confirm)
	}
	viewCart := i18n.T(ctx, i18n.KeyViewCart)
	if !strings.Contains(added, viewCart) || !strings.Contains(added, `href="/cart"`) {
		t.Errorf("a successful add renders no cart link; wanted %q beside /cart", viewCart)
	}
	if !strings.Contains(added, `role="status"`) {
		t.Error("the success confirmation is not exposed as a live status")
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

// TestTheCheckoutSummaryNamesTheVariant holds that the summary names the
// variant, on the one screen built to confirm what is about to be paid for.
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
// 會員載具, 手機條碼載具 and 公司統編 need different information, and Validate()
// blanks the field that does not apply, so rendering both would leave the form
// promising something the server will not demand.
func TestTheInvoiceFormAsksForOneThing(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	tests := []struct {
		name            string
		kind            invoice.Preference
		wantCarrier     bool
		wantCompanyName bool
		wantTaxID       bool
	}{
		{name: "the default keeps neither", kind: "", wantCarrier: false, wantTaxID: false},
		{name: "a mobile barcode needs the carrier", kind: "mobile_carrier", wantCarrier: true},
		{name: "a company invoice needs its registered buyer", kind: "company", wantCompanyName: true, wantTaxID: true},
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
			if got := strings.Contains(html, `id="invoice_company_name"`); got != tt.wantCompanyName {
				t.Errorf("invoice=%q renders the company-name field = %v, want %v",
					tt.kind, got, tt.wantCompanyName)
			}
		})
	}

	// The three choosers compose because they are three radio groups in ONE
	// form, so a submission carries all of them.
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
		Cart:     CartView{Lines: []CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
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
		Cart:          CartView{Lines: []CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
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

// TestEnterInTheCheckoutPlacesTheOrder holds a rule the browser applies and no
// linter can see: HTML makes the FIRST submit button in tree order the one
// Enter activates in any text field, so a leading submit must place the order
// rather than a chooser's 更新 re-rendering the form. Asserting ORDER is the
// whole point: both buttons exist either way.
func TestEnterInTheCheckoutPlacesTheOrder(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	view := CheckoutView{
		Cart:           CartView{Lines: []CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
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

// TestAFullyFundedOrderIsNotAskedToPay covers the funding term of
// AwaitingPayment. An order paid entirely from store credit, or zeroed by a
// 100% coupon, is legitimately 'pending' with NO payment row, so status and
// Committed together still read "unpaid" and only OwedCents tells them apart.
func TestAFullyFundedOrderIsNotAskedToPay(t *testing.T) {
	t.Parallel()

	funded := &OrderView{
		Number: "GO-260101-000012", Status: FulfillmentPending,
		SubtotalCents: 100000, ShippingCents: 6000, ShippingName: "宅配",
		// Not committed and nothing owed: paid in full from store credit.
		Committed: false, OwedCents: 0,
	}
	if funded.AwaitingPayment() {
		t.Error("an order that owes nothing is asked to pay; the pay link it " +
			"renders goes to a Stripe session that cannot be created")
	}

	html := renderToString(t, Order(layouts.Page{Title: "訂單"}, funded))
	if strings.Contains(html, "/orders/"+funded.Number+"/pay") {
		t.Error("the order page offers a payment link for an order that owes nothing")
	}

	// The control: same order, same status, same Committed, money still owed.
	owing := &OrderView{
		Number: "GO-260101-000013", Status: FulfillmentPending,
		SubtotalCents: 100000, ShippingCents: 6000, ShippingName: "宅配",
		Committed: false, OwedCents: 106000,
	}
	if !owing.AwaitingPayment() {
		t.Error("an order that still owes money is not offered a way to pay it")
	}
}
