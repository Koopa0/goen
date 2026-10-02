package admin

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

// TestTheDashboardOpensEveryOrderItLists holds the landing page's reason for
// carrying a queue at all. A row that names an order and does not open it makes
// a staff member copy the number into the order screen's search box, which is
// the work the dashboard exists to remove.
func TestTheDashboardOpensEveryOrderItLists(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	t.Run("a listed order is a link to itself", func(t *testing.T) {
		t.Parallel()
		html := renderToString(t, Dashboard(Meta(ctx), DashboardView{
			Recent: []OrderRow{{
				Number:     "GO-260918-000001",
				Status:     pages.FulfillmentShipped,
				StatusText: "已出貨",
				PlacedAt:   "2026-09-18 10:00",
				Recipient:  "版面顧客",
				TotalCents: 149900,
			}},
		}))
		if !strings.Contains(html, `href="/admin/orders/GO-260918-000001"`) {
			t.Error("the queue names an order it does not open")
		}
		if !strings.Contains(html, "GO-260918-000001") {
			t.Fatal("the queue does not list the order at all")
		}
	})

	t.Run("an empty queue says so rather than showing an empty table", func(t *testing.T) {
		t.Parallel()
		html := renderToString(t, Dashboard(Meta(ctx), DashboardView{}))
		if !strings.Contains(html, i18n.T(ctx, i18n.KeyAdminQueueNoneYet)) {
			t.Error("an order-less dashboard does not say there are no orders yet")
		}
	})
}

// TestTheDashboardCountsWhatHasAClockOrAPersonWaiting holds the tiles a shift
// starts from. A return request runs against the seven-day right of rescission,
// and a question is a customer waiting; both used to be reachable only from the
// navigation, so a count on this page was the one place nobody would look.
func TestTheDashboardCountsWhatHasAClockOrAPersonWaiting(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	html := renderToString(t, Dashboard(Meta(ctx), DashboardView{
		PendingReturns: 3, ReturnsDeadline: "2026-10-05", UnansweredQuestions: 7,
	}))
	for _, want := range []string{
		`href="/admin/returns"`, i18n.T(ctx, i18n.KeyAdminQueueStatReturns),
		`href="/admin/questions"`, i18n.T(ctx, i18n.KeyAdminQueueStatQuestions),
		"2026-10-05", `<span class="goen-stat__value">3</span>`, `<span class="goen-stat__value">7</span>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the dashboard does not carry %q", want)
		}
	}

	t.Run("no deadline is named when nothing is waiting or nothing has a clock", func(t *testing.T) {
		t.Parallel()
		for name, v := range map[string]DashboardView{
			"nothing waiting": {ReturnsDeadline: "2026-10-05"},
			"no clock":        {PendingReturns: 2},
		} {
			if note := v.ReturnsDeadlineNote(ctx); note != "" {
				t.Errorf("%s: the returns tile names a deadline anyway: %q", name, note)
			}
			if strings.Contains(renderToString(t, Dashboard(Meta(ctx), v)), "goen-stat__note") {
				t.Errorf("%s: the dashboard renders an empty deadline line", name)
			}
		}
	})
}
