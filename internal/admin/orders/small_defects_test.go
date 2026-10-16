package orders

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestPickingSlipsEmptyStateIsAboutPicking(t *testing.T) {
	t.Parallel()
	body := render(t, Picking(layouts.Page{Title: "Picking"}, &PickingView{}))
	if !strings.Contains(body, i18n.T(i18n.WithLocale(t.Context(), i18n.En), i18n.KeyAdminPickingNone)) {
		t.Errorf("Picking with no totals lacks its own empty sentence:\n%s", body)
	}
	if !strings.Contains(body, `<div class="goen-admin__empty"><p class="goen-admin__emptytitle">Nothing to pick right now.</p></div>`) {
		t.Error("Picking with no totals does not use the shared empty block")
	}
	if strings.Contains(body, "No orders in this state") {
		t.Error("Picking with no totals borrows the order list's status-filter sentence")
	}
}

func TestPickingTotalsHeadAlignsWithItsNumbers(t *testing.T) {
	t.Parallel()
	body := render(t, Picking(layouts.Page{Title: "Picking"}, &PickingView{Totals: []PickingLine{{SKU: "A-1", Name: "Cup", Remaining: 3}}}))
	if want := `<th class="goen-admin__cellnum" scope="col">Not yet dispatched</th>`; !strings.Contains(body, want) {
		t.Errorf("Picking totals head lacks %q", want)
	}
}
