package cart

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/discount/campaign"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/picture"
)

// TestPageDeliveryAvailability holds that delivery availability never changes
// checkout eligibility, which would lose a mid-checkout draft; the page only
// says so.
func TestPageDeliveryAvailability(t *testing.T) {
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
					doc, err := html.Parse(strings.NewReader(body))
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
						if n.Type != html.ElementNode {
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

// TestPageSummaryEndsOnTotal holds that the cart summary ends on the total,
// after the shipping row.
func TestPageSummaryEndsOnTotal(t *testing.T) {
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

// TestLine_LineTotalShort holds arithmetic a customer can check: a line the
// shelf cannot meet is priced for what CAN be supplied, and the page says so
// rather than leaving a quantity box that disagrees with the total.
func TestLine_LineTotalShort(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	short := Line{
		VariantID: "v1", Slug: "x", Name: "Pixelight 9 Pro",
		UnitCents: 100000, Quantity: 5, Available: 2, Short: true,
	}
	if got, want := short.LineTotal(), "NT$2,000"; got != want {
		t.Errorf("LineTotal() = %q, want %q — the price is for what can be supplied", got, want)
	}

	markup := renderToString(t, Page(Meta(ctx), View{Lines: []Line{short}}))
	said := fmt.Sprintf(i18n.T(ctx, i18n.KeyPricedFor), "2")
	if !strings.Contains(markup, said) {
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

// TestCartLineSmallPhoto holds that a line's thumbnail, 96px on a phone and 160px
// at most, offers the -400 derivative through the same srcset the product cards
// use, and is told its slot's widths so the browser does not fall back to the
// 1600px source.
func TestCartLineSmallPhoto(t *testing.T) {
	t.Parallel()
	const key = "nimbus-buds-pro-01.webp"
	line := Line{
		Slug: "buds", Name: "Buds", Quantity: 1, UnitCents: 100,
		ImageURL: picture.Image{BundledKey: key}.URL(), ImageSrcset: picture.Image{BundledKey: key}.Srcset(), ImageAlt: "Buds",
	}
	markup := renderToString(t, cartLine(line))
	small := assets.URL("media/products/nimbus-buds-pro-01-400.webp")
	for _, want := range []string{`srcset="` + small + ` 400w`, `sizes="(min-width: 64rem) 160px, (min-width: 40rem) 128px, 96px"`, `width="96"`, `height="96"`} {
		if !strings.Contains(markup, want) {
			t.Errorf("cart line image omits %s:\n%s", want, markup)
		}
	}
}

// TestCartLineNoScript holds that the quantity form keeps the submit button and
// the remove control a browser without script relies on, next to the attribute
// that lets script apply a change on its own.
func TestCartLineNoScript(t *testing.T) {
	t.Parallel()
	markup := renderToString(t, cartLine(Line{Slug: "buds", Name: "Buds", Quantity: 1, UnitCents: 100}))
	for _, want := range []string{
		`action="/cart/items/update" data-autosubmit`,
		`goen-line__update" type="submit">更新`,
		`name="remove" value="1"`,
	} {
		if !strings.Contains(markup, want) {
			t.Errorf("cart line omits %s:\n%s", want, markup)
		}
	}
}

// TestCartLineUpdateSwap holds that the quantity form is an htmx request that
// replaces the line's text and price, the summary, the notices and the header's
// cart link, and names neither the thumbnail, the stepper nor the whole list.
// Naming any of those would repaint what the shopper did not change.
func TestCartLineUpdateSwap(t *testing.T) {
	t.Parallel()
	const id = "11111111-1111-4111-8111-111111111111"
	markup := renderToString(t, cartLine(Line{VariantID: id, Slug: "buds", Name: "Buds", Quantity: 1, UnitCents: 100}))
	for _, want := range []string{
		`hx-post="/cart/items/update"`,
		`hx-swap="none"`,
		`hx-select-oob="#line-body-` + id + `,#line-money-` + id + `,#cart-summary,#cart-lines-heading,#cart-notices,#cart-link,#cart-count:innerHTML"`,
		`hx-sync="closest .goen-cart__lines:queue all"`,
		`data-feedback-skip`,
		`id="line-body-` + id + `"`,
		`id="line-money-` + id + `"`,
	} {
		if !strings.Contains(markup, want) {
			t.Errorf("cart line omits %s:\n%s", want, markup)
		}
	}
	for _, bad := range []string{`hx-target`, `hx-select=`} {
		if strings.Contains(markup, bad) {
			t.Errorf("the line form carries %s, which would replace a whole region:\n%s", bad, markup)
		}
	}
}

// cartMeterOf is the markup of the region a quantity update replaces with the
// cart's standing against free delivery, so a figure in it cannot go stale
// after a change.
func cartMeterOf(t *testing.T, locale i18n.Locale, v View) string {
	t.Helper()
	ctx := i18n.WithLocale(t.Context(), locale)
	markup := renderIn(t, locale, Page(Meta(ctx), v))
	at := strings.Index(markup, `id="cart-count"`)
	end := strings.Index(markup, `id="cart-lines-heading"`)
	if at < 0 || end < at {
		t.Fatalf("the cart has no meter region before its lines: %s", markup)
	}
	return markup[at:end]
}

// TestPageFreeDeliveryMeter holds that the meter says how far the cart is from
// free delivery and draws it against the one amount every method turns free
// at: the reading is the subtotal, capped at the threshold, rounded down; once
// reached it says so with the zones still charged extra. Where the methods
// disagree it draws nothing, and where none names an amount it draws no bar.
func TestPageFreeDeliveryMeter(t *testing.T) {
	t.Parallel()
	lines := []Line{{VariantID: "v", Slug: "s", Name: "x", Quantity: 2, UnitCents: 40000}}
	for _, tt := range []struct {
		name   string
		locale i18n.Locale
		v      View
		want   []string
		not    []string
	}{
		{
			"short of the threshold", i18n.ZhHant,
			View{Lines: lines, ItemCount: 2, SubtotalCents: 80000,
				FreeDelivery: FreeDelivery{Kind: FreeDeliveryShort, ShortfallCents: 220000, ThresholdCents: 300000}},
			[]string{`<p class="goen-meter__text" id="cart-meter">免運還差 NT$2,200</p>`, `max="300000"`, `value="80000"`,
				`aria-labelledby="cart-meter"`, `aria-describedby="cart-meter-note"`, ">26%<", "滿 NT$3,000 免運"},
			[]string{"已享免運", "goen-meter--met"},
		},
		{
			"one dollar short never reads full", i18n.En,
			View{Lines: lines, ItemCount: 2, SubtotalCents: 299900,
				FreeDelivery: FreeDelivery{Kind: FreeDeliveryShort, ShortfallCents: 100, ThresholdCents: 300000}},
			[]string{"NT$1 to free delivery", `value="299900"`, ">99%<", "Free over NT$3,000"},
			[]string{">100%<"},
		},
		{
			"threshold reached with a zone charged extra", i18n.ZhHant,
			View{Lines: lines, ItemCount: 2, SubtotalCents: 360000,
				FreeDelivery: FreeDelivery{Kind: FreeDeliveryReached, ThresholdCents: 300000, SurchargeZones: []string{"離島"}}},
			[]string{"goen-meter--met", "已享免運（離島另計）", `value="300000"`, ">100%<", "已滿 NT$3,000"},
			[]string{"免運還差", "NT$0"},
		},
		{
			"free at any subtotal names no amount and draws no bar", i18n.En,
			View{Lines: lines, ItemCount: 2, SubtotalCents: 80000,
				FreeDelivery: FreeDelivery{Kind: FreeDeliveryReached}},
			[]string{">Free delivery<"},
			[]string{"<progress", "Over NT$"},
		},
		{
			"a method that never turns free", i18n.ZhHant,
			View{Lines: lines, ItemCount: 2, SubtotalCents: 80000},
			nil,
			[]string{"goen-meter", "免運", "<progress"},
		},
	} {
		got := cartMeterOf(t, tt.locale, tt.v)
		for _, w := range tt.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: the meter lacks %q: %s", tt.name, w, got)
			}
		}
		for _, w := range tt.not {
			if strings.Contains(got, w) {
				t.Errorf("%s: the meter holds %q: %s", tt.name, w, got)
			}
		}
	}
}

// TestPageEmptyNoMeter holds that an empty cart draws no meter and no lines.
func TestPageEmptyNoMeter(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	v := View{FreeDelivery: FreeDelivery{Kind: FreeDeliveryShort, ShortfallCents: 300000, ThresholdCents: 300000}}
	markup := renderToString(t, Page(Meta(ctx), v))
	for _, unwanted := range []string{"goen-meter", `id="cart-count"`, `id="cart-lines-heading"`} {
		if strings.Contains(markup, unwanted) {
			t.Errorf("an empty cart draws %s", unwanted)
		}
	}
}

// TestPageSoldOutLine holds that a sold-out line is left out of the count and
// offers only remove.
func TestPageSoldOutLine(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	v := View{
		Lines: []Line{
			{VariantID: "ok", Slug: "a", Name: "a", Quantity: 2, Available: 5, UnitCents: 40000},
			{VariantID: "gone", Slug: "b", Name: "b", Quantity: 1, UnitCents: 99900, Unavailable: true},
		},
		ItemCount: 2, SubtotalCents: 80000,
	}
	markup := renderToString(t, Page(Meta(ctx), v))

	if !strings.Contains(markup, ">2 件商品</h2>") || strings.Contains(markup, "3 件商品") {
		t.Error("the sold-out line is counted in n 件商品")
	}
	if strings.Contains(markup, "已無庫存") {
		t.Error("the sold-out line still says 已無庫存")
	}
	_, afterOpen, found := strings.Cut(markup, `id="line-gone"`)
	gone, _, closed := strings.Cut(afterOpen, "</li>")
	if !found || !closed {
		t.Fatalf("the sold-out line is not in the cart: %s", markup)
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
	if !strings.Contains(markup, `aria-disabled="true"`) || strings.Contains(markup, `href="/checkout"`) {
		t.Error("checkout is offered while a line is sold out")
	}
	if !strings.Contains(markup, `aria-describedby="cart-alert"`) || !strings.Contains(markup, `id="cart-alert"`) {
		t.Error("the disabled checkout does not point at the sentence that says why")
	}
}

// TestView_StockNoticeBlocked holds that the notice says what blocks checkout:
// a sold-out line before a short one, and nothing when neither blocks.
func TestView_StockNoticeBlocked(t *testing.T) {
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

// TestPagePayStartNote holds that the cart says the stock is held once the order
// is placed, and how long the customer has to start paying, under the checkout
// button; it does not say the cart itself holds anything.
func TestPagePayStartNote(t *testing.T) {
	t.Parallel()
	lines := []Line{{VariantID: "v", Slug: "s", Name: "x", Quantity: 1, UnitCents: 100}}
	for locale, want := range map[i18n.Locale]string{
		i18n.ZhHant: "送出訂單後保留，請在 28 分鐘內開始付款",
		i18n.En:     "Held once you place the order; start paying within 28 minutes",
	} {
		ctx := i18n.WithLocale(t.Context(), locale)
		markup := renderIn(t, locale, Page(Meta(ctx), View{Lines: lines, PayStartMinutes: 28}))
		if !strings.Contains(markup, want) {
			t.Errorf("%s: the cart lacks %q under the checkout button", locale, want)
		}
	}
}

// TestPageRemoveSkipsValidation holds that removing a line is not blocked by a
// quantity the shelf cannot meet: the remove button shares a form with the
// quantity field, whose max is the stock, so a number typed above it would stop
// the removal behind a native validation bubble.
func TestPageRemoveSkipsValidation(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	line := Line{VariantID: "v1", Slug: "x", Name: "x", UnitCents: 100000, Quantity: 2, Available: 4}
	markup := renderToString(t, Page(Meta(ctx), View{Lines: []Line{line}}))
	i := strings.Index(markup, `name="remove"`)
	if i < 0 {
		t.Fatal("the line has no remove button")
	}
	open := strings.LastIndex(markup[:i], "<button")
	if tag := markup[open : i+strings.Index(markup[i:], ">")]; !strings.Contains(tag, "formnovalidate") {
		t.Errorf("the remove button does not skip the form's validation: %s", tag)
	}
}

// TestView_LineCountAgrees holds that the count over the lines takes the
// English singular for one unit and the plural otherwise.
func TestView_LineCountAgrees(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale i18n.Locale
		units  int64
		want   string
	}{
		{i18n.En, 1, "1 item"},
		{i18n.En, 2, "2 items"},
		{i18n.ZhHant, 1, "1 件商品"},
	} {
		ctx := i18n.WithLocale(t.Context(), tt.locale)
		if got := (View{ItemCount: tt.units}).LineCount(ctx); got != tt.want {
			t.Errorf("%s: LineCount(%d) = %q, want %q", tt.locale.Tag(), tt.units, got, tt.want)
		}
	}
}

// TestPageShippingRowFreeDelivery holds that the cart summary's shipping row
// agrees with the free-delivery meter.
func TestPageShippingRowFreeDelivery(t *testing.T) {
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
		markup := b.String()
		if tt.kind == FreeDeliveryReached {
			meter := markup[strings.Index(markup, `id="cart-count"`):strings.Index(markup, `id="cart-lines-heading"`)]
			for _, zone := range tt.zones {
				if !strings.Contains(meter, zone) {
					t.Errorf("%s: the meter does not name %q, which the summary charges extra: %s", tt.name, zone, meter)
				}
			}
		}
		at := strings.Index(markup, `id="cart-summary"`)
		if at < 0 {
			t.Fatalf("%s: no cart summary: %s", tt.name, markup)
		}
		markup = markup[at:]
		if end := strings.Index(markup, "</dl>"); end > 0 {
			markup = markup[:end]
		}
		if !strings.Contains(markup, tt.want) {
			t.Errorf("%s: summary lacks %q: %s", tt.name, tt.want, markup)
		}
		if strings.Contains(markup, tt.not) {
			t.Errorf("%s: summary holds %q: %s", tt.name, tt.not, markup)
		}
		if len(tt.zones) == 0 && (strings.Contains(markup, "另計") || strings.Contains(markup, " extra)")) {
			t.Errorf("%s: summary names a surcharge zone where there is none: %s", tt.name, markup)
		}
	}
}

// TestPageReorderLeftOut holds that a buy-again names each line it left out and
// why, links a product only while it is listed, and draws no list for a browser
// that was given no names.
func TestPageReorderLeftOut(t *testing.T) {
	t.Parallel()
	left := []SkippedLine{
		{Name: "Teapot", Label: "Blue", Reason: SkipSoldOut, Slug: "teapot"},
		{Name: "Old kettle", Reason: SkipGone},
	}
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			render := func(left []SkippedLine) *html.Node {
				body := renderComponent(t, ctx, Page(Meta(ctx), View{ReorderAdded: 1, ReorderSkipped: 2, ReorderLeftOut: left}))
				doc, err := html.Parse(strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				return doc
			}

			if list := elementByClass(render(nil), "ul", "goen-cart__leftout"); list != nil {
				t.Error("a page given no names draws a list of them")
			}
			list := elementByClass(render(left), "ul", "goen-cart__leftout")
			if list == nil {
				t.Fatal("the page lists none of the left-out lines")
			}
			type row struct {
				Parts []string
				Href  string
			}
			var got []row
			for li := list.FirstChild; li != nil; li = li.NextSibling {
				if li.Type != html.ElementNode {
					continue
				}
				var r row
				for part := li.FirstChild; part != nil; part = part.NextSibling {
					if part.Type != html.ElementNode {
						continue
					}
					r.Parts = append(r.Parts, strings.Join(strings.Fields(text(part)), " "))
					if part.Data == "a" {
						r.Href = attr(part, "href")
					}
				}
				got = append(got, r)
			}
			want := []row{
				{Parts: []string{"Teapot", "Blue", i18n.T(ctx, i18n.KeySoldOut)}, Href: "/p/teapot"},
				{Parts: []string{"Old kettle", i18n.T(ctx, i18n.KeyReorderGone)}},
			}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("left-out lines (-want +got):\n%s", diff)
			}
		})
	}
}

// elementByClass is the first element named tag whose class list holds class.
func elementByClass(root *html.Node, tag, class string) *html.Node {
	for n := range root.Descendants() {
		if n.Type == html.ElementNode && n.Data == tag && slices.Contains(strings.Fields(attr(n, "class")), class) {
			return n
		}
	}
	return nil
}

// TestView_ReorderTextAgrees holds that the partial-reorder sentence agrees in
// number with both counts, which it names in one breath.
func TestView_ReorderTextAgrees(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	for _, c := range []struct{ added, skipped int32 }{{1, 1}, {1, 2}, {2, 1}, {2, 2}} {
		got := View{ReorderAdded: c.added, ReorderSkipped: c.skipped}.ReorderText(ctx)
		for _, n := range []int32{c.added, c.skipped} {
			wrong := []string{" item ", " is "}
			if n == 1 {
				wrong = []string{" items", " are "}
			}
			for _, w := range wrong {
				if strings.Contains(got, fmt.Sprintf("%d%s", n, w)) {
					t.Errorf("ReorderText(added %d, skipped %d) = %q, which says %d%q", c.added, c.skipped, got, n, w)
				}
			}
		}
	}
}

// TestCartLineOnSale holds that a line on a campaign's price shows it beside the
// struck price it replaces and says until when it holds, 特價至 11 月 22 日 or
// On sale until Nov 22, the English phrase's trailing comma heard and not seen;
// and that a line not on sale shows its unit price and nothing of a campaign,
// whatever its compare-at price.
func TestCartLineOnSale(t *testing.T) {
	t.Parallel()
	taipei := time.FixedZone("CST", 8*3600)
	end := campaign.NewEnd(time.Date(2026, 11, 23, 0, 0, 0, 0, taipei), time.Date(2026, 11, 13, 12, 0, 0, 0, taipei))
	sale := Line{Slug: "mug", Name: "Mug", Quantity: 1, Available: 3, UnitCents: 43200, CompareCents: 48000, Sale: &end}
	plain := sale
	plain.Sale = nil
	for _, tt := range []struct {
		locale i18n.Locale
		until  string
		was    string
	}{
		{i18n.ZhHant, "<span class=\"goen-line__until\">特價至 <time datetime=\"2026-11-22\">11\u00a0月 22\u00a0日</time><span class=\"goen-sr-only\"></span></span>", "原價"},
		{i18n.En, "<span class=\"goen-line__until\">On sale until <time datetime=\"2026-11-22\">Nov\u00a022</time><span class=\"goen-sr-only\">,</span></span>", "Was"},
	} {
		markup := renderIn(t, tt.locale, cartLine(sale))
		for _, want := range []string{
			tt.until,
			`<span class="goen-line__sale">NT$432</span>`,
			`<span class="goen-sr-only">` + tt.was + `</span><s class="goen-line__was">NT$480</s>`,
		} {
			if !strings.Contains(markup, want) {
				t.Errorf("%s: the line on sale lacks %s:\n%s", tt.locale, want, markup)
			}
		}
		if strings.Contains(markup, "goen-line__unit") {
			t.Errorf("%s: the line on sale still says its unit price as a plain one", tt.locale)
		}
		markup = renderIn(t, tt.locale, cartLine(plain))
		for _, unwanted := range []string{"goen-line__until", "goen-line__sale", "<s "} {
			if strings.Contains(markup, unwanted) {
				t.Errorf("%s: a line not on sale draws %s", tt.locale, unwanted)
			}
		}
		if want := fmt.Sprintf(i18n.T(i18n.WithLocale(t.Context(), tt.locale), i18n.KeyUnitPrice), "NT$432"); !strings.Contains(markup, want) {
			t.Errorf("%s: a line not on sale lacks %q", tt.locale, want)
		}
	}
}
