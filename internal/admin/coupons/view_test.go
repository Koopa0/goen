package coupons

import (
	"bytes"
	"html"
	"strings"
	"testing"

	"github.com/a-h/templ"
	xhtml "golang.org/x/net/html"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// The tests in this file are the coupon halves of cross-feature tests that
// still live in internal/ui/pages/admin. That package's test build cannot
// import this one while the back office's audit desk reaches the view
// package, so each half moved here with the page it renders and the other
// views stay behind.

func TestTheCouponFormSaysTheLimitIsPerMemberAccount(t *testing.T) {
	t.Parallel()
	for _, loc := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), loc)
		var b strings.Builder
		if err := Coupons(layouts.Page{Title: "c"}, CouponsView{}).Render(ctx, &b); err != nil {
			t.Fatal(err)
		}
		for _, k := range []i18n.Key{i18n.KeyAdminCoupPerCustomer, i18n.KeyAdminCoupPerCustomerHint} {
			if !strings.Contains(b.String(), i18n.T(ctx, k)) {
				t.Errorf("%v coupon form lacks %q", loc, i18n.T(ctx, k))
			}
		}
	}
}

// From TestEveryAdminScreenPutsItsWorkInTheContentColumn.
func TestCouponsScreenPutsItsWorkInTheContentColumn(t *testing.T) {
	t.Parallel()
	ctx := layouts.WithStaff(i18n.WithLocale(t.Context(), i18n.ZhHant), true)

	name := "coupons"
	page := renderComponent(t, ctx, Coupons(layouts.Page{}, CouponsView{}))

	frame := strings.Index(page, `<div class="goen-admin">`)
	rail := strings.Index(page, "goen-admin__nav")
	column := strings.Index(page, `class="goen-admin__main"`)
	work := strings.Index(page, `class="ui-page-head"`)
	if frame < 0 || rail < 0 || column < 0 || work < 0 {
		t.Fatalf("%s: rendered no back-office frame at all (frame=%d rail=%d "+
			"column=%d work=%d); the order below would prove nothing",
			name, frame, rail, column, work)
	}
	if frame > rail || rail > column || column > work {
		t.Errorf("%s: the frame reads frame=%d rail=%d column=%d work=%d — the "+
			"work has to open INSIDE the content column, or it is a sibling of "+
			"the rail and the grid puts the rail on a row of its own",
			name, frame, rail, column, work)
	}
	if n := strings.Count(page, `class="goen-admin__main"`); n != 1 {
		t.Errorf("%s: %d content columns, want exactly 1 — a second one is a "+
			"second grid child and the same defect one level down", name, n)
	}
}

// From TestEveryAdminEmptyPageKeepsItsRestartLink.
func TestCouponsEmptyPageKeepsItsRestartLink(t *testing.T) {
	t.Parallel()
	b := keyset.Bound{PastEnd: true, First: "/admin/products?q=test"}
	var out bytes.Buffer
	if err := Coupons(layouts.Page{Title: "Pagination"}, CouponsView{Bound: b}).Render(i18n.WithLocale(t.Context(), i18n.En), &out); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	if !strings.Contains(body, `href="/admin/products?q=test"`) || !strings.Contains(body, "First page") {
		t.Fatal("empty page lost its plain restart link")
	}
	if strings.Count(body, "There are no entries on this page.") != 1 {
		t.Fatal("empty page lost its shared empty state")
	}
}

// From TestEveryQueueKeepsItsOwnFirstPageEmptyState and
// TestEveryQueueWithMoreRowsOffersNext.
func TestCouponsQueueKeepsItsOwnFirstPageEmptyState(t *testing.T) {
	t.Parallel()
	ctx := func(k i18n.Key) string { return i18n.T(i18n.WithLocale(t.Context(), i18n.En), k) }
	generic := ctx(i18n.KeyPageEmpty)
	_, b := keyset.PageBound("/admin/queue", false, []db.Order{}, keyset.PageSize, func(*db.Order) string { return "" })
	body := renderComponent(t, i18n.WithLocale(t.Context(), i18n.En), Coupons(layouts.Page{Title: "Queue"}, CouponsView{Bound: b}))
	if !strings.Contains(body, html.EscapeString(ctx(i18n.KeyAdminCoupEmpty))) {
		t.Errorf("the queue's own empty text %q is missing", ctx(i18n.KeyAdminCoupEmpty))
	}
	if strings.Contains(body, generic) {
		t.Error("an empty first page shows the later-page sentence")
	}
}

func TestCouponsQueueWithMoreRowsOffersNext(t *testing.T) {
	t.Parallel()
	ctx := func(k i18n.Key) string { return i18n.T(i18n.WithLocale(t.Context(), i18n.En), k) }
	rows := make([]db.Order, keyset.PageLimit)
	_, b := keyset.PageBound("/admin/queue", false, rows, keyset.PageSize,
		func(*db.Order) string { return `{"ID":"12345678-1234-1234-1234-123456789abc"}` })
	body := renderComponent(t, i18n.WithLocale(t.Context(), i18n.En), Coupons(layouts.Page{Title: "Queue"}, CouponsView{Bound: b}))
	if !strings.Contains(body, `rel="next"`) {
		t.Error("a first page with more rows has no Next link")
	}
	if strings.Contains(body, ctx(i18n.KeyPageEmpty)) {
		t.Error("a first page with more rows says it is empty")
	}
}

// From TestAdminExamplesStayOutsideRefusedInputs.
func TestCouponExamplesStayOutsideRefusedInputs(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		for _, draft := range []struct{ value, refusal string }{{"", ""}, {"Submitted <& value", ""}, {"", "Refused"}, {"Submitted <& value", "Refused"}} {
			typed, refusal := draft.value, draft.refusal
			t.Run(locale.Tag()+"/"+typed+"/"+refusal, func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				errors := func(fields ...string) map[string]string {
					if refusal == "" {
						return nil
					}
					result := make(map[string]string, len(fields))
					for _, field := range fields {
						result[field] = refusal
					}
					return result
				}
				prefix := "Example: "
				discount := "Summer sale — NT$200 off everything"
				if locale == i18n.ZhHant {
					prefix = "例："
					discount = "夏季全站折 NT$200"
				}
				p := layouts.Page{}
				for _, page := range []struct {
					name      string
					component templ.Component
					fields    map[string]string
				}{
					{"coupon", Coupons(p, CouponsView{Draft: CouponDraft{Code: typed, Description: typed, PerCustomer: typed}, Errors: errors("code", "description", "percustomer")}), map[string]string{"c-code": prefix + "SUMMER20", "c-description": prefix + discount, "c-percustomer": prefix + "1"}},
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
								t.Errorf("refused %s has no visible linked example %q", id, example)
							}
						}
					})
				}
			})
		}
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
