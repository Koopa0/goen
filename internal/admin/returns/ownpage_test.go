package returns

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/returns"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestReturnsOwnPageFormsPostBack: on a return's own page every form carries the
// flag that sends its answer back there, and on the queue none does, so the
// queue's answers stay where they were.
func TestReturnsOwnPageFormsPostBack(t *testing.T) {
	t.Parallel()
	rows := []Return{
		// An open goodwill request offers assessment and the decision.
		{ID: "open", OrderNumber: "GO-1", Status: returns.StatusRequested, Window: returns.WindowGoodwill,
			Lines: []Line{{OrderLineID: "open-line", Quantity: 1}}},
		// An inspected approval whose money is still owed offers the resend and the
		// closing.
		{ID: "owed", OrderNumber: "GO-2", Status: returns.StatusApproved, Decided: true, PayoutOutstanding: true, Window: returns.WindowStatutory,
			Lines: []Line{{OrderLineID: "owed-line", Quantity: 1, Inspected: true}}},
		// An approval whose parcel has not been opened offers the inspection.
		{ID: "coming", OrderNumber: "GO-3", Status: returns.StatusApproved, Decided: true, Window: returns.WindowStatutory,
			Lines: []Line{{OrderLineID: "coming-line", Quantity: 1, Restockable: true}}},
		// An inspected approval whose money has gone back offers the closing.
		{ID: "opened", OrderNumber: "GO-4", Status: returns.StatusApproved, Decided: true, Window: returns.WindowStatutory,
			Lines: []Line{{OrderLineID: "opened-line", Quantity: 1, Inspected: true, Received: 1}}},
	}
	const flag = `<input type="hidden" name="from" value="return">`
	for _, own := range []bool{true, false} {
		ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
		page := renderComponent(t, ctx, Returns(frame.Page{}, View{Rows: rows, OwnPage: own}))
		var forms []string
		for _, form := range strings.Split(page, "<form")[1:] {
			// The page frame has forms of its own, such as the language switch.
			if form, _, _ = strings.Cut(form, "</form>"); strings.Contains(form, `action="/admin/returns/`) {
				forms = append(forms, form)
			}
		}
		const wantForms = 6
		if len(forms) != wantForms {
			t.Fatalf("OwnPage=%t renders %d return forms, want %d: the fixture no longer reaches every form", own, len(forms), wantForms)
		}
		for _, form := range forms {
			if got := strings.Contains(form, flag); got != own {
				t.Errorf("OwnPage=%t: form carries the flag = %t: %.120s", own, got, form)
			}
		}
	}
}
