package checkout

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
)

// islandView is a checkout of NT$1,000 whose address is in a zone that charges
// NT$200 on top of a base rate of baseCents.
func islandView(baseCents int64) View {
	return View{
		Cart: cart.View{
			SubtotalCents: 100000,
			Lines:         []cart.Line{{Name: "x", Quantity: 1, UnitCents: 100000}},
		},
		Shipping:            []cart.ShippingChoice{{VersionID: "s1", Name: "宅配到府", FeeCents: baseCents, Free: baseCents == 0}},
		Chosen:              "s1",
		QuotedShippingCents: baseCents + 20000,
		SurchargeCents:      20000,
		ZoneName:            "離島",
	}
}

// summaryRows is the text of each row of the totals, label first, then its figures.
func summaryRows(t *testing.T, markup string) []string {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		t.Fatalf("parse the checkout: %v", err)
	}
	totals := findDescendant(doc, func(n *html.Node) bool { return attrValue(n, "id") == "summary-totals" })
	if totals == nil {
		t.Fatal("the checkout has no totals")
	}
	var rows []string
	for row := totals.FirstChild; row != nil; row = row.NextSibling {
		if row.Type != html.ElementNode {
			continue
		}
		var cells []string
		for cell := row.FirstChild; cell != nil; cell = cell.NextSibling {
			cells = append(cells, nodeText(cell))
		}
		rows = append(rows, strings.Join(strings.Fields(strings.Join(cells, " ")), " "))
	}
	return rows
}

func rowStarting(t *testing.T, rows []string, label string) string {
	t.Helper()
	for _, r := range rows {
		if strings.HasPrefix(r, label+" ") {
			return r
		}
	}
	t.Fatalf("no summary row starts with %q: %q", label, rows)
	return ""
}

// TestPageShippingRowSurcharge: Page states a zone's surcharge in the delivery
// row itself, after 免運 or the base rate, and the chosen method's card says the
// same. A separate row under 免運 read as if free delivery covered the surcharge.
func TestPageShippingRowSurcharge(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name      string
		locale    i18n.Locale
		label     string
		zone      string
		baseCents int64
		coupon    bool
		want      string
		wantCard  string
	}{
		{
			name: "free base", locale: i18n.ZhHant, label: "運費", zone: "離島",
			want: "運費 免運，離島 +NT$200", wantCard: "免運，離島 +NT$200",
		},
		{
			name: "charged base", locale: i18n.ZhHant, label: "運費", zone: "離島", baseCents: 10000,
			want: "運費 NT$100，離島 +NT$200", wantCard: "NT$100，離島 +NT$200",
		},
		{
			name: "free-delivery code", locale: i18n.ZhHant, label: "運費", zone: "離島", baseCents: 10000, coupon: true,
			want: "運費 免運，離島 +NT$200",
		},
		{
			name: "english", locale: i18n.En, label: "Delivery", zone: "Outlying islands",
			want: "Delivery Free, Outlying islands +NT$200", wantCard: "Free, Outlying islands +NT$200",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v := islandView(tt.baseCents)
			v.ZoneName = tt.zone
			v.CouponFreeShipping = tt.coupon
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			markup := renderComponent(t, ctx, Page(Meta(ctx), &v))

			rows := summaryRows(t, markup)
			if got := rowStarting(t, rows, tt.label); got != tt.want {
				t.Errorf("the delivery row = %q, want %q", got, tt.want)
			}
			if len(rows) != 3 {
				t.Errorf("the totals have %d rows, want subtotal, delivery and total only: %q", len(rows), rows)
			}
			for _, gone := range []string{"已含", "加價", "surcharge (included)"} {
				if strings.Contains(markup, gone) {
					t.Errorf("the checkout still says %q", gone)
				}
			}
			if tt.wantCard != "" && !strings.Contains(markup, `<span class="goen-checkout__shipfee">`+tt.wantCard+`</span>`) {
				t.Errorf("the chosen card does not say %q", tt.wantCard)
			}
		})
	}

	t.Run("a method that was not quoted adds nothing", func(t *testing.T) {
		t.Parallel()
		v := islandView(10000)
		v.Shipping = append(v.Shipping, cart.ShippingChoice{VersionID: "s2", Name: "超商取貨", FeeCents: 6000})
		markup := renderToString(t, Page(Meta(t.Context()), &v))
		if !strings.Contains(markup, `<span class="goen-checkout__shipfee">NT$60</span>`) {
			t.Error("the card of a method the address was not quoted for carries the surcharge")
		}
	})
}

// TestPageCreditNote: Page gives the store-credit row its balance, what this
// order takes and what is left, since the one negative figure says only the
// middle of the three.
func TestPageCreditNote(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	v := View{
		Cart:                 cart.View{SubtotalCents: 2590000, Lines: []cart.Line{{Name: "x", Quantity: 1, UnitCents: 2590000}}},
		Shipping:             []cart.ShippingChoice{{VersionID: "s1", Name: "宅配到府", Free: true}},
		Chosen:               "s1",
		AvailableCreditCents: 3000000,
	}
	rows := summaryRows(t, renderToString(t, Page(Meta(ctx), &v)))
	if got, want := rowStarting(t, rows, "購物金"), "購物金 -NT$25,900 可用 NT$30,000，本次折抵 NT$25,900，剩 NT$4,100"; got != want {
		t.Errorf("the credit row = %q, want %q", got, want)
	}

	none := v
	none.AvailableCreditCents = 0
	for _, r := range summaryRows(t, renderToString(t, Page(Meta(ctx), &none))) {
		if strings.Contains(r, "可用") {
			t.Errorf("a shopper with no credit is told about it: %q", r)
		}
	}
}

// postedFigures is a refused form that had been showing the island view at a mainland
// address's rate, with the fields in changes set over those figures.
func postedFigures(t *testing.T, changes map[string]string) *http.Request {
	t.Helper()
	form := url.Values{
		"shown_subtotal": {"100000"}, "shown_shipping": {"10000"}, "shown_discount": {"0"},
		"shown_credit": {"0"}, "shown_total": {"110000"},
	}
	for name, v := range changes {
		form.Set(name, v)
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/checkout", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := r.ParseForm(); err != nil {
		t.Fatalf("parse the posted figures: %v", err)
	}
	return r
}

// TestView_NoteQuoteChanged: noteQuoteChanged names each row whose figure is not
// the one the form showed, with both figures, and falls back to the general
// sentence when no row can be named. It holds that a refused submit says what
// changed instead of listing every possibility.
func TestView_NoteQuoteChanged(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	general := i18n.T(ctx, i18n.KeyCheckoutChanged)

	t.Run("names the rows and marks them", func(t *testing.T) {
		t.Parallel()
		// Shown for a mainland address at NT$100; the address is now an island's.
		v := islandView(10000)
		v.noteQuoteChanged(ctx, postedFigures(t, nil))

		if v.Repriced != "" {
			t.Errorf("the general sentence stands beside the rows: %q", v.Repriced)
		}
		got := make([]string, len(v.Moved))
		for i, m := range v.Moved {
			got[i] = m.Text
		}
		want := []string{"運費：NT$100 → NT$300", "應付金額：NT$1,100 → NT$1,300"}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("moved rows = %q, want %q", got, want)
		}

		markup := renderToString(t, Page(Meta(ctx), &v))
		notice, _, ok := strings.Cut(markup, "goen-summary__row--total")
		if !ok || !strings.Contains(notice, `<div id="summary-moved"><div class="goen-notice goen-notice--accent" role="status">`) {
			t.Fatal("the rows are not in a status notice above the total")
		}
		for _, line := range want {
			if !strings.Contains(notice, "<li>"+line+"</li>") {
				t.Errorf("the notice does not read %q", line)
			}
		}
		if strings.Contains(markup, general) {
			t.Error("the general sentence is on the page beside the rows")
		}
		flag := i18n.T(ctx, i18n.KeyCheckoutMovedMark)
		rows := summaryRows(t, markup)
		for _, marked := range []string{"運費", "應付金額"} {
			if r := rowStarting(t, rows, marked); !strings.Contains(r, flag) {
				t.Errorf("the %s row is not marked: %q", marked, r)
			}
		}
		if r := rowStarting(t, rows, "小計"); strings.Contains(r, flag) {
			t.Errorf("a row that did not move is marked: %q", r)
		}
	})

	t.Run("no row moved", func(t *testing.T) {
		t.Parallel()
		v := islandView(10000)
		v.noteQuoteChanged(ctx, postedFigures(t, map[string]string{"shown_shipping": "30000", "shown_total": "130000"}))
		if len(v.Moved) != 0 || v.Repriced != general {
			t.Errorf("moved = %v, repriced = %q, want no rows and the general sentence", v.Moved, v.Repriced)
		}
		if strings.Contains(renderToString(t, Page(Meta(ctx), &v)), `<div id="summary-moved"><`) {
			t.Error("the summary holds a notice with nothing in it")
		}
	})

	t.Run("the form does not say", func(t *testing.T) {
		t.Parallel()
		for name, r := range map[string]*http.Request{
			"missing":  postedFigures(t, map[string]string{"shown_total": ""}),
			"garbled":  postedFigures(t, map[string]string{"shown_shipping": "ten"}),
			"negative": postedFigures(t, map[string]string{"shown_shipping": "-1"}),
		} {
			v := islandView(10000)
			v.noteQuoteChanged(ctx, r)
			if len(v.Moved) != 0 || v.Repriced != general {
				t.Errorf("%s: moved = %v, repriced = %q, want the general sentence alone", name, v.Moved, v.Repriced)
			}
		}
	})

	t.Run("no method delivers", func(t *testing.T) {
		t.Parallel()
		// Shown at NT$300; every method has gone, so delivery and the total now read lower.
		v := islandView(10000)
		v.Shipping, v.Chosen, v.QuotedShippingCents = nil, "", 0
		v.noteQuoteChanged(ctx, postedFigures(t, map[string]string{"shown_shipping": "30000", "shown_total": "130000"}))
		if len(v.Moved) != 0 || v.Repriced != general {
			t.Errorf("moved = %v, repriced = %q, want the general sentence and no row reading as free delivery", v.Moved, v.Repriced)
		}
	})

	t.Run("another notice stands", func(t *testing.T) {
		t.Parallel()
		v := islandView(10000)
		v.Repriced = i18n.T(ctx, i18n.KeyShippingUnpriceable)
		v.noteQuoteChanged(ctx, postedFigures(t, nil))
		if len(v.Moved) != 0 || v.Repriced != general {
			t.Errorf("moved = %v, repriced = %q, want the general sentence in place of the stale one", v.Moved, v.Repriced)
		}
	})
}

// TestView_ShownFields: ShownFields carries the figure each summary row states,
// as a size, and Page puts them inside the form and the region a chooser swaps.
// They are what a later refusal compares against, so a figure that differs from
// the row would name a row that never moved.
func TestView_ShownFields(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	v := islandView(10000)
	v.CouponFreeShipping = true
	v.CouponApplied = "測試折扣"
	v.CouponDiscountCents = 5000
	v.AvailableCreditCents = 2000
	got := map[string]string{}
	for _, f := range v.ShownFields() {
		got[f.Name] = f.Value
	}
	want := map[string]string{
		"shown_subtotal": "100000", "shown_shipping": "20000", "shown_discount": "5000",
		"shown_credit": "2000", "shown_total": strconv.FormatInt(v.TotalCents(), 10),
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("%s = %q, want %q", name, got[name], value)
		}
	}
	if len(got) != len(want) {
		t.Errorf("ShownFields = %v, want only %v", got, want)
	}

	doc, err := html.Parse(strings.NewReader(renderToString(t, Page(Meta(ctx), &v))))
	if err != nil {
		t.Fatalf("parse the checkout: %v", err)
	}
	shown := findDescendant(doc, func(n *html.Node) bool { return attrValue(n, "id") == "checkout-shown" })
	if shown == nil {
		t.Fatal("the page has no element for the shown figures")
	}
	inside := map[string]bool{}
	for p := shown.Parent; p != nil; p = p.Parent {
		inside[attrValue(p, "id")] = true
	}
	for _, id := range []string{"checkout-form", "checkout-region"} {
		if !inside[id] {
			t.Errorf("the shown figures are outside #%s, so a chooser's swap would not carry them", id)
		}
	}
}
