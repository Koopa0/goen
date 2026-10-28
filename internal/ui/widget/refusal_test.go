package widget_test

import (
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/widget"
)

func TestRefusalSummaryCountsInEnglish(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	for _, tt := range []struct {
		count int
		want  string
	}{
		{1, "Goods receipt was not saved. Please correct 1 field."},
		{2, "Goods receipt was not saved. Please correct 2 fields."},
	} {
		if got := (widget.Refusal{Form: i18n.KeyAdminRefusalReceipt, Count: tt.count}).Text(ctx); got != tt.want {
			t.Errorf("Text(%d) = %q, want %q", tt.count, got, tt.want)
		}
	}
}
