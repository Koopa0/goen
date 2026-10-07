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

func TestReturnIntentGroupsEachReturnState(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		status returns.Status
		want   components.Intent
	}{
		{returns.StatusRequested, components.IntentWarn},
		{returns.StatusApproved, components.IntentNeutral},
		{returns.StatusRejected, components.IntentNeutral},
		{returns.StatusCompleted, components.IntentDone},
	} {
		if got := (Return{Status: tt.status}).StatusIntent(); got != tt.want {
			t.Errorf("Return{%q}.StatusIntent() = %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestOrderListPillsFollowTheirGroupAndLeaveOutCommitted(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := OrdersView{
		Bound: firstPageBound("orders"),
		Orders: []OrderRow{
			{Number: "GO-A", Status: order.FulfillmentPicking, StatusText: "備貨中", StatusIntent: components.IntentProgress, Committed: true},
			{Number: "GO-B", Status: order.FulfillmentPending, StatusText: "待出貨", StatusIntent: components.IntentWarn, Committed: true},
			{Number: "GO-C", Status: order.FulfillmentCompleted, StatusText: "已完成", StatusIntent: components.IntentDone, Committed: true},
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
	if got := badgeClasses(t, page, i18n.T(ctx, i18n.KeyAdminQueueCommitted)); len(got) == 0 {
		t.Error("the order page lost its committed pill")
	}
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
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := ReturnsView{Rows: []Return{
		{ID: "r1", OrderNumber: "GO-1", Status: returns.StatusRequested, StatusText: "待處理", Window: "within"},
		{ID: "r2", OrderNumber: "GO-2", Status: returns.StatusCompleted, StatusText: "已完成", Window: "within", Decided: true},
	}}
	page := renderComponent(t, ctx, Returns(layouts.Page{}, view))
	wantBadge(t, page, "待處理", "goen-badge goen-badge--warn")
	wantBadge(t, page, "已完成", "goen-badge goen-badge--done")
}
