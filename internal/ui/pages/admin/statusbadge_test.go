package admin

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/returns"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/user"
)

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

func textOf(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(textOf(c))
	}
	return strings.TrimSpace(b.String())
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

func TestFulfillmentIntentGroupsEachOrderState(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		status    order.FulfillmentStatus
		committed bool
		owed      int64
		want      components.Intent
	}{
		{"waiting for the customer", order.FulfillmentPending, false, 100, components.IntentNeutral},
		{"paid, waiting for the shop", order.FulfillmentPending, true, 0, components.IntentWarn},
		{"paid by credit alone", order.FulfillmentPending, false, 0, components.IntentWarn},
		{"picking", order.FulfillmentPicking, true, 0, components.IntentProgress},
		{"shipped", order.FulfillmentShipped, true, 0, components.IntentProgress},
		{"delivered", order.FulfillmentDelivered, true, 0, components.IntentDone},
		{"completed", order.FulfillmentCompleted, true, 0, components.IntentDone},
		{"cancelled", order.FulfillmentCancelled, false, 0, components.IntentNeutral},
		{"a retired state", order.FulfillmentStatus("retired"), false, 0, components.IntentNeutral},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := FundedFulfillmentIntent(tt.status, tt.committed, tt.owed); got != tt.want {
				t.Errorf("FundedFulfillmentIntent(%q, %t, %d) = %q, want %q", tt.status, tt.committed, tt.owed, got, tt.want)
			}
		})
	}
}

func TestEveryFulfillmentStateHasAGroup(t *testing.T) {
	t.Parallel()
	for _, s := range order.FulfillmentStatuses {
		if s == order.FulfillmentCancelled || s == order.FulfillmentPending {
			continue
		}
		if FundedFulfillmentIntent(s, true, 0) == components.IntentNeutral {
			t.Errorf("%q falls to neutral, the group of states nobody acts on", s)
		}
	}
}

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
		if row.StatusIntent() == components.IntentNeutral {
			t.Errorf("%q falls to neutral, the group of states nobody acts on", status)
		}
	}
}

func TestReturnIntentGroupsEachReturnState(t *testing.T) {
	t.Parallel()
	inspected := []ReturnLine{{Inspected: true}}
	waiting := []ReturnLine{{Inspected: false}}
	for _, tt := range []struct {
		name string
		row  Return
		want components.Intent
	}{
		{"requested", Return{Status: returns.StatusRequested}, components.IntentWarn},
		{"approved, goods not back", Return{Status: returns.StatusApproved, Lines: waiting}, components.IntentProgress},
		{"approved, fully inspected", Return{Status: returns.StatusApproved, Lines: inspected}, components.IntentWarn},
		{"approved, payout outstanding", Return{Status: returns.StatusApproved, Lines: waiting, Decided: true, PayoutOutstanding: true}, components.IntentWarn},
		{"approved, payout stranded", Return{Status: returns.StatusApproved, Lines: waiting, Decided: true, PayoutOutstanding: true, PayoutBlocked: true}, components.IntentDanger},
		{"declined", Return{Status: returns.StatusRejected}, components.IntentNeutral},
		{"completed", Return{Status: returns.StatusCompleted}, components.IntentDone},
		{"cancelled and refunded", Return{Status: returns.StatusCompleted, BeforeShipment: true}, components.IntentNeutral},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.row.StatusIntent(); got != tt.want {
				t.Errorf("StatusIntent() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOrderListPillsFollowTheirGroupAndLeaveOutCommitted(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := OrdersView{
		Bound: firstPageBound("orders"),
		Orders: []OrderRow{
			{Number: "GO-A", Status: order.FulfillmentPicking, StatusText: "備貨中", StatusIntent: components.IntentProgress},
			{Number: "GO-B", Status: order.FulfillmentPending, StatusText: "待出貨", StatusIntent: components.IntentWarn},
			{Number: "GO-C", Status: order.FulfillmentCompleted, StatusText: "已完成", StatusIntent: components.IntentDone},
		},
	}
	page := renderComponent(t, ctx, Orders(layouts.Page{}, view))
	wantBadge(t, page, "備貨中", "goen-badge goen-badge--progress")
	wantBadge(t, page, "待出貨", "goen-badge goen-badge--warn")
	wantBadge(t, page, "已完成", "goen-badge goen-badge--done")
	if got := badgeClasses(t, page, i18n.T(ctx, i18n.KeyAdminQueueCommitted)); len(got) != 0 {
		t.Errorf("the list repeats the committed pill on every row: %v", got)
	}
}

func TestOrderPageHeadKeepsItsGroupAndCommitted(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := &OrderView{Number: "GO-A", Status: order.FulfillmentShipped, StatusText: "已出貨", StatusIntent: components.IntentProgress, Committed: true}
	page := renderComponent(t, ctx, Order(layouts.Page{}, view))
	wantBadge(t, page, "已出貨", "goen-badge goen-badge--progress")
	wantBadge(t, page, i18n.T(ctx, i18n.KeyAdminQueueCommitted), "goen-badge")
}

func TestHealthItemsAreDoneOrNeedYou(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	// The outbox is healthy by default and the recommendation projection, never built, is not.
	page := renderComponent(t, ctx, Health(layouts.Page{}, &WorkerHealthView{}))
	wantBadge(t, page, i18n.T(ctx, i18n.KeyAdminHPAllClear), "goen-badge goen-badge--done")
	wantBadge(t, page, i18n.T(ctx, i18n.KeyAdminHPNeedsLook), "goen-badge goen-badge--warn")
}

func TestStaffEnrollmentBadgesAreDoneOrNeedYou(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := StaffView{Rows: []StaffRow{
		{ID: "on", Role: user.RoleStaff, Enrolled: true},
		{ID: "off", Role: user.RoleStaff},
	}}
	page := renderComponent(t, ctx, Staff(layouts.Page{}, view))
	wantBadge(t, page, i18n.T(ctx, i18n.KeyAdminTOTPOn), "goen-badge goen-badge--done")
	wantBadge(t, page, i18n.T(ctx, i18n.KeyAdminTOTPOff), "goen-badge goen-badge--warn")
}

func TestQuestionQueueWaitingNeedsYouAndTheCountIsPlainText(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := QuestionsView{Rows: []Question{{ID: "q", ProductName: "P"}}}
	page := renderComponent(t, ctx, Questions(layouts.Page{}, view))
	wantBadge(t, page, i18n.T(ctx, i18n.KeyAdminQWaiting), "goen-badge goen-badge--warn")
	count := fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminQuestionsWaiting), "1")
	if !strings.Contains(page, `<p class="goen-admin__headcount">`+count+`</p>`) {
		t.Errorf("the waiting count %q is not plain text in the page head", count)
	}
	if got := badgeClasses(t, page, count); len(got) != 0 {
		t.Errorf("the count is still a badge: %v", got)
	}
}

func TestMessageQueueWaitingAndOverdueNeedYouAndOverdueSaysSo(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	fresh := Message{ID: "fresh", SubjectLabel: "S1", WaitingDays: 0}
	late := Message{ID: "late", SubjectLabel: "S2", WaitingDays: 4}
	view := MessagesView{Bound: firstPageBound("messages"), Rows: []Message{fresh, late}}
	page := renderComponent(t, ctx, Messages(layouts.Page{}, view))
	wantBadge(t, page, fresh.Waiting(ctx), "goen-badge goen-badge--warn")
	wantBadge(t, page, late.OverdueText(ctx), "goen-badge goen-badge--warn")
	if !strings.Contains(late.OverdueText(ctx), "逾時") {
		t.Errorf("overdue text %q does not say 逾時", late.OverdueText(ctx))
	}
	if !strings.Contains(page, `<span aria-hidden="true">▲</span>`) {
		t.Error("the overdue badge has no ▲")
	}
	if strings.Count(page, "▲") != 1 {
		t.Errorf("▲ appears %d times, want it on the overdue row only", strings.Count(page, "▲"))
	}
	count := fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminMessagesOpen), view.OpenCountText())
	if !strings.Contains(page, `<p class="goen-admin__headcount">`+count+`</p>`) {
		t.Errorf("the open count %q is not plain text in the page head", count)
	}
	if got := badgeClasses(t, page, count); len(got) != 0 {
		t.Errorf("the count is still a badge: %v", got)
	}
}

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
				page := renderComponent(t, ctx, Returns(layouts.Page{}, ReturnsView{Rows: []Return{r.row}}))
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

func TestDashboardAndCustomerRecentOrdersFollowTheirGroup(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	row := OrderRow{Number: "GO-A", Status: order.FulfillmentPending, StatusText: "待出貨", StatusIntent: components.IntentWarn}

	dashboard := renderComponent(t, ctx, Dashboard(Meta(ctx), DashboardView{Recent: []OrderRow{row}}))
	wantBadge(t, dashboard, "待出貨", "goen-badge goen-badge--warn")

	customer := renderComponent(t, ctx, Customer(Meta(ctx), &CustomerView{Orders: 1, Recent: []OrderRow{row}}))
	wantBadge(t, customer, "待出貨", "goen-badge goen-badge--warn")
}
