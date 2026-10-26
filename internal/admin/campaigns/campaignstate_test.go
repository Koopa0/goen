package campaigns

import (
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

// TestRow_StateNothingOnSale: a running campaign with nothing on sale says it is hidden from the shop.
func TestRow_StateNothingOnSale(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		row  Row
		want string
	}{
		{"running without a product on sale", Row{Active: true, Running: true, Products: 3}, "Not shown in the shop: no product on sale is in stock"},
		{"running with one on sale", Row{Active: true, Running: true, Products: 3, Sellable: true}, "Running"},
		{"switched off wins", Row{Running: false}, "Switched off"},
	}
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.row.State(ctx); got != tt.want {
				t.Errorf("Row%+v.State() = %q, want %q", tt.row, got, tt.want)
			}
			view := CampaignView{Detail: Detail{Active: tt.row.Active, Running: tt.row.Running, Sellable: tt.row.Sellable}}
			if got := view.StateText(ctx); got != tt.want {
				t.Errorf("CampaignView.StateText() = %q, want %q", got, tt.want)
			}
		})
	}
	if (Row{Running: true, Products: 3}).Live() {
		t.Error("Live() = true for a running campaign with nothing on sale")
	}
}
