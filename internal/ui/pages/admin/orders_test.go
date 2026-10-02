package admin

import (
	"fmt"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
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
// cart.Address.Validate refuses one of the two and accepts neither.
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

// An automatic action is the system's, never the customer's: only an actor-less
// cancellation the sweeper did not make is the customer's own.
func TestTheTimelineNamesWhoCancelled(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		event  OrderEvent
		zh, en string
	}{
		{"staff", OrderEvent{Kind: "cancelled", Actor: "王店長"}, "王店長", "王店長"},
		{"payment deadline", OrderEvent{Kind: "cancelled", System: true}, "系統", "System"},
		{"customer", OrderEvent{Kind: "cancelled"}, "顧客", "Customer"},
		{"webhook", OrderEvent{Kind: "paid"}, "系統", "System"},
	} {
		for locale, want := range map[i18n.Locale]string{i18n.ZhHant: tc.zh, i18n.En: tc.en} {
			if got := tc.event.By(i18n.WithLocale(t.Context(), locale)); got != want {
				t.Errorf("%s/%s: By = %q, want %q", tc.name, locale, got, want)
			}
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
		{name: "committed", view: OrderView{Status: pages.FulfillmentPicking, Committed: true}, want: true},
		{name: "credit paid in full, pending", view: OrderView{Status: pages.FulfillmentPending}, want: true},
		{name: "still owing", view: OrderView{Status: pages.FulfillmentPending, Unpaid: true}, want: false},
		{name: "cancelled", view: OrderView{Status: pages.FulfillmentCancelled}, want: false},
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
