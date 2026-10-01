package admin

import (
	"fmt"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

// A combination another SKU already carries is answered on the option selects,
// the control that chose it, and not as a server error: the storefront sells a
// selection only when exactly one variant matches it.
func TestADuplicateOptionCombinationIsRefusedOnTheOptionSelects(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	got, err := variantWriteError(ctx, "buds", fmt.Errorf("audit: %w", errVariantCombinationTaken))
	if err != nil {
		t.Fatalf("variantWriteError = %v, want a field refusal", err)
	}
	want := i18n.T(ctx, i18n.KeyFormVariantCombinationTaken)
	if len(got) != 1 || got["options"] != want || want == "" {
		t.Errorf("refusal = %v, want only options: %q", got, want)
	}
}
