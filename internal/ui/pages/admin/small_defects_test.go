package admin

import (
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/user"
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

func TestStaffRowPrintsAnEmailOnce(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		row   StaffRow
		count int
	}{
		"no name":   {StaffRow{ID: "1", Email: "nameless@example.com", Role: user.RoleStaff}, 1},
		"with name": {StaffRow{ID: "2", Email: "named@example.com", Role: user.RoleStaff, Name: "Mei"}, 1},
	} {
		body := render(t, Staff(layouts.Page{Title: "Staff"}, StaffView{Rows: []StaffRow{tc.row}}))
		if got := strings.Count(body, tc.row.Email); got != tc.count {
			t.Errorf("%s: %q appears %d times in the staff list, want %d", name, tc.row.Email, got, tc.count)
		}
	}
}

func TestTiersMultiplierColumnIsNumeric(t *testing.T) {
	t.Parallel()
	body := render(t, Tiers(layouts.Page{Title: "Tiers"}, TiersView{Rows: []Tier{{ID: "1", Code: "gold", Name: "Gold", MinSpend: 10000, MultiplierBP: 11000, Members: 4}}}))
	if !strings.Contains(body, `<td class="goen-admin__cellnum">1.1`) {
		t.Errorf("tiers multiplier cell is not numeric:\n%s", body)
	}
	if !strings.Contains(body, `<th class="goen-admin__cellnum" scope="col">Points rate`) {
		t.Errorf("tiers multiplier head is not numeric:\n%s", body)
	}
}

func TestEmptyLedgerSaysNoMovementOnce(t *testing.T) {
	t.Parallel()
	v := &MovementsView{SKU: "A-1", Days: []StockDay{{Day: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}}}
	body := render(t, Movements(layouts.Page{Title: "Movements"}, v))
	if strings.Contains(body, v.NoStockLine(i18n.WithLocale(t.Context(), i18n.En))) {
		t.Error("an empty ledger also prints the days-without-movement line")
	}
	if !strings.Contains(body, i18n.T(i18n.WithLocale(t.Context(), i18n.En), i18n.KeyAdminLedgerEmpty)) {
		t.Error("an empty ledger lacks its empty state")
	}
}

func TestZonePrefixRowsFollowLines(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ raw, want string }{
		{"209 210 211 212 880 881 882 883 884 885", "3"},
		{"", "3"},
		{"209\n210\n211\n212", "5"},
	} {
		if got := zonePrefixRows(tc.raw); got != tc.want {
			t.Errorf("zonePrefixRows(%q) = %s, want %s", tc.raw, got, tc.want)
		}
	}
}
