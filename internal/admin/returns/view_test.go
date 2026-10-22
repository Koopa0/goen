package returns

import (
	"bytes"
	"fmt"
	stdhtml "html"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/order/returns"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/widget"
)

// The tests in this file are the returns halves of tests that render several
// desks' pages: each half lives with the page it renders.

// badgeClasses returns the class attribute of every badge whose text contains word.
func badgeClasses(t *testing.T, page, word string) []string {
	t.Helper()
	root, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var out []string
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			class := attr(n, "class")
			if strings.HasPrefix(class, "goen-badge") && strings.Contains(textOf(n), word) {
				out = append(out, class)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}

func wantBadge(t *testing.T, page, word, class string) {
	t.Helper()
	got := badgeClasses(t, page, word)
	if len(got) == 0 {
		t.Errorf("no badge says %q", word)
		return
	}
	for _, c := range got {
		if c != class {
			t.Errorf("badge %q has class %q, want %q", word, c, class)
		}
	}
}

// From statusbadge_test.go, moved whole.
func TestEveryReturnStatusHasAGroup(t *testing.T) {
	t.Parallel()
	for _, status := range returns.Statuses {
		row := &Return{Status: status}
		if status == returns.StatusApproved {
			row.Lines = []ReturnLine{{}}
		}
		if status == returns.StatusRejected {
			continue
		}
		if row.StatusIntent() == widget.IntentNeutral {
			t.Errorf("%q falls to neutral, the group of states nobody acts on", status)
		}
	}
}

// From statusbadge_test.go, moved whole.
func TestReturnIntentGroupsEachReturnState(t *testing.T) {
	t.Parallel()
	inspected := []ReturnLine{{Inspected: true}}
	waiting := []ReturnLine{{Inspected: false}}
	for _, tt := range []struct {
		name string
		row  Return
		want widget.Intent
	}{
		{"requested", Return{Status: returns.StatusRequested}, widget.IntentWarn},
		{"approved, goods not back", Return{Status: returns.StatusApproved, Lines: waiting}, widget.IntentProgress},
		{"approved, fully inspected", Return{Status: returns.StatusApproved, Lines: inspected}, widget.IntentWarn},
		{"approved, payout outstanding", Return{Status: returns.StatusApproved, Lines: waiting, Decided: true, PayoutOutstanding: true}, widget.IntentWarn},
		{"approved, payout stranded", Return{Status: returns.StatusApproved, Lines: waiting, Decided: true, PayoutOutstanding: true, PayoutBlocked: true}, widget.IntentDanger},
		{"declined", Return{Status: returns.StatusRejected}, widget.IntentNeutral},
		{"completed", Return{Status: returns.StatusCompleted}, widget.IntentDone},
		{"cancelled and refunded", Return{Status: returns.StatusCompleted, BeforeShipment: true}, widget.IntentNeutral},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.row.StatusIntent(); got != tt.want {
				t.Errorf("StatusIntent() = %q, want %q", got, tt.want)
			}
		})
	}
}

// From statusbadge_test.go, moved whole.
func TestReturnQueueBadgesFollowTheirGroup(t *testing.T) {
	t.Parallel()
	inspected := []ReturnLine{{Inspected: true}}
	waiting := []ReturnLine{{}}
	for _, locale := range []struct {
		locale i18n.Locale
		words  [8]string
	}{
		{i18n.ZhHant, [8]string{"待處理", "退回中", "待結案", "待重新退款", "退款失敗", "未同意", "已完成", "已取消並退款"}},
		{i18n.En, [8]string{"Open", "On its way back", "Ready to close", "Refund to resend", "Refund failed", "Declined", "Completed", "Cancelled and refunded"}},
	} {
		t.Run(string(locale.locale), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale.locale)
			w := locale.words
			rows := []struct {
				row   Return
				word  string
				class string
			}{
				{Return{Status: returns.StatusRequested, StatusText: w[0]}, w[0], "goen-badge goen-badge--warn"},
				{Return{Status: returns.StatusApproved, Lines: waiting, StatusText: "x1"}, w[1], "goen-badge goen-badge--progress"},
				{Return{Status: returns.StatusApproved, Lines: inspected, StatusText: "x2"}, w[2], "goen-badge goen-badge--warn"},
				{Return{Status: returns.StatusApproved, Lines: waiting, Decided: true, PayoutOutstanding: true, StatusText: "x3"}, w[3], "goen-badge goen-badge--warn"},
				{Return{Status: returns.StatusApproved, Lines: waiting, Decided: true, PayoutOutstanding: true, PayoutBlocked: true, StatusText: "x4"}, w[4], "goen-badge goen-badge--danger"},
				{Return{Status: returns.StatusRejected, StatusText: w[5]}, w[5], "goen-badge"},
				{Return{Status: returns.StatusCompleted, StatusText: w[6]}, w[6], "goen-badge goen-badge--done"},
				{Return{Status: returns.StatusCompleted, BeforeShipment: true, StatusText: w[7]}, w[7], "goen-badge"},
			}
			for i, r := range rows {
				r.row.ID = fmt.Sprintf("r%d", i)
				r.row.OrderNumber = fmt.Sprintf("GO-%d", i)
				r.row.Window = "within"
				page := renderComponent(t, ctx, Returns(frame.Page{}, ReturnsView{Rows: []Return{r.row}}))
				wantBadge(t, page, r.word, r.class)
				if got := badgeClasses(t, page, ""); len(got) != 1 {
					t.Errorf("%q: %d badges on a one-row queue, want 1", r.word, len(got))
				}
				for _, stale := range []string{"x1", "x2", "x3", "x4"} {
					if len(badgeClasses(t, page, stale)) != 0 {
						t.Errorf("%q: a badge shows the store's word %q", r.word, stale)
					}
				}
			}
		})
	}
}

// From refundconfirm_test.go, moved whole.
func TestARefundBeforeShipmentIsFinishedOnItsOrderPage(t *testing.T) {
	t.Parallel()
	r := &Return{
		ID: "return", OrderNumber: "GO-260929-000001", Status: "approved", Decided: true,
		BeforeShipment: true, PayoutOutstanding: true, Window: "undelivered",
		Lines: []ReturnLine{{OrderLineID: "line", Quantity: 1}},
	}
	if r.AwaitingGoods() || r.CanComplete() {
		t.Fatal("the returns queue offers to inspect or close goods that never shipped")
	}
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	var body strings.Builder
	if err := Returns(frame.Page{Title: "returns"}, ReturnsView{Rows: []Return{*r}}).Render(ctx, &body); err != nil {
		t.Fatal(err)
	}
	page := body.String()
	if !strings.Contains(page, `href="/admin/orders/GO-260929-000001"`) ||
		strings.Contains(page, r.Action()) || strings.Contains(page, r.InspectAction()) {
		t.Fatalf("a refund before shipment is not sent to its order page: %s", page)
	}
}

// From refusal_navigation_test.go, moved whole.
func TestRefusedInspectionKeepsBlankAndUnreadableCounts(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"", " +oops "} {
		view := ReturnsView{Rows: []Return{{ID: "r1", Status: "approved", Decided: true, Window: "within", Lines: []ReturnLine{{OrderLineID: "l1", Quantity: 2, Restockable: true, DraftReceived: raw, DraftRestocked: raw}}}}, Errors: map[string]string{"r1.inspect": "bad count"}}
		body := renderToString(t, Returns(frame.Page{}, view))
		doc, err := html.Parse(strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"recv-r1-l1", "stock-r1-l1"} {
			for n := range doc.Descendants() {
				if refusalAttr(n, "id") == id {
					if refusalAttr(n, "value") != raw {
						t.Errorf("%s lost raw count %q", id, raw)
					}
					if raw != "" && refusalAttr(n, "type") == "number" {
						t.Errorf("%s lets the browser discard unreadable input", id)
					}
				}
			}
		}
		if !strings.Contains(body, `action="/admin/returns/r1/inspect#inspect-r1"`) || !strings.Contains(body, `href="#recv-r1-l1"`) {
			t.Error("inspection has no return to its first refused field")
		}
	}
}

// From TestProductView_VariantRefusal, which keeps the product half.
func TestInspectionRefusalsCountEachRefusalOnce(t *testing.T) {
	t.Parallel()
	view := ReturnsView{
		Rows: []Return{{ID: "r1", Lines: []ReturnLine{
			{OrderLineID: "l1", Restockable: true}, {OrderLineID: "l2", Restockable: true},
		}}},
		Errors: map[string]string{"r1.inspect": "bad count"},
	}
	got := view.InspectionRefusals()
	if len(got) != 1 || got[0].Count != 1 || got[0].First != "recv-r1-l1" {
		t.Errorf("InspectionRefusals() = %+v, want one refusal at recv-r1-l1", got)
	}
}

func refusalAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// From TestAuditEmptyPageRestartLink.
func TestReturnsEmptyPageKeepsItsRestartLink(t *testing.T) {
	t.Parallel()
	b := keyset.Bound{PastEnd: true, First: "/admin/products?q=test"}
	p := frame.Page{Title: "Pagination"}
	cases := map[string]templ.Component{
		"returns": Returns(p, ReturnsView{Bound: b}),
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
func TestReturnsQueueKeepsItsOwnFirstPageEmptyState(t *testing.T) {
	t.Parallel()
	ctx := func(k i18n.Key) string { return i18n.T(i18n.WithLocale(t.Context(), i18n.En), k) }
	generic := ctx(i18n.KeyPageEmpty)
	_, b := keyset.PageBound("/admin/queue", false, []db.Order{}, keyset.PageSize, func(*db.Order) string { return "" })
	p := frame.Page{Title: "Queue"}
	for name, c := range map[string]struct {
		page templ.Component
		want string
	}{
		"returns": {Returns(p, ReturnsView{Bound: b}), ctx(i18n.KeyAdminRetEmpty)},
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
func TestReturnsQueueWithMoreRowsOffersNext(t *testing.T) {
	t.Parallel()
	ctx := func(k i18n.Key) string { return i18n.T(i18n.WithLocale(t.Context(), i18n.En), k) }
	rows := make([]db.Order, keyset.PageLimit)
	_, b := keyset.PageBound("/admin/queue", false, rows, keyset.PageSize,
		func(*db.Order) string { return `{"ID":"12345678-1234-1234-1234-123456789abc"}` })
	p := frame.Page{Title: "Queue"}
	for name, page := range map[string]templ.Component{
		"returns": Returns(p, ReturnsView{Bound: b}),
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
func TestReturnExamplesStayOutsideRefusedInputs(t *testing.T) {
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
				inspection := "For example: box damaged, accessories missing"
				if locale == i18n.ZhHant {
					inspection = "例如：外盒破損、配件缺少"
				}
				p := frame.Page{}
				for _, page := range []struct {
					name      string
					component templ.Component
					fields    map[string]string
				}{
					{"return inspection", Returns(p, ReturnsView{Rows: []Return{{ID: "request", Status: returns.StatusApproved, Window: returns.WindowStatutory, Decided: true, Lines: []ReturnLine{{OrderLineID: "line", Quantity: 1, DraftNote: typed}}}}, Errors: errors("request.inspect")}), map[string]string{"note-request-line": inspection}},
				} {
					t.Run(page.name, func(t *testing.T) {
						markup := renderComponent(t, ctx, page.component)
						z := html.NewTokenizer(strings.NewReader(markup))
						for z.Next() != html.ErrorToken {
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
						doc, err := html.Parse(strings.NewReader(markup))
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

func exampleElementByID(n *html.Node, id string) *html.Node {
	if n.Type == html.ElementNode && attr(n, "id") == id {
		return n
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if found := exampleElementByID(child, id); found != nil {
			return found
		}
	}
	return nil
}
