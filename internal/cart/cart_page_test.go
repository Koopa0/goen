package cart

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	htmlparse "golang.org/x/net/html"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
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
					v := View{Lines: []Line{{VariantID: "item", Name: "Item", UnitCents: 100, Quantity: 1, Available: 2}}, SubtotalCents: 100, ItemCount: 1, NoDelivery: tt.noDelivery}
					if tt.blocked {
						v.Lines[0].Quantity, v.Lines[0].Short = 3, true
						v.SubtotalCents, v.ItemCount = 200, 2
					}
					if v.CanCheckout() != !tt.blocked {
						t.Error("delivery availability changed checkout eligibility, which would lose a mid-checkout draft")
					}
					body := renderComponent(t, ctx, Page(Meta(ctx), v))
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

func TestTheCartSummaryEndsOnTheTotal(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	v := View{Lines: []Line{{VariantID: "item", Name: "Item", UnitCents: 100, Quantity: 1, Available: 2}}, SubtotalCents: 100, ItemCount: 1}
	body := renderComponent(t, ctx, Page(Meta(ctx), v))
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

// TestAShortLineSaysWhatItIsPricedFor holds arithmetic a customer can check: a
// line the shelf cannot meet is priced for what CAN be supplied, and the page
// says so rather than leaving a quantity box that disagrees with the total.
func TestAShortLineSaysWhatItIsPricedFor(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	short := Line{
		VariantID: "v1", Slug: "x", Name: "Pixelight 9 Pro",
		UnitCents: 100000, Quantity: 5, Available: 2, Short: true,
	}
	if got, want := short.LineTotal(), "NT$2,000"; got != want {
		t.Errorf("LineTotal() = %q, want %q — the price is for what can be supplied", got, want)
	}

	html := renderToString(t, Page(Meta(ctx), View{Lines: []Line{short}}))
	said := fmt.Sprintf(i18n.T(ctx, i18n.KeyPricedFor), "2")
	if !strings.Contains(html, said) {
		t.Errorf("a line asking for 5 and priced for 2 does not say so; wanted %q", said)
	}

	// A line the shelf can meet says nothing: "priced for 5" beside "× 5" is
	// noise on every ordinary row in the cart.
	ok := Line{VariantID: "v2", Slug: "y", Name: "y", UnitCents: 100000, Quantity: 2, Available: 9}
	full := renderToString(t, Page(Meta(ctx), View{Lines: []Line{ok}}))
	if strings.Contains(full, i18n.T(ctx, i18n.KeyPricedFor)[:3]) {
		t.Error("an ordinary line is annotated with what it is priced for")
	}
}

// TestCartLineLoadsTheSmallPhoto holds that a 96px thumbnail offers the -400
// derivative through the same srcset the product cards use, and is told its
// slot is 96px wide so the browser does not fall back to the 1600px source.
func TestCartLineLoadsTheSmallPhoto(t *testing.T) {
	t.Parallel()
	const key = "nimbus-buds-pro-01.webp"
	line := Line{
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
	html := renderToString(t, cartLine(Line{Slug: "buds", Name: "Buds", Quantity: 1, UnitCents: 100}))
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
	html := renderToString(t, cartLine(Line{VariantID: id, Slug: "buds", Name: "Buds", Quantity: 1, UnitCents: 100}))
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

// cartFactsOf is the markup of the cart's fact line, the region a quantity
// update replaces, so a figure in it cannot go stale after a change.
func cartFactsOf(t *testing.T, v View) string {
	t.Helper()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderToString(t, Page(Meta(ctx), v))
	at := strings.Index(html, `id="cart-count"`)
	end := strings.Index(html, `id="cart-notices"`)
	if at < 0 || end < at {
		t.Fatalf("the cart has no fact line before its notices: %s", html)
	}
	return html[at:end]
}

func TestTheCartFactLineStatesItemsSubtotalAndFreeDelivery(t *testing.T) {
	t.Parallel()
	lines := []Line{{VariantID: "v", Slug: "s", Name: "x", Quantity: 2, UnitCents: 40000}}
	for _, tt := range []struct {
		name string
		v    View
		want []string
		not  []string
	}{
		{
			"short of the threshold",
			View{Lines: lines, ItemCount: 2, SubtotalCents: 80000,
				FreeDelivery: FreeDelivery{Kind: FreeDeliveryShort, ShortfallCents: 220000, ThresholdCents: 300000}},
			[]string{"商品", "2\u00a0<small>件</small>", "小計", `<small class="ui-statline__pre">NT$</small>800`,
				"免運還差", `<small class="ui-statline__pre">NT$</small>2,200`, "滿 NT$3,000 免運"},
			nil,
		},
		{
			"threshold reached",
			View{Lines: lines, ItemCount: 2, SubtotalCents: 360000,
				FreeDelivery: FreeDelivery{Kind: FreeDeliveryReached, ThresholdCents: 300000}},
			[]string{"運費", "<dd>免運<", "已滿 NT$3,000"},
			[]string{"免運還差", "NT$0"},
		},
		{
			"a method that never turns free",
			View{Lines: lines, ItemCount: 2, SubtotalCents: 80000},
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
	if html := renderToString(t, Page(Meta(ctx), View{})); strings.Contains(html, "ui-statline") {
		t.Error("an empty cart prints a fact line")
	}
}

func TestASoldOutLineIsNotCountedAndOffersOnlyRemove(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	v := View{
		Lines: []Line{
			{VariantID: "ok", Slug: "a", Name: "a", Quantity: 2, Available: 5, UnitCents: 40000},
			{VariantID: "gone", Slug: "b", Name: "b", Quantity: 1, UnitCents: 99900, Unavailable: true},
		},
		ItemCount: 2, SubtotalCents: 80000,
	}
	html := renderToString(t, Page(Meta(ctx), v))

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
	line := Line{VariantID: "v", Slug: "s", Name: "x", Quantity: 1, UnitCents: 100}
	soldOut, short := line, line
	soldOut.Unavailable = true
	short.Short = true

	for _, tt := range []struct {
		name  string
		lines []Line
		want  string
	}{
		{"sold out", []Line{soldOut}, "有商品已售完，移除後才能結帳。"},
		{"short", []Line{short}, "有商品的庫存不足，請先調整數量再結帳。"},
		{"both: the sold-out line is the one to deal with first", []Line{short, soldOut}, "有商品已售完，移除後才能結帳。"},
		{"neither", []Line{line}, ""},
	} {
		got := View{Lines: tt.lines}.StockNotice(ctx)
		if got != tt.want {
			t.Errorf("%s: StockNotice = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestTheCartShowsTheStockHoldUnderCheckout(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	lines := []Line{{VariantID: "v", Slug: "s", Name: "x", Quantity: 1, UnitCents: 100}}
	html := renderToString(t, Page(Meta(ctx), View{Lines: lines, HoldMinutes: 61}))
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
	line := Line{VariantID: "v1", Slug: "x", Name: "x", UnitCents: 100000, Quantity: 2, Available: 4}
	html := renderToString(t, Page(Meta(ctx), View{Lines: []Line{line}}))
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
	facts := View{ItemCount: 2, SubtotalCents: 200}.Facts(ctx)
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
	lines := []Line{{VariantID: "v", Slug: "s", Name: "x", Quantity: 2, UnitCents: 180000}}
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
		v := View{Lines: lines, ItemCount: 2, SubtotalCents: 360000,
			FreeDelivery: FreeDelivery{Kind: tt.kind, ShortfallCents: 1, ThresholdCents: 300000, SurchargeZones: tt.zones}}
		var b strings.Builder
		if err := Page(Meta(ctx), v).Render(ctx, &b); err != nil {
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
