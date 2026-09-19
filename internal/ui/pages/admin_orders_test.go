package pages

import (
	"fmt"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/pickup"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestAdminOrdersEmptyCopyMatchesTheQueueContext(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	render := func(v AdminOrdersView) string {
		return renderToString(t, AdminOrders(AdminOrdersMeta(ctx), v))
	}

	t.Run("a search miss names the term and not the status filter", func(t *testing.T) {
		t.Parallel()
		html := render(AdminOrdersView{Term: "GO-MISSING", Searched: true})
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
		html := render(AdminOrdersView{})
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
		html := render(AdminOrdersView{Status: "picking"})
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
	html := renderToString(t, AdminOrder(layouts.Page{Title: "GO-PICKUP"}, &AdminOrderView{
		Number:            "GO-PICKUP",
		Correctable:       true,
		PickupDestination: true,
		PickupBrands:      PickupBrandChoices(),
		Address:           Delivery{PickupBrand: pickup.FamilyMart}.Line(),
		Delivery:          AdminDelivery{PickupBrand: pickup.FamilyMart},
	}))

	for _, id := range []string{"d-store-code", "d-store-name"} {
		tag := tagWithID(t, html, id)
		if strings.Contains(tag, "required") {
			t.Errorf("the correction form still demands #%s: %s", id, tag)
		}
	}
	if tag := tagWithID(t, html, "d-brand"); !strings.Contains(tag, "required") {
		t.Errorf("the chain stopped being required: %s", tag)
	}

	chain := Delivery{PickupBrand: pickup.FamilyMart}.Line()
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
