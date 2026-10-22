package warranty

import (
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestWarrantyConfirmationSelectsTheRegisteredUnit(t *testing.T) {
	t.Parallel()
	rows := []Warranty{
		{LineID: "line-a", Unit: 1, Name: "Phone", Serial: "SN-FIRST"},
		{LineID: "line-a", Unit: 2, Name: "Phone", Serial: "SN-SECOND"},
		{LineID: "line-b", Unit: 2, Name: "Tablet", Serial: "SN-TABLET"},
		{LineID: "returned", Unit: 1, Name: "Returned", Returned: true},
	}
	for _, tt := range []struct {
		name, query string
		want        *Warranty
	}{
		{name: "no query"},
		{name: "unbound success", query: "?ok=1"},
		{name: "missing success", query: "?line=line-a&unit=2"},
		{name: "stale refusal", query: "?serial=1&refused=1"},
		{name: "missing line", query: "?ok=1&unit=2"},
		{name: "unknown line", query: "?ok=1&line=foreign&unit=2"},
		{name: "invalid unit", query: "?ok=1&line=line-a&unit=oops"},
		{name: "unknown unit", query: "?ok=1&line=line-a&unit=3"},
		{name: "zero unit", query: "?ok=1&line=line-a&unit=0"},
		{name: "negative unit", query: "?ok=1&line=line-a&unit=-1"},
		{name: "oversized unit", query: "?ok=1&line=line-a&unit=1001"},
		{name: "returned unit", query: "?ok=1&line=returned&unit=1"},
		{name: "first unit", query: "?ok=1&line=line-a&unit=1", want: &rows[0]},
		{name: "second unit", query: "?ok=1&line=line-a&unit=2", want: &rows[1]},
		{name: "another line", query: "?ok=1&line=line-b&unit=2", want: &rows[2]},
		{name: "stale flags", query: "?ok=1&line=line-a&unit=2&serial=1&refused=1", want: &rows[1]},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequestWithContext(t.Context(), "GET", "/account/warranty/order"+tt.query, nil)
			if diff := cmp.Diff(tt.want, registeredUnitFor(r, rows)); diff != "" {
				t.Errorf("registered unit (-want +got):\n%s", diff)
			}
		})
	}
}
