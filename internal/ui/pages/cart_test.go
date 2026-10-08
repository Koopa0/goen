package pages

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"
	"github.com/google/go-cmp/cmp"
	htmlparse "golang.org/x/net/html"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/carrier"
	"github.com/koopa0/goen/internal/destination"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/pickup"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
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
					v := CartView{Lines: []CartLine{{VariantID: "item", Name: "Item", UnitCents: 100, Quantity: 1, Available: 2}}, SubtotalCents: 100, ItemCount: 1, NoDelivery: tt.noDelivery}
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

func TestPickupChainChoicesMatchValidationAndReturnFreshStorage(t *testing.T) {
	want := []PickupChainChoice{
		{Value: "seven_eleven", Label: "7-ELEVEN"},
		{Value: "family_mart", Label: "全家 FamilyMart"},
		{Value: "hi_life", Label: "萊爾富 Hi-Life"},
		{Value: "ok_mart", Label: "OK mart"},
	}
	got := PickupChainChoices()
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("PickupChainChoices() mismatch (-want +got):\n%s", diff)
	}
	for _, choice := range got {
		if !choice.Value.Known() {
			t.Errorf("PickupChainChoices() offers %q, but KnownPickupChain rejects it", choice.Value)
		}
	}

	got[0].Value = "other_chain"
	got[0].Label = "Other"
	if diff := cmp.Diff(want, PickupChainChoices()); diff != "" {
		t.Errorf("mutating PickupChainChoices() changed the next result (-want +got):\n%s", diff)
	}
	if pickup.Chain("other_chain").Known() {
		t.Error("mutating PickupChainChoices() changed KnownPickupChain")
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
		Cart:           CartView{Lines: []CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
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
	v := CartView{Lines: []CartLine{{VariantID: "item", Name: "Item", UnitCents: 100, Quantity: 1, Available: 2}}, SubtotalCents: 100, ItemCount: 1}
	body := renderComponent(t, ctx, Cart(CartMeta(ctx), v))
	_, summary, ok := strings.Cut(body, `id="cart-summary"`)
	if !ok {
		t.Fatal("the cart has no summary")
	}
	shipping := strings.Index(summary, "<dt>"+i18n.T(ctx, i18n.KeyShippingFee)+"</dt>")
	total := strings.Index(summary, `goen-summary__row--total"><dt>`+i18n.T(ctx, i18n.KeySubtotal)+"</dt>")
	if shipping < 0 || total < 0 {
		t.Fatalf("the summary lacks a row: shipping at %d, total at %d", shipping, total)
	}
	if total < shipping {
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
		Cart:           CartView{Lines: []CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
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
		Cart:         CartView{Lines: []CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
		Shipping:     []ShippingChoice{{VersionID: "ship-1", Code: "pickup", Name: "超商取貨"}},
		Chosen:       "ship-1",
		Destination:  "pickup_point",
		PickupChains: CheckoutPickupChainChoices(),
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

// TestCheckoutOffersTheChainsTheShopShipsTo holds the two sets apart: a shopper
// chooses between the chains whose store picker the shop will integrate, and the
// back office keeps every chain, because an order already placed at one of the
// others still has to be correctable.
func TestCheckoutOffersTheChainsTheShopShipsTo(t *testing.T) {
	t.Parallel()

	want := []PickupChainChoice{
		{Value: pickup.SevenEleven, Label: "7-ELEVEN"},
		{Value: pickup.FamilyMart, Label: "全家 FamilyMart"},
	}
	got := CheckoutPickupChainChoices()
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("CheckoutPickupChainChoices() mismatch (-want +got):\n%s", diff)
	}
	for _, choice := range got {
		if !choice.Value.Known() {
			t.Errorf("checkout offers %q, which validation rejects", choice.Value)
		}
	}
	if len(PickupChainChoices()) != len(pickup.Offered()) {
		t.Error("the back office no longer offers every chain the shop can accept")
	}
}

// TestAPickupOrderIsNamedByItsChain holds what the confirmation and the back
// office show while no store picker exists: the chain is the destination, and a
// store appears only when one is known.
func TestAPickupOrderIsNamedByItsChain(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		d      Delivery
		pickup bool
		want   string
	}{
		{
			name: "the chain alone", pickup: true,
			d:    Delivery{PickupChain: pickup.SevenEleven},
			want: "7-ELEVEN",
		},
		{
			name: "a chain whose store is known", pickup: true,
			d: Delivery{
				PickupChain: pickup.FamilyMart, PickupStoreCode: "012345",
				PickupStoreName: "台北車站門市",
			},
			want: "全家 FamilyMart 台北車站門市(012345)",
		},
		{
			name: "an address",
			d: Delivery{
				PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號",
			},
			want: "110 台北市信義區松高路 1 號",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.d.IsPickup(); got != tt.pickup {
				t.Errorf("IsPickup() = %v, want %v", got, tt.pickup)
			}
			if got := tt.d.Line(); got != tt.want {
				t.Errorf("Line() = %q, want %q", got, tt.want)
			}
		})
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
		destination destination.Kind
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
		status    order.FulfillmentStatus
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

// TestACreditPaidOrdersCancelSaysItVoidsTheInvoice: the customer's own press is
// the consent to voiding the 統一發票, so the form says so in both languages,
// and only where an invoice was owed.
func TestACreditPaidOrdersCancelSaysItVoidsTheInvoice(t *testing.T) {
	t.Parallel()
	viewOf := func(creditCents, owedCents int64) *OrderView {
		return &OrderView{
			Number: "GO-260101-000003", Status: "pending", ShippingName: "宅配",
			SubtotalCents: 100000, ShippingCents: 6000,
			CreditCents: creditCents, OwedCents: owedCents,
		}
	}
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		voids := templ.EscapeString(i18n.T(ctx, i18n.KeyOrderCancelVoidsInvoice))
		if got := renderIn(t, locale, Order(layouts.Page{Title: "order"}, viewOf(106000, 0))); !strings.Contains(got, voids) {
			t.Errorf("%s: the cancel form of a credit-paid order does not say the invoice is voided", locale)
		}
		// Unpaid, part paid by credit, and free: none was owed an invoice.
		for _, uninvoiced := range []*OrderView{viewOf(0, 106000), viewOf(6000, 100000), viewOf(0, 0)} {
			if got := renderIn(t, locale, Order(layouts.Page{Title: "order"}, uninvoiced)); strings.Contains(got, voids) {
				t.Errorf("%s: an order with %d credit owing %d says cancelling voids an invoice it never had",
					locale, uninvoiced.CreditCents, uninvoiced.OwedCents)
			}
		}
	}
}

// TestOnlyAnOrderThatOwesMoneyIsOfferedPayment holds the two funded cases apart:
// a captured card leaves the order committed and still owing, and a wholly
// store-credited one owes nothing and is not committed until it leaves pending.
func TestOnlyAnOrderThatOwesMoneyIsOfferedPayment(t *testing.T) {
	tests := []struct {
		name      string
		status    order.FulfillmentStatus
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
				Committed: tt.committed, OwedCents: tt.owed, PaymentsEnabled: true,
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
			Number: "GO-260101-000012", Status: order.FulfillmentDelivered,
			PlacedAt: shoptime.Date{Year: 2026, Month: time.January, Day: 1}, TotalCents: 106000, LineCount: 1,
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
		Number: "GO-260101-000010", Status: "pending", PlacedAt: shoptime.Date{Year: 2026, Month: time.January, Day: 1},
		TotalCents: 106000, LineCount: 1, Committed: true, OwedCents: 106000,
	}
	unpaid := AccountOrder{
		Number: "GO-260101-000011", Status: "pending", PlacedAt: shoptime.Date{Year: 2026, Month: time.January, Day: 1},
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
		Number: "GO-260101-000010", Status: order.FulfillmentPending,
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
		Number: "GO-260101-000012", Status: order.FulfillmentDelivered,
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

// TestADeliveredOrderLinksToItsWarrantyForm holds that a line links to the registration form from the day its
// parcel arrives, because cover starts when the goods reach somebody, and not before.
func TestADeliveredOrderLinksToItsWarrantyForm(t *testing.T) {
	line := OrderLine{SKU: "S1", Name: "耳機", UnitCents: 100000, Quantity: 1, WarrantyMonths: 12}
	tests := []struct {
		name      string
		status    order.FulfillmentStatus
		delivered bool
		want      bool
	}{
		{name: "pending", status: "pending", want: false},
		{name: "picking", status: "picking", want: false},
		{name: "shipped", status: "shipped", want: false},
		{name: "delivered", status: "delivered", delivered: true, want: true},
		// Convenience-store pickup moves shipped to completed with nobody at the
		// counter to witness a handover, so completed also ends a delivery.
		{name: "completed", status: "completed", delivered: true, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &OrderView{
				Number: "GO-260101-000012", Status: tt.status,
				SubtotalCents: 100000, ShippingCents: 6000, ShippingName: "宅配",
				Committed: true, ShowWarrantyLink: true, Lines: []OrderLine{line},
			}
			if tt.status != "pending" && tt.status != "picking" {
				parcel := OrderShipment{Carrier: carrier.BlackCat, Tracking: "T1", ShippedAt: orderNow.AddDate(0, 0, -4), Lines: []OrderLine{line}}
				if tt.delivered {
					parcel.DeliveredAt, parcel.RescissionEnds, parcel.GoodwillEnds = orderNow.AddDate(0, 0, -3), orderDay(13), orderDay(20)
				}
				v.Shipments = []OrderShipment{parcel}
			}
			v.Now = orderNow
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
	line := OrderLine{SKU: "S1", Name: "耳機", UnitCents: 100000, Quantity: 1, WarrantyMonths: 12}
	v := &OrderView{
		Number: "GO-260101-000012", Status: order.FulfillmentDelivered,
		SubtotalCents: 100000, ShippingCents: 6000, ShippingName: "宅配",
		ShowWarrantyLink: false, Now: orderNow, Lines: []OrderLine{line},
		Shipments: []OrderShipment{{
			Carrier: carrier.BlackCat, Tracking: "T1", ShippedAt: orderNow.AddDate(0, 0, -4), DeliveredAt: orderNow.AddDate(0, 0, -3),
			RescissionEnds: orderDay(13), GoodwillEnds: orderDay(20), Lines: []OrderLine{line},
		}},
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
}

func TestAReviewerIsMaskedTheSameWayForEveryReview(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		locale i18n.Locale
		author string
		want   string
	}{
		{"zh full name", i18n.ZhHant, "王小明", "王○○"},
		{"zh one character", i18n.ZhHant, "王", "王○○"},
		{"zh latin name", i18n.ZhHant, "alice Chen", "a○○"},
		{"en full name", i18n.En, "Alice Chen", "A."},
		{"en lower case", i18n.En, "bob", "B."},
		{"en han name", i18n.En, "王小明", "王."},
		{"padded", i18n.ZhHant, "  陳大文", "陳○○"},
		{"leading replacement character zh", i18n.ZhHant, "\uFFFD Example Person", "\uFFFD○○"},
		{"leading replacement character en", i18n.En, "\uFFFD Example Person", "\uFFFD."},
		{"blank", i18n.ZhHant, "   ", "○○"},
		{"no name zh", i18n.ZhHant, "", "匿名顧客"},
		{"no name en", i18n.En, "", "Anonymous"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := (ProductReview{Author: tt.author}).DisplayAuthor(i18n.WithLocale(t.Context(), tt.locale))
			if got != tt.want {
				t.Errorf("DisplayAuthor(%q) in %s = %q, want %q", tt.author, tt.locale, got, tt.want)
			}
		})
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
			AddedOutcome: AddOutcome(outcome),
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
				Cart:           CartView{Lines: []CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
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
		Cart: CartView{Lines: []CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
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
		Cart:     CartView{Lines: []CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
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

// TestAFullyFundedOrderIsNotAskedToPay covers the funding term of
// AwaitingPayment. An order paid entirely from store credit, or zeroed by a
// 100% coupon, is legitimately 'pending' with NO payment row, so status and
// Committed together still read "unpaid" and only OwedCents tells them apart.
func TestAFullyFundedOrderIsNotAskedToPay(t *testing.T) {
	t.Parallel()

	funded := &OrderView{
		Number: "GO-260101-000012", Status: order.FulfillmentPending,
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
		Number: "GO-260101-000013", Status: order.FulfillmentPending,
		SubtotalCents: 100000, ShippingCents: 6000, ShippingName: "宅配",
		Committed: false, OwedCents: 106000,
	}
	if !owing.AwaitingPayment() {
		t.Error("an order that still owes money is not offered a way to pay it")
	}
}

// TestCartLineLoadsTheSmallPhoto holds that a 96px thumbnail offers the -400
// derivative through the same srcset the product cards use, and is told its
// slot is 96px wide so the browser does not fall back to the 1600px source.
func TestCartLineLoadsTheSmallPhoto(t *testing.T) {
	t.Parallel()
	const key = "nimbus-buds-pro-01.webp"
	line := CartLine{
		Slug: "buds", Name: "Buds", Quantity: 1, UnitCents: 100,
		ImageURL: assets.ProductImageURL(key), ImageSrcset: assets.ProductImageSrcsetAt(key, 1600), ImageAlt: "Buds",
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
	html := renderToString(t, cartLine(CartLine{Slug: "buds", Name: "Buds", Quantity: 1, UnitCents: 100}))
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
	html := renderToString(t, cartLine(CartLine{VariantID: id, Slug: "buds", Name: "Buds", Quantity: 1, UnitCents: 100}))
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
		Cart:     CartView{Lines: []CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
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
		Cart:     CartView{Lines: []CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
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

func TestQuestionsAndAnswersMaskNamesLikeReviews(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	if got := (Question{Asker: "王小明"}).Who(ctx); got != "王○○" {
		t.Errorf("asker shown as %q, want 王○○", got)
	}
	if got := (Answer{Author: "陳大文"}).Who(ctx); got != "陳○○" {
		t.Errorf("answerer shown as %q, want 陳○○", got)
	}
	if got := (Question{Asker: "\uFFFD Example Person"}).Who(ctx); strings.Contains(got, "Example") {
		t.Errorf("asker shown as %q, which carries the rest of the name", got)
	}
	if got := (Answer{Author: "\uFFFD Example Person"}).Who(ctx); strings.Contains(got, "Example") {
		t.Errorf("answerer shown as %q, which carries the rest of the name", got)
	}
	if got := (Answer{Author: "陳大文", IsStaff: true}).Who(ctx); got != "goen" {
		t.Errorf("staff answer shown as %q, want goen", got)
	}
}

func couponTestView() CheckoutView {
	return CheckoutView{
		Cart:     CartView{Lines: []CartLine{{Name: "x", Quantity: 1, UnitCents: 100}}},
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

	src, err := os.ReadFile(filepath.Join("..", "..", "..", "assets", "js", "goen.js"))
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

// TestWithoutPaymentsTheOrderPageLeadsNowhereNearThePayPage holds the other half
// of a loop: the pay page of a shop that takes no payment links to the order, so
// the order must not link back, and the pay page must still offer a way out.
func TestWithoutPaymentsTheOrderPageLeadsNowhereNearThePayPage(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	unpaid := &OrderView{
		Number: "GO-260101-000009", Status: order.FulfillmentPending,
		SubtotalCents: 100000, ShippingCents: 6000, ShippingName: "宅配", OwedCents: 106000,
	}
	html := renderToString(t, Order(layouts.Page{Title: "訂單"}, unpaid))
	if strings.Contains(html, "/orders/GO-260101-000009/pay") {
		t.Error("the order page links to a payment page the shop cannot serve")
	}
	if !strings.Contains(html, "尚未付款") {
		t.Error("the order page no longer says the order is unpaid")
	}

	pay := renderToString(t, Pay(PayMeta(ctx, "GO-260101-000009"), PayView{Number: "GO-260101-000009"}))
	if !strings.Contains(pay, `href="/orders/GO-260101-000009"`) || !strings.Contains(pay, `href="/contact"`) {
		t.Errorf("the pay page of a shop with no payments offers no way out:\n%s", pay)
	}
}

// TestTheOrderPageStatesItsOwnState holds the eyebrow, the payment row and the
// amount due, each from the order's own facts.
func TestTheOrderPageStatesItsOwnState(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	render := func(v *OrderView) string {
		v.Number = "GO-260101-000020"
		return renderToString(t, Order(layouts.Page{Title: "訂單"}, v))
	}

	delivered := render(&OrderView{Status: order.FulfillmentDelivered, Committed: true, SubtotalCents: 100000, OwedCents: 100000})
	if !strings.Contains(delivered, `goen-pagehead__eyebrow">已送達<`) {
		t.Error("a delivered order's eyebrow does not say it was delivered")
	}
	if !strings.Contains(delivered, "付款狀態") || !strings.Contains(delivered, "付款完成") {
		t.Error("a delivered order does not say it is paid")
	}

	for _, tt := range []struct {
		name string
		view OrderView
		want PaymentState
	}{
		{"unpaid", OrderView{Status: order.FulfillmentPending, OwedCents: 100}, PaymentAwaiting},
		{"captured", OrderView{Status: order.FulfillmentPending, Committed: true, OwedCents: 100}, PaymentPaid},
		{"funded by credit", OrderView{Status: order.FulfillmentPending}, PaymentPaid},
		{"cancelled before paying", OrderView{Status: order.FulfillmentCancelled, OwedCents: 100}, PaymentNone},
		{"cancelled and refunded", OrderView{
			Status: order.FulfillmentCancelled, Timeline: []OrderEvent{{Kind: "cancelled"}, {Kind: "refunded"}},
		}, PaymentRefunded},
		{"delivered with a partial refund", OrderView{
			Status: order.FulfillmentDelivered, Committed: true, Timeline: []OrderEvent{{Kind: "refunded"}},
		}, PaymentPaid},
	} {
		if got := tt.view.PaymentState(); got != tt.want {
			t.Errorf("%s: payment state = %q, want %q", tt.name, got, tt.want)
		}
	}

	credited := render(&OrderView{
		Status: order.FulfillmentPending, SubtotalCents: 100000, ShippingCents: 6000, CreditCents: 106000, OwedCents: 0,
	})
	credit := strings.Index(credited, i18n.T(ctx, i18n.KeyOrderCreditApplied))
	due := strings.Index(credited, i18n.T(ctx, i18n.KeyOrderAmountDue))
	if credit < 0 || due < 0 || credit > due {
		t.Error("the store credit does not sit above the amount due")
	}
	if !strings.Contains(credited[due:], "NT$0") {
		t.Error("a fully credited order does not say NT$0 is due")
	}
}

// TestTheAccountListOffersToPayAnUnpaidOrderWhereThatCanBeDone holds the 付款
// action: only for an order that owes, only where payment is possible.
func TestTheAccountListOffersToPayAnUnpaidOrderWhereThatCanBeDone(t *testing.T) {
	t.Parallel()
	view := func(enabled bool) *AccountView {
		return &AccountView{
			PaymentsEnabled: enabled,
			Orders: []AccountOrder{
				{Number: "GO-260101-000011", Status: order.FulfillmentPending, OwedCents: 100},
				{Number: "GO-260101-000010", Status: order.FulfillmentPending, Committed: true, OwedCents: 100},
			},
		}
	}
	on := renderToString(t, Account(AccountMeta(t.Context()), view(true)))
	if !strings.Contains(on, `href="/orders/GO-260101-000011/pay"`) {
		t.Error("an unpaid order has no 付款 action")
	}
	if strings.Contains(on, `href="/orders/GO-260101-000010/pay"`) {
		t.Error("a paid order is offered payment")
	}
	if off := renderToString(t, Account(AccountMeta(t.Context()), view(false))); strings.Contains(off, "/orders/GO-260101-000011/pay") {
		t.Error("an order is offered payment where the shop takes none")
	}
}

// TestTheCartRecoveryPageGoesHome holds 回到商店 to its word: it was the page
// the shopper was trying to reach, which is this page's own failure.
func TestTheCartRecoveryPageGoesHome(t *testing.T) {
	t.Parallel()
	html := renderToString(t, CartRecovery(CartRecoveryMeta(t.Context()), CartRecoveryView{Next: "/account", Notice: "n", Retry: "r"}))
	back := tagCarrying(t, html, "notice__back")
	if !strings.Contains(back, `href="/"`) {
		t.Errorf("回到商店 does not go to the home page: %s", back)
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
	lines := []CartLine{{VariantID: "v", Slug: "s", Name: "x", Quantity: 2, UnitCents: 40000}}
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
		Lines: []CartLine{
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
	line := CartLine{VariantID: "v", Slug: "s", Name: "x", Quantity: 1, UnitCents: 100}
	soldOut, short := line, line
	soldOut.Unavailable = true
	short.Short = true

	for _, tt := range []struct {
		name  string
		lines []CartLine
		want  string
	}{
		{"sold out", []CartLine{soldOut}, "有商品已售完，移除後才能結帳。"},
		{"short", []CartLine{short}, "有商品的庫存不足，請先調整數量再結帳。"},
		{"both: the sold-out line is the one to deal with first", []CartLine{short, soldOut}, "有商品已售完，移除後才能結帳。"},
		{"neither", []CartLine{line}, ""},
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
	lines := []CartLine{{VariantID: "v", Slug: "s", Name: "x", Quantity: 1, UnitCents: 100}}
	html := renderToString(t, Cart(CartMeta(ctx), CartView{Lines: lines}))
	if !strings.Contains(html, "庫存保留 60 分鐘") {
		t.Error("the cart does not say how long the stock is held")
	}
}

// TestRemovingALineIsNotBlockedByAQuantityTheShelfCannotMeet: the remove button
// shares a form with the quantity field, whose max is the stock, so a number
// typed above it would stop the removal behind a native validation bubble.
func TestRemovingALineIsNotBlockedByAQuantityTheShelfCannotMeet(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	line := CartLine{VariantID: "v1", Slug: "x", Name: "x", UnitCents: 100000, Quantity: 2, Available: 4}
	html := renderToString(t, Cart(CartMeta(ctx), CartView{Lines: []CartLine{line}}))
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

func TestShowCancelKeepsTheOrderFundingAndHoldFacts(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.October, 7, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name string
		view OrderView
		can  bool
		show bool
	}{
		{name: "unresolved return", view: OrderView{Status: order.FulfillmentPending, OwedCents: 100, PaymentReturnHint: true, HoldUntil: now.Add(time.Minute)}, can: true},
		{name: "plain", view: OrderView{Status: order.FulfillmentPending, OwedCents: 100, HoldUntil: now.Add(time.Minute)}, can: true, show: true},
		{name: "expired", view: OrderView{Status: order.FulfillmentPending, OwedCents: 100, PaymentReturnHint: true, HoldUntil: now.Add(-time.Nanosecond)}, can: true, show: true},
		{name: "equal deadline", view: OrderView{Status: order.FulfillmentPending, OwedCents: 100, PaymentReturnHint: true, HoldUntil: now}, can: true, show: true},
		{name: "unknown deadline", view: OrderView{Status: order.FulfillmentPending, OwedCents: 100, PaymentReturnHint: true}, can: true, show: true},
		{name: "expired checking", view: OrderView{Status: order.FulfillmentPending, OwedCents: 100, PaymentReturnHint: true, HoldUntil: now.Add(-time.Nanosecond), PaymentRefreshURL: "/orders/ORD-1?paid=1&confirmation=1"}, can: true},
		{name: "no hold checking", view: OrderView{Status: order.FulfillmentPending, OwedCents: 100, PaymentReturnHint: true, PaymentRefreshURL: "/orders/ORD-1?paid=1&confirmation=1"}, can: true},
		{name: "equal deadline checking", view: OrderView{Status: order.FulfillmentPending, OwedCents: 100, PaymentReturnHint: true, HoldUntil: now, PaymentRefreshURL: "/orders/ORD-1?paid=1&confirmation=1"}, can: true},
		{name: "checking without return marker", view: OrderView{Status: order.FulfillmentPending, OwedCents: 100, PaymentRefreshURL: "/orders/ORD-1?paid=1&confirmation=1"}, can: true},
		{name: "captured", view: OrderView{Status: order.FulfillmentPending, Committed: true, OwedCents: 100, PaymentReturnHint: true, HoldUntil: now.Add(time.Minute)}},
		{name: "store credit funded", view: OrderView{Status: order.FulfillmentPending, OwedCents: 0, PaymentReturnHint: true, HoldUntil: now.Add(time.Minute)}, can: true, show: true},
		{name: "cancelled", view: OrderView{Status: order.FulfillmentCancelled, OwedCents: 100, PaymentReturnHint: true, HoldUntil: now.Add(time.Minute)}},
		{name: "shipped", view: OrderView{Status: order.FulfillmentShipped, OwedCents: 100, PaymentReturnHint: true, HoldUntil: now.Add(time.Minute)}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.view.Now = now
			if got := tt.view.CanCancel(); got != tt.can {
				t.Errorf("CanCancel() = %v, want %v", got, tt.can)
			}
			if got := tt.view.ShowCancel(); got != tt.show {
				t.Errorf("ShowCancel() = %v, want %v", got, tt.show)
			}
		})
	}
}

func TestTheCartSummaryShippingRowAgreesWithTheFreeDeliveryFact(t *testing.T) {
	t.Parallel()
	lines := []CartLine{{VariantID: "v", Slug: "s", Name: "x", Quantity: 2, UnitCents: 180000}}
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
