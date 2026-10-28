package orders

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/order"
)

func numberN(n int) string { return fmt.Sprintf("GO-261029-%06d", n) }

func TestSelectedNumbers(t *testing.T) {
	t.Parallel()
	a, b := numberN(1), numberN(2)
	many := make([]string, keyset.PageSize+1)
	for i := range many {
		many[i] = numberN(i)
	}
	for _, tt := range []struct {
		name    string
		values  []string
		want    []string
		wantErr bool
	}{
		{"none", nil, []string{}, false},
		{"duplicates collapse, first place kept", []string{b, a, b, a}, []string{b, a}, false},
		{"junk is dropped", []string{"", "x", a, "go-261029-000002", a + " ", "' OR 1=1", b}, []string{a, b}, false},
		{"a page is allowed", many[:keyset.PageSize], many[:keyset.PageSize], false},
		{"one more than a page is refused", many, nil, true},
		{"repeats of one number are not a page", slices.Repeat([]string{a}, 200), []string{a}, false},
	} {
		got, err := selectedNumbers(tt.values)
		if (err != nil) != tt.wantErr {
			t.Errorf("%s: err = %v, want error %t", tt.name, err, tt.wantErr)
			continue
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("%s: numbers = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// The order's own form and the batch answer by the same reading of what
// Advance returned, so a refusal one knows is a refusal the other knows.
func TestClassifyAdvance(t *testing.T) {
	t.Parallel()
	constraint := func(name string) error {
		return fmt.Errorf("%w: %w", ErrRefused, &pgconn.PgError{Code: "23514", ConstraintName: name})
	}
	for _, tt := range []struct {
		name string
		err  error
		want advanceOutcome
	}{
		{"moved", nil, advanced},
		{"paid cancel by the store", ErrPaidCancel, advancePaidCancel},
		{"paid cancel by the database", constraint("orders_paid_cancel_needs_refund"), advancePaidCancel},
		{"unfunded", constraint("orders_funded_to_leave_pending"), advanceUnfunded},
		{"owes a parcel", constraint("orders_finished_when_shipped"), advanceOwesParcel},
		{"another rule", constraint("orders_legal_transition"), advanceRefused},
		{"already there", fmt.Errorf("%w: %w", ErrAlreadyThere, ErrRefused), advanceRefused},
		{"cancelled", fmt.Errorf("%w: %w", ErrCancelled, constraint("orders_legal_transition")), advanceRefused},
		{"no such order", ErrRefused, advanceRefused},
		{"a lock timeout is a failure", &pgconn.PgError{Code: "55P03"}, advanceFailed},
	} {
		if got := classifyAdvance(tt.err); got != tt.want {
			t.Errorf("%s: outcome = %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestBatchReason(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	legal := &pgconn.PgError{Code: "23514", ConstraintName: "orders_legal_transition"}
	unfunded := &pgconn.PgError{Code: "23514", ConstraintName: "orders_funded_to_leave_pending"}
	for _, tt := range []struct {
		name string
		err  error
		want string
	}{
		{"cancelled", fmt.Errorf("%w: %w: %w", ErrCancelled, ErrRefused, legal), "已取消"},
		{"unpaid", fmt.Errorf("%w: %w", ErrRefused, unfunded), "還沒付清"},
		{"changed", fmt.Errorf("%w: %w", ErrRefused, legal), "狀態已經變了"},
		{"missing", ErrRefused, "狀態已經變了"},
	} {
		if got := batchReason(ctx, tt.err); got != tt.want {
			t.Errorf("%s: reason = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// post sends the form to the handler with a store that is never reached: every
// request here is answered before the database is asked anything.
// A failure partway through leaves the orders before it committed: the answer
// names the one that got no answer and the ones never tried, and counts those
// that did start.
func TestStartPacking_StopsAtAFailure(t *testing.T) {
	t.Parallel()
	nums := []string{numberN(1), numberN(2), numberN(3), numberN(4), numberN(5)}
	timedOut := &pgconn.PgError{Code: "55P03"}
	var tried []string
	advance := func(_ context.Context, number string) error {
		tried = append(tried, number)
		switch number {
		case nums[1]:
			return fmt.Errorf("%w: %w", ErrRefused, &pgconn.PgError{Code: "23514", ConstraintName: "orders_funded_to_leave_pending"})
		case nums[2]:
			return fmt.Errorf("lock order: %w", timedOut)
		}
		return nil
	}
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	batch := startPacking(ctx, slog.New(slog.DiscardHandler), nums, advance)

	if !slices.Equal(tried, nums[:3]) {
		t.Errorf("tried %v, want the loop to stop at the third", tried)
	}
	if !batch.Interrupted || batch.OthersStarted != 1 {
		t.Errorf("interrupted %t, started %d, want true and 1", batch.Interrupted, batch.OthersStarted)
	}
	want := []BatchRefusal{
		{nums[1], "還沒付清"},
		{nums[2], "沒有回應，請再按一次「開始備貨」。"},
		{nums[3], "還沒處理"},
		{nums[4], "還沒處理"},
	}
	if !slices.Equal(batch.Refused, want) {
		t.Errorf("refused = %v, want %v", batch.Refused, want)
	}

	zh := renderComponent(t, ctx, Orders(Meta(ctx), func() View { v := workView(QueueReady, nums...); v.Batch = batch; return v }()))
	for _, line := range []string{
		nums[1] + "：還沒付清", nums[2] + "：沒有回應，請再按一次「開始備貨」。",
		nums[3] + "：還沒處理", nums[4] + "：還沒處理", "其他 1 筆已開始備貨。",
	} {
		if !strings.Contains(zh, line) {
			t.Errorf("zh-Hant page lacks %q", line)
		}
	}
	enCtx := i18n.WithLocale(t.Context(), i18n.En)
	en := startPacking(enCtx, slog.New(slog.DiscardHandler), nums, advance)
	if got := en.Refused[1].Reason; got != "No answer; press Start packing again." {
		t.Errorf("en reason = %q", got)
	}
}

func TestStartPacking_NoFailureNoStop(t *testing.T) {
	t.Parallel()
	nums := []string{numberN(1), numberN(2)}
	batch := startPacking(t.Context(), slog.New(slog.DiscardHandler), nums, func(_ context.Context, number string) error {
		if number == nums[0] {
			return fmt.Errorf("%w: %w", ErrAlreadyThere, ErrRefused)
		}
		return nil
	})
	if batch.Interrupted || len(batch.Refused) != 0 || batch.OthersStarted != 2 {
		t.Errorf("batch = %+v, want two started and nothing refused", batch)
	}
}

func postBatch(t *testing.T, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/admin/orders/picking", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	NewHandler(&Store{}, nil, slog.New(slog.DiscardHandler)).StartPicking(w, r)
	return w
}

func TestHandler_StartPickingEdges(t *testing.T) {
	t.Parallel()
	over := url.Values{}
	for i := range keyset.PageSize + 1 {
		over.Add("order", numberN(i))
	}
	for _, tt := range []struct {
		name     string
		values   url.Values
		code     int
		location string
	}{
		{"nothing ticked", url.Values{}, http.StatusSeeOther, "/admin/orders?status=ready&nosel=1"},
		{"only junk", url.Values{"order": {"x", "' OR 1=1", "https://evil.example/"}}, http.StatusSeeOther, "/admin/orders?status=ready&nosel=1"},
		{"more than a page", over, http.StatusBadRequest, ""},
	} {
		w := postBatch(t, tt.values)
		if w.Code != tt.code || w.Header().Get("Location") != tt.location {
			t.Errorf("%s: %d %q, want %d %q", tt.name, w.Code, w.Header().Get("Location"), tt.code, tt.location)
		}
	}
}

func getSlips(t *testing.T, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	w := httptest.NewRecorder()
	NewHandler(&Store{}, nil, slog.New(slog.DiscardHandler)).PickingSlips(w, r)
	return w
}

// Pressing the print button with nothing ticked sends no order, and must not
// print the whole queue.
func TestHandler_PickingSlipsSelection(t *testing.T) {
	t.Parallel()
	over := url.Values{selectionParam: {"1"}}
	for i := range keyset.PageSize + 1 {
		over.Add("order", numberN(i))
	}
	for _, tt := range []struct {
		name, target string
		code         int
		location     string
	}{
		{"the button with nothing ticked", "/admin/orders/picking/slips?" + selectionParam + "=1", http.StatusSeeOther, "/admin/orders?status=picking&nosel=1"},
		{"orders that are not numbers", "/admin/orders/picking/slips?order=x&order=&" + selectionParam + "=1", http.StatusSeeOther, "/admin/orders?status=picking&nosel=1"},
		{"more than a page", "/admin/orders/picking/slips?" + over.Encode(), http.StatusBadRequest, ""},
	} {
		w := getSlips(t, tt.target)
		if w.Code != tt.code || w.Header().Get("Location") != tt.location {
			t.Errorf("%s: %d %q, want %d %q", tt.name, w.Code, w.Header().Get("Location"), tt.code, tt.location)
		}
	}
}

func parseHTML(t *testing.T, doc string) *html.Node {
	t.Helper()
	n, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func findAll(n *html.Node, match func(*html.Node) bool) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && match(n) {
			out = append(out, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return out
}

func plainText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

func workView(status QueueFilter, numbers ...string) View {
	v := View{Status: status}
	for _, n := range numbers {
		v.Work = append(v.Work, WorkRow{Row: Row{Number: n, Status: order.FulfillmentPending, StatusText: "x", Recipient: "r"}})
	}
	return v
}

// The two work tabs are each one form: the ready tab posts the ticked orders to
// start packing, the packing tab asks for the slips of the ticked orders. The
// start button is the page's primary action; the print button is a step that
// is not.
func TestOrdersWorkTabsAreBatchForms(t *testing.T) {
	t.Parallel()
	one, two := numberN(1), numberN(2)
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(t.Context(), locale)
		for _, tt := range []struct {
			status         QueueFilter
			method, action string
			button         i18n.Key
			class          string
			named          bool
		}{
			{QueueReady, "post", "/admin/orders/picking", i18n.KeyAdminQueueStartPicking, "goen-btn--primary", false},
			{QueuePicking, "get", "/admin/orders/picking/slips", i18n.KeyAdminBatchPrint, "goen-btn--secondary", true},
		} {
			doc := parseHTML(t, renderComponent(t, ctx, Orders(Meta(ctx), workView(tt.status, one, two))))
			forms := findAll(doc, func(n *html.Node) bool { return n.Data == "form" && attr(n, "class") == "goen-admin__batch" })
			if len(forms) != 1 {
				t.Fatalf("%s %s: %d batch forms, want 1", locale, tt.status, len(forms))
			}
			form := forms[0]
			if attr(form, "method") != tt.method || attr(form, "action") != tt.action {
				t.Errorf("%s %s: form is %s %s, want %s %s", locale, tt.status, attr(form, "method"), attr(form, "action"), tt.method, tt.action)
			}
			boxes := findAll(form, func(n *html.Node) bool { return n.Data == "input" && attr(n, "type") == "checkbox" })
			var values []string
			for _, b := range boxes {
				if attr(b, "name") != "order" || attr(b, "checked") != "" {
					t.Errorf("%s %s: box %v is not an unticked 'order'", locale, tt.status, b.Attr)
				}
				values = append(values, attr(b, "value"))
			}
			if !slices.Equal(values, []string{one, two}) {
				t.Errorf("%s %s: boxes carry %v, want %v", locale, tt.status, values, []string{one, two})
			}
			labels := findAll(form, func(n *html.Node) bool { return n.Data == "label" && attr(n, "class") == "goen-admin__pick" })
			if len(labels) != 2 || plainText(labels[0]) != fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminBatchSelect), one) {
				t.Errorf("%s %s: each box needs a label naming its order, got %d", locale, tt.status, len(labels))
			}
			buttons := findAll(form, func(n *html.Node) bool { return n.Data == "button" })
			if len(buttons) != 1 || attr(buttons[0], "type") != "submit" || plainText(buttons[0]) != i18n.T(ctx, tt.button) {
				t.Fatalf("%s %s: want one submit button reading %q", locale, tt.status, i18n.T(ctx, tt.button))
			}
			if !strings.Contains(" "+attr(buttons[0], "class")+" ", " "+tt.class+" ") {
				t.Errorf("%s %s: button class %q lacks %s", locale, tt.status, attr(buttons[0], "class"), tt.class)
			}
			if got := attr(buttons[0], "name") == selectionParam && attr(buttons[0], "value") == "1"; got != tt.named {
				t.Errorf("%s %s: button marks a selection = %t, want %t", locale, tt.status, got, tt.named)
			}
			if hint := findAll(doc, func(n *html.Node) bool { return attr(n, "id") == attr(buttons[0], "aria-describedby") }); len(hint) != 1 {
				t.Errorf("%s %s: the button's hint is not on the page", locale, tt.status)
			}
		}
	}
}

// Nothing else on the orders desk is a batch: not the other tabs, not a search,
// and not a work tab with nothing on it.
func TestOrdersOtherViewsHaveNoBatchForm(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	other := View{Status: QueueAll, Orders: []Row{{Number: numberN(1), Status: order.FulfillmentPending}}}
	searched := View{Status: QueueReady, Searched: true, Term: "GO-261029", Orders: other.Orders}
	for name, v := range map[string]View{
		"all": other, "a search on the ready tab": searched,
		"an empty ready tab": {Status: QueueReady}, "an empty packing tab": {Status: QueuePicking},
	} {
		out := renderComponent(t, ctx, Orders(Meta(ctx), v))
		if strings.Contains(out, "goen-admin__batch") || strings.Contains(out, `name="order"`) {
			t.Errorf("%s draws a batch form", name)
		}
	}
}

func TestOrdersBatchRefusalNames(t *testing.T) {
	t.Parallel()
	one, two := numberN(1), numberN(2)
	v := workView(QueueReady, one, two)
	v.Batch = BatchResult{
		Refused:       []BatchRefusal{{Number: one, Reason: "已取消"}, {Number: two, Reason: "還沒付清"}},
		OthersStarted: 3,
	}
	zh := renderComponent(t, i18n.WithLocale(t.Context(), i18n.ZhHant), Orders(Meta(t.Context()), v))
	for _, want := range []string{one + "：已取消", two + "：還沒付清", "其他 3 筆已開始備貨。", "這些訂單沒有開始備貨："} {
		if !strings.Contains(zh, want) {
			t.Errorf("zh-Hant page lacks %q", want)
		}
	}
	v.Batch = BatchResult{Refused: v.Batch.Refused[:1], OthersStarted: 1}
	en := renderComponent(t, i18n.WithLocale(t.Context(), i18n.En), Orders(Meta(t.Context()), v))
	for _, want := range []string{one + ": 已取消", "The other 1 order has started packing."} {
		if !strings.Contains(en, want) {
			t.Errorf("en page lacks %q", want)
		}
	}
	none := renderComponent(t, i18n.WithLocale(t.Context(), i18n.En), Orders(Meta(t.Context()), workView(QueueReady, one)))
	if strings.Contains(none, "goen-admin__refusals") {
		t.Error("a batch nobody refused drew a refusal")
	}
}

func TestPickingSelectionTexts(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale    i18n.Locale
		asked, in int
		scope     string
		gone      string
	}{
		{i18n.ZhHant, 3, 3, "選取的 3 筆", ""},
		{i18n.ZhHant, 3, 2, "選取的 3 筆", "選取的 3 筆中有 1 筆已沒有待出貨的商品，沒有列出。"},
		{i18n.En, 1, 0, "1 selected order", "1 of the 1 selected orders has nothing left to ship and is not listed."},
		{i18n.En, 4, 1, "4 selected orders", "3 of the 4 selected orders have nothing left to ship and are not listed."},
	} {
		ctx := i18n.WithLocale(t.Context(), tt.locale)
		v := PickingView{Selection: &PickingSelection{Asked: tt.asked, Listed: tt.in}}
		if got := v.ScopeText(ctx); got != tt.scope {
			t.Errorf("%s %d/%d: scope = %q, want %q", tt.locale, tt.in, tt.asked, got, tt.scope)
		}
		if got := v.GoneText(ctx); got != tt.gone {
			t.Errorf("%s %d/%d: gone = %q, want %q", tt.locale, tt.in, tt.asked, got, tt.gone)
		}
	}
	whole := PickingView{}
	if got := whole.GoneText(t.Context()); got != "" {
		t.Errorf("the whole queue says %q is gone", got)
	}
	if !strings.Contains(whole.ScopeText(i18n.WithLocale(t.Context(), i18n.En)), strconv.Itoa(keyset.PageSize)) {
		t.Error("the whole queue lost its page size sentence")
	}
}

// The slips of a selection carry no pager and no link to a next batch: the
// numbers are the whole request.
func TestPickingOfSelectionHasNoPaging(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	v := &PickingView{
		Selection: &PickingSelection{Asked: 2, Listed: 1},
		Slips:     []*OrderView{{OutstandingQuantities: true, Number: numberN(1)}},
	}
	out := renderComponent(t, ctx, Picking(Meta(ctx), v))
	for _, want := range []string{"2 selected orders", "1 of the 2 selected orders has nothing left to ship and is not listed."} {
		if !strings.Contains(out, want) {
			t.Errorf("selected slips lack %q", want)
		}
	}
	if strings.Contains(out, keyset.Param+"=") || strings.Contains(out, i18n.T(ctx, i18n.KeyAdminPickingScope)[:20]) {
		t.Error("selected slips page or scope as if they were the whole queue")
	}
}

// A refused batch is a refused page only when no order started: with some started
// the page reports work that went through, and an interrupted batch answers 503.
func TestBatchResult_SavedNothing(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		batch BatchResult
		want  bool
	}{
		{"none started", BatchResult{}, true},
		{"some started", BatchResult{OthersStarted: 2}, false},
		{"interrupted before any started", BatchResult{Interrupted: true}, false},
		{"interrupted after some started", BatchResult{Interrupted: true, OthersStarted: 1}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.batch.savedNothing(); got != tt.want {
				t.Errorf("savedNothing() = %v, want %v", got, tt.want)
			}
		})
	}
}
