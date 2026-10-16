package admin

import (
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestRefusalSummaryCountsInEnglish(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	for _, tt := range []struct {
		count int
		want  string
	}{
		{1, "Stock adjustment was not saved. Please correct 1 field."},
		{2, "Stock adjustment was not saved. Please correct 2 fields."},
	} {
		if got := (layouts.Refusal{Form: i18n.KeyAdminRefusalStock, Count: tt.count}).Text(ctx); got != tt.want {
			t.Errorf("Text(%d) = %q, want %q", tt.count, got, tt.want)
		}
	}
}
