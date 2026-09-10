package admin

import (
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/icons"
)

func TestTaxonomyIconValidationMatchesTheRenderer(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)

	for _, key := range icons.CategoryKeys() {
		form := &TaxonomyForm{Slug: "valid-category", Name: "Category", IconKey: key}
		if errs := form.Validate(ctx); errs["icon_key"] != "" {
			t.Errorf("Validate rejected rendered category icon %q: %v", key, errs)
		}
	}

	form := &TaxonomyForm{Slug: "valid-category", Name: "Category", IconKey: "rocket"}
	if errs := form.Validate(ctx); errs["icon_key"] == "" {
		t.Error("Validate accepted rocket, which icons.Category renders as nothing")
	}
}
