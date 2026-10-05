package admin

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/pickup"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestAdminOrdersEmptyCopyMatchesTheQueueContext(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	render := func(v OrdersView) string {
		return renderToString(t, Orders(OrdersMeta(ctx), v))
	}

	t.Run("a search miss names the term and not the status filter", func(t *testing.T) {
		t.Parallel()
		html := render(OrdersView{Term: "GO-MISSING", Searched: true})
		want := i18n.T(ctx, i18n.KeyAdminQueueNoneFound)
		if !strings.Contains(html, fmt.Sprintf(want, "GO-MISSING")) {
			t.Fatalf("search miss HTML lacks %q", fmt.Sprintf(want, "GO-MISSING"))
		}
		if strings.Contains(html, i18n.T(ctx, i18n.KeyAdminQueueEmpty)) {
			t.Error("a search miss still claims the status filter is empty")
		}
	})

	t.Run("an empty all-orders queue says there are none yet", func(t *testing.T) {
		t.Parallel()
		html := render(OrdersView{})
		want := i18n.T(ctx, i18n.KeyAdminQueueNoneYet)
		if !strings.Contains(html, want) {
			t.Fatalf("empty shop HTML lacks %q", want)
		}
		if strings.Contains(html, i18n.T(ctx, i18n.KeyAdminQueueEmpty)) {
			t.Error("an empty shop borrows the status-tab empty copy")
		}
	})

	t.Run("an empty status tab keeps the state-specific copy", func(t *testing.T) {
		t.Parallel()
		html := render(OrdersView{Status: "picking"})
		want := i18n.T(ctx, i18n.KeyAdminQueueEmpty)
		if !strings.Contains(html, want) {
			t.Fatalf("empty status tab HTML lacks %q", want)
		}
	})
}

// TestAPickupOrderCorrectsWithoutAStore locks the shape checkout already
// writes: the chain is the destination, and the store behind it is filled in
// by the carrier's picker. An order placed before one exists carries the chain
// alone, so the correction form must let a staff member save it that way —
// order.Delivery.Validate refuses one of the two and accepts neither.
func TestAPickupOrderCorrectsWithoutAStore(t *testing.T) {
	t.Parallel()
	html := renderToString(t, Order(layouts.Page{Title: "GO-PICKUP"}, &OrderView{
		Number:            "GO-PICKUP",
		Correctable:       true,
		PickupDestination: true,
		PickupChains:      pages.PickupChainChoices(),
		Address:           pages.Delivery{PickupChain: pickup.FamilyMart}.Line(),
		Delivery:          Delivery{PickupChain: pickup.FamilyMart},
	}))

	for _, id := range []string{"d-store-code", "d-store-name"} {
		tag := tagWithID(t, html, id)
		if strings.Contains(tag, "required") {
			t.Errorf("the correction form still demands #%s: %s", id, tag)
		}
	}
	if tag := tagWithID(t, html, "d-chain"); !strings.Contains(tag, "required") {
		t.Errorf("the chain stopped being required: %s", tag)
	}

	chain := pages.Delivery{PickupChain: pickup.FamilyMart}.Line()
	if !strings.Contains(html, chain) {
		t.Errorf("the order detail does not name the chain %q", chain)
	}
	if strings.Contains(html, chain+" ") || strings.Contains(html, chain+"(") {
		t.Errorf("the order detail invents a store beside the chain %q", chain)
	}
}

// tagWithID returns the opening tag carrying id, so a test can ask what
// attributes it holds without parsing the whole document.
func tagWithID(t *testing.T, html, id string) string {
	t.Helper()
	at := strings.Index(html, `id="`+id+`"`)
	if at < 0 {
		t.Fatalf("no element carries id %q", id)
	}
	start := strings.LastIndex(html[:at], "<")
	end := strings.Index(html[at:], ">")
	if start < 0 || end < 0 {
		t.Fatalf("element with id %q is not a tag", id)
	}
	return html[start : at+end+1]
}

// TestTheTimelineNamesWhoActed: a staff member by name, an erased one as such
// and never as the system, and everyone else by kind; under the list, how long
// mail stays on it.
func TestTheTimelineNamesWhoActed(t *testing.T) {
	t.Parallel()
	entries := []TimelineEntry{
		{Label: i18n.KeyStatusPlaced, ActorKind: ActorCustomer},
		{Label: i18n.KeyAdminTimelineProvider, Note: "checkout.session.completed", ActorKind: ActorProvider},
		{Label: i18n.KeyStatusPicking, ActorKind: ActorStaff, Actor: "王店長"},
		{Label: i18n.KeyAuditInvoiceAllowance, Status: i18n.KeyAdminTimelineInvoiceAwaitingBuyer, ActorKind: ActorStaff},
		{Label: i18n.KeyAuditInvoiceVoid, Status: i18n.KeyAdminTimelineInvoicePending, ActorKind: ActorSystem},
	}
	for _, tc := range []struct {
		locale  i18n.Locale
		by      []string
		caption string
	}{
		{i18n.ZhHant, []string{"顧客", "金流服務商", "王店長", "已刪除的帳號", "系統"}, "郵件紀錄只保留 30 天，更早的郵件不會列在這裡。"},
		{i18n.En, []string{"Customer", "Payment provider", "王店長", "Erased account", "System"}, "Mail is kept for 30 days; older mail is not listed here."},
	} {
		ctx := i18n.WithLocale(t.Context(), tc.locale)
		html := renderComponent(t, ctx, Order(layouts.Page{}, &OrderView{
			Number: "GO-261004-000001", Timeline: entries, MailKept: 30 * 24 * time.Hour,
		}))
		items := strings.Split(html, `class="goen-admin__event"`)[1:]
		if len(items) != len(entries) {
			t.Fatalf("%s: Order renders %d timeline entries, want %d", tc.locale, len(items), len(entries))
		}
		for i, e := range entries {
			item, _, _ := strings.Cut(items[i], "</li>")
			for _, want := range []string{i18n.T(ctx, e.Label), " · " + tc.by[i], e.Note} {
				if !strings.Contains(item, want) {
					t.Errorf("%s: timeline entry %d (%s) lacks %q: %s", tc.locale, i, e.Label, want, item)
				}
			}
			if e.Status != "" && !strings.Contains(item, i18n.T(ctx, e.Status)) {
				t.Errorf("%s: timeline entry %d (%s) lacks its status %q", tc.locale, i, e.Label, i18n.T(ctx, e.Status))
			}
		}
		if !strings.Contains(html, tc.caption) {
			t.Errorf("%s: the timeline does not say %q", tc.locale, tc.caption)
		}
	}
}

// TestTheIssueButtonFollowsTheMoney: the button is offered for every order
// whose money has arrived, including a pending one store credit paid in full,
// and for no order still owing.
func TestTheIssueButtonFollowsTheMoney(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		view OrderView
		want bool
	}{
		{name: "committed", view: OrderView{Status: order.FulfillmentPicking, Committed: true}, want: true},
		{name: "credit paid in full, pending", view: OrderView{Status: order.FulfillmentPending}, want: true},
		{name: "still owing", view: OrderView{Status: order.FulfillmentPending, Unpaid: true}, want: false},
		{name: "cancelled", view: OrderView{Status: order.FulfillmentCancelled}, want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.view.InvoicingEnabled = true
			if got := tt.view.CanIssueInvoice(); got != tt.want {
				t.Errorf("CanIssueInvoice = %t, want %t", got, tt.want)
			}
		})
	}
}

// A mail or invoice operation sits at its creation but is labelled with where
// it stands now, so both moments are shown: created at one time, and now in a
// state it reached at another.
func TestTheTimelineShowsWhenAnOperationWasCreatedAndWhenItSettled(t *testing.T) {
	t.Parallel()
	entries := []TimelineEntry{
		{At: "2026-10-05 10:00", DoneAt: "2026-10-05 10:20", Label: i18n.KeyAdminTimelineMailPaid,
			Status: i18n.KeyAdminTimelineMailSent, ActorKind: ActorSystem},
		{At: "2026-10-05 10:05", Label: i18n.KeyAdminTimelineMailShipped,
			Status: i18n.KeyAdminTimelineMailQueued, ActorKind: ActorSystem},
		{At: "2026-10-05 10:30", Label: i18n.KeyStatusPicking, ActorKind: ActorStaff, Actor: "王店長"},
	}
	for _, tc := range []struct {
		locale i18n.Locale
		want   [][]string
		absent []string
	}{
		{i18n.ZhHant, [][]string{
			{"2026-10-05 10:00 建立", "目前：已寄出（2026-10-05 10:20）"},
			{"2026-10-05 10:05 建立", "目前：尚未寄出"},
			{"2026-10-05 10:30 · 王店長"},
		}, []string{"目前：尚未寄出（"}},
		{i18n.En, [][]string{
			{"Created 2026-10-05 10:00", "Now: Sent (2026-10-05 10:20)"},
			{"Created 2026-10-05 10:05", "Now: Not sent yet"},
			{"2026-10-05 10:30 · 王店長"},
		}, []string{"Now: Not sent yet ("}},
	} {
		html := renderOrder(t, tc.locale, &OrderView{Number: "GO-261005-000002", Timeline: entries})
		items := strings.Split(html, `class="goen-admin__event"`)[1:]
		if len(items) != len(entries) {
			t.Fatalf("%s: renders %d timeline entries, want %d", tc.locale, len(items), len(entries))
		}
		for i, wants := range tc.want {
			item, _, _ := strings.Cut(items[i], "</li>")
			for _, want := range wants {
				if !strings.Contains(item, want) {
					t.Errorf("%s: timeline entry %d lacks %q: %s", tc.locale, i, want, item)
				}
			}
		}
		for _, bad := range tc.absent {
			if strings.Contains(html, bad) {
				t.Errorf("%s: a state with no completion time shows one: %q", tc.locale, bad)
			}
		}
	}
}
