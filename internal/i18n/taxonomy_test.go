package i18n

import (
	"context"
	"strings"
	"testing"
)

// The accessible name of a category control must contain its visible label
// (WCAG 2.5.3 Label in Name), or speech control cannot reach it.
func TestCategoryControlNamesContainTheirVisibleLabel(t *testing.T) {
	for _, pair := range []struct{ visible, name Key }{
		{KeyAdminColIcon, KeyAdminTaxIconOf},
		{KeyAdminColTone, KeyAdminTaxToneOf},
		{KeyAdminColComparable, KeyAdminTaxComparableOf},
	} {
		for _, l := range []Locale{ZhHant, En} {
			ctx := WithLocale(context.Background(), l)
			visible, name := T(ctx, pair.visible), T(ctx, pair.name)
			if !strings.Contains(name, visible) {
				t.Errorf("%s: accessible name %q does not contain visible label %q", l.Tag(), name, visible)
			}
		}
	}
}
