package pages

import (
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestWarrantyRowListsCoverEnd(t *testing.T) {
	t.Parallel()
	view := WarrantyListView{Rows: []Warranty{{
		Name: "Buds", Order: "GO-261001-000001", InForce: true,
		ExpiresOn: shoptime.Date{Year: 2027, Month: time.October, Day: 6},
	}}}
	html := renderToString(t, WarrantyList(layouts.Page{Title: "Warranty"}, view))
	for _, want := range []string{"ui-statline--s", "保固至", `datetime="2027-10-06"`, "goen-warranty__item"} {
		if !strings.Contains(html, want) {
			t.Errorf("WarrantyList row lacks %q", want)
		}
	}
}

func TestWarrantyRowOfAReturnedOrderStatesNoCover(t *testing.T) {
	t.Parallel()
	view := WarrantyListView{Rows: []Warranty{{
		Name: "Buds", Order: "GO-261001-000001", Returned: true,
		ExpiresOn: shoptime.Date{Year: 2027, Month: time.October, Day: 6},
	}}}
	html := renderToString(t, WarrantyList(layouts.Page{Title: "Warranty"}, view))
	for _, banned := range []string{"保固中", "保固至", "ui-statline--s"} {
		if strings.Contains(html, banned) {
			t.Errorf("a returned order's warranty row still says %q", banned)
		}
	}
	if !strings.Contains(html, "這項商品已辦理退貨") {
		t.Error("a returned order's warranty row does not say it was returned")
	}
}
