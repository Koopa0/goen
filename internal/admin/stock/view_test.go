package stock

import (
	"bytes"
	stdhtml "html"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"
	xhtml "golang.org/x/net/html"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/product/inventory"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/stockrisk"
)

// The tests in this file are the stock halves of tests that render several
// desks' pages: each half lives with the page it renders.

const stockedAllWindow = 30 * 24 * time.Hour

// From small_defects_test.go, moved whole.
func TestEmptyLedgerSaysNoMovementOnce(t *testing.T) {
	t.Parallel()
	v := &MovementsView{SKU: "A-1", Days: []StockDay{{Day: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}}}
	body := render(t, Movements(frame.Page{Title: "Movements"}, v))
	if strings.Contains(body, v.NoStockLine(i18n.WithLocale(t.Context(), i18n.En))) {
		t.Error("an empty ledger also prints the days-without-movement line")
	}
	if !strings.Contains(body, i18n.T(i18n.WithLocale(t.Context(), i18n.En), i18n.KeyAdminLedgerEmpty)) {
		t.Error("an empty ledger lacks its empty state")
	}
}

// From stockrisk_test.go, moved whole.
func TestStockDeskListsDaysCoverAboveTheSearch(t *testing.T) {
	t.Parallel()

	for _, loc := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), loc)
		rows := []stockrisk.Row{{Risk: inventory.Risk{SKU: "OUT-1", Name: "Gone", Slug: "gone"}},
			{Risk: inventory.Risk{SKU: "EST-1", Name: "Going", Slug: "going", Sales: inventory.Sales{Sellable: 20, Sold: 60, Orders: 30, InStock: stockedAllWindow}}},
		}
		html := renderComponent(t, ctx, Variants(frame.Page{}, VariantsView{ShowCover: true, AtRisk: rows, MoreSoldOut: 3}))

		heading := strings.Index(html, i18n.T(ctx, i18n.KeyAdminRepStock))
		search := strings.Index(html, `id="stock-search"`)
		out, est := strings.Index(html, "OUT-1"), strings.Index(html, "EST-1")
		if heading < 0 || search < 0 || heading > search {
			t.Errorf("%s: days cover heading at %d, search at %d, want the heading first", loc, heading, search)
		}
		if out < 0 || est < 0 || out > est {
			t.Errorf("%s: sold out row at %d, estimated row at %d, want sold out first", loc, out, est)
		}
		for _, want := range []string{
			i18n.Count(ctx, i18n.KeyAdminRepMoreSoldOut, 3, 3), "goen-chartrangebar",
		} {
			if !strings.Contains(html, want) {
				t.Errorf("%s: stock desk lacks %q", loc, want)
			}
		}
	}
}

// From stockrisk_test.go, moved whole.
func TestStockDeskSaysSoWhenNothingNeedsRestocking(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	html := renderComponent(t, ctx, Variants(frame.Page{}, VariantsView{ShowCover: true}))
	if want := i18n.Count(ctx, i18n.KeyAdminRepStockEmpty, inventory.CoverWindowDays, inventory.CoverWindowDays); !strings.Contains(html, want) {
		t.Errorf("an empty days cover list does not say %q", want)
	}
}

// From stockrisk_test.go, moved whole.
func TestStockDeskLaterPagesLeaveDaysCoverOut(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	html := renderComponent(t, ctx, Variants(frame.Page{}, VariantsView{Variants: []Variant{{SKU: "A-1", ProductName: "x"}}}))
	if strings.Contains(html, i18n.T(ctx, i18n.KeyAdminRepStock)) {
		t.Error("a page without ShowCover still lists the days cover section")
	}
}

// From TestReportAndStockDeskShareTheDaysCoverSection.
func TestStockDeskDrawsTheSharedDaysCoverSection(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	rows := []stockrisk.Row{{Risk: inventory.Risk{SKU: "OUT-1", Name: "Gone", Slug: "gone"}}}
	section := renderComponent(t, ctx, stockrisk.Section(rows, 0, inventory.CoverWindowDays))
	for name, page := range map[string]string{
		"stock desk": renderComponent(t, ctx, Variants(frame.Page{}, VariantsView{ShowCover: true, AtRisk: rows})),
	} {
		if !strings.Contains(page, section) {
			t.Errorf("%s does not contain the shared days cover section", name)
		}
	}
}

// From TestAuditEmptyPageRestartLink.
func TestStockEmptyPagesKeepTheirRestartLink(t *testing.T) {
	t.Parallel()
	b := keyset.Bound{PastEnd: true, First: "/admin/products?q=test"}
	p := frame.Page{Title: "Pagination"}
	cases := map[string]templ.Component{
		"stock":     Variants(p, VariantsView{Bound: b}),
		"movements": Movements(p, &MovementsView{Bound: b}),
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			if err := c.Render(i18n.WithLocale(t.Context(), i18n.En), &out); err != nil {
				t.Fatal(err)
			}
			body := out.String()
			if !strings.Contains(body, `href="/admin/products?q=test"`) || !strings.Contains(body, "First page") {
				t.Fatal("empty page lost its plain restart link")
			}
			if strings.Count(body, "There are no entries on this page.") != 1 {
				t.Fatal("empty page lost its shared empty state")
			}
		})
	}
}

// From TestAuditFirstPageEmpty.
func TestStockQueuesKeepTheirOwnFirstPageEmptyState(t *testing.T) {
	t.Parallel()
	ctx := func(k i18n.Key) string { return i18n.T(i18n.WithLocale(t.Context(), i18n.En), k) }
	generic := ctx(i18n.KeyPageEmpty)
	_, b := keyset.PageBound("/admin/queue", false, []db.Order{}, keyset.PageSize, func(*db.Order) string { return "" })
	p := frame.Page{Title: "Queue"}
	for name, c := range map[string]struct {
		page templ.Component
		want string
	}{
		"stock":     {Variants(p, VariantsView{Bound: b}), ctx(i18n.KeyAdminQueueNoVariants)},
		"movements": {Movements(p, &MovementsView{Bound: b}), ctx(i18n.KeyAdminLedgerEmpty)},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			body := render(t, c.page)
			if !strings.Contains(body, stdhtml.EscapeString(c.want)) {
				t.Errorf("the queue's own empty text %q is missing", c.want)
			}
			if strings.Contains(body, generic) {
				t.Error("an empty first page shows the later-page sentence")
			}
		})
	}
}

// From TestAuditFullPageOffersNext.
func TestStockQueuesWithMoreRowsOfferNext(t *testing.T) {
	t.Parallel()
	ctx := func(k i18n.Key) string { return i18n.T(i18n.WithLocale(t.Context(), i18n.En), k) }
	rows := make([]db.Order, keyset.PageLimit)
	_, b := keyset.PageBound("/admin/queue", false, rows, keyset.PageSize,
		func(*db.Order) string { return `{"ID":"12345678-1234-1234-1234-123456789abc"}` })
	p := frame.Page{Title: "Queue"}
	for name, page := range map[string]templ.Component{
		"stock":     Variants(p, VariantsView{Bound: b}),
		"movements": Movements(p, &MovementsView{Bound: b}),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			body := render(t, page)
			if !strings.Contains(body, `rel="next"`) {
				t.Error("a first page with more rows has no Next link")
			}
			if strings.Contains(body, ctx(i18n.KeyPageEmpty)) {
				t.Error("a first page with more rows says it is empty")
			}
		})
	}
}

// From the examples test the admin screens once shared.
func TestStockExamplesStayOutsideRefusedInputs(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		for _, draft := range []struct{ value, refusal string }{{"", ""}, {"Submitted <& value", ""}, {"", "Refused"}, {"Submitted <& value", "Refused"}} {
			typed, refusal := draft.value, draft.refusal
			t.Run(locale.Tag()+"/"+typed+"/"+refusal, func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				prefix := "Example: "
				if locale == i18n.ZhHant {
					prefix = "例："
				}
				p := frame.Page{}
				for _, page := range []struct {
					name      string
					component templ.Component
					fields    map[string]string
				}{
					{"stock adjustment", Variants(p, VariantsView{Variants: []Variant{{SKU: "sample", DraftDelta: typed, DeltaError: refusal}}}), map[string]string{"adj-sample": prefix + "+10"}},
				} {
					t.Run(page.name, func(t *testing.T) {
						markup := renderComponent(t, ctx, page.component)
						z := xhtml.NewTokenizer(strings.NewReader(markup))
						for z.Next() != xhtml.ErrorToken {
							token := z.Token()
							if token.Data != "input" {
								continue
							}
							var descriptions int
							for _, attribute := range token.Attr {
								if attribute.Key == "aria-describedby" {
									descriptions++
								}
							}
							if descriptions > 1 {
								t.Error("an input repeats aria-describedby")
							}
						}
						doc, err := xhtml.Parse(strings.NewReader(markup))
						if err != nil {
							t.Fatal(err)
						}
						for id, example := range page.fields {
							input := exampleElementByID(doc, id)
							if input == nil || input.Data != "input" {
								t.Fatalf("refused %s has no input %s", page.name, id)
							}
							if attr(input, "placeholder") != "" || attr(input, "value") != typed {
								t.Errorf("%s placeholder/value = %q/%q, want empty/%q", id, attr(input, "placeholder"), attr(input, "value"), typed)
							}
							var linkedExample bool
							for _, description := range strings.Fields(attr(input, "aria-describedby")) {
								hint := exampleElementByID(doc, description)
								if hint == nil {
									t.Errorf("%s description %s has no text", id, description)
									continue
								}
								_, hidden := attrPresent(hint, "hidden")
								if textOf(hint) == example && !hidden && !strings.Contains(attr(hint, "class"), "sr-only") {
									linkedExample = true
								}
							}
							if !linkedExample {
								t.Errorf("refused %s has no visible linked example %q", page.name, example)
							}
						}
					})
				}
			})
		}
	}
}

// From TestDeskRefusalsNameTheirFormAndLinkToTheFirstInvalidControl.
func TestStockRefusalNamesItsFormAndLinksToTheFirstInvalidControl(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			bad := i18n.T(ctx, i18n.KeyAdminNoticeRefused)
			cases := []struct {
				name                  string
				component             templ.Component
				action, target, first string
				title                 i18n.Key
				count                 int
			}{
				{"stock", Variants(frame.Page{}, VariantsView{Variants: []Variant{{SKU: "TEST-SKU", DraftDelta: " +oops ", DeltaError: bad}}}), "/admin/stock/adjust#row-TEST-SKU", "row-TEST-SKU", "adj-TEST-SKU", i18n.KeyAdminRefusalStock, 1},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					body := renderComponent(t, ctx, tc.component)
					doc, err := xhtml.Parse(strings.NewReader(body))
					if err != nil {
						t.Fatal(err)
					}
					var alert *xhtml.Node
					var form, target, first *xhtml.Node
					ids := map[string]*xhtml.Node{}
					for n := range doc.Descendants() {
						if n.Type != xhtml.ElementNode {
							continue
						}
						id := refusalAttr(n, "id")
						if id != "" {
							ids[id] = n
						}
						if n.Data == "form" && refusalAttr(n, "action") == tc.action {
							form = n
						}
						if id == tc.target {
							target = n
						}
						if id == tc.first {
							first = n
						}
						if refusalAttr(n, "role") == "alert" && strings.Contains(refusalText(n), i18n.T(ctx, tc.title)) {
							for link := range n.Descendants() {
								if link.Data == "a" && refusalAttr(link, "href") == "#"+tc.first {
									alert = n
								}
							}
						}
					}
					if form == nil || target == nil {
						t.Errorf("form must post to its section: %s", tc.action)
					}
					if alert == nil {
						t.Fatalf("page-top alert must name %q and link to #%s", i18n.T(ctx, tc.title), tc.first)
					}
					count := "2"
					if tc.count == 1 {
						count = "1"
					}
					if !strings.Contains(refusalText(alert), count) {
						t.Errorf("alert does not count %d invalid fields", tc.count)
					}
					for n := range doc.Descendants() {
						if n == alert {
							break
						}
						if n.Data == "form" && strings.Contains(refusalAttr(n, "class"), "goen-admin__") {
							t.Error("summary must precede the forms")
							break
						}
					}
					if first == nil || refusalAttr(first, "aria-invalid") != "true" {
						t.Errorf("first invalid control %s is not marked", tc.first)
					}
					for n := range doc.Descendants() {
						if n.Type != xhtml.ElementNode || refusalAttr(n, "aria-invalid") != "true" {
							continue
						}
						describes := strings.Fields(refusalAttr(n, "aria-describedby"))
						if len(describes) == 0 {
							t.Errorf("invalid control %s has no description", refusalAttr(n, "id"))
						}
						for _, id := range describes {
							if ids[id] == nil || strings.TrimSpace(refusalText(ids[id])) == "" {
								t.Errorf("invalid control references missing error %s", id)
							}
						}
					}
				})
			}
		})
	}
}

func exampleElementByID(n *xhtml.Node, id string) *xhtml.Node {
	if n.Type == xhtml.ElementNode && attr(n, "id") == id {
		return n
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if found := exampleElementByID(child, id); found != nil {
			return found
		}
	}
	return nil
}

func refusalAttr(n *xhtml.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func refusalText(n *xhtml.Node) string {
	if n == nil {
		return ""
	}
	var out strings.Builder
	if n.Type == xhtml.TextNode {
		out.WriteString(n.Data)
	}
	for child := range n.Descendants() {
		if child.Type == xhtml.TextNode {
			out.WriteString(child.Data)
		}
	}
	return out.String()
}

func attr(n *xhtml.Node, key string) string {
	v, _ := attrPresent(n, key)
	return v
}

func attrPresent(n *xhtml.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

func textOf(n *xhtml.Node) string {
	if n.Type == xhtml.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(textOf(c))
	}
	return strings.TrimSpace(b.String())
}
