package loyalty

import (
	"html"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestThePointsPageSaysWhatExpiresAndWhatDoesNot(t *testing.T) {
	t.Parallel()
	for _, loc := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), loc)
		var b strings.Builder
		if err := Points(layouts.Page{Title: "p"}, PointsView{PerCredit: 1}).Render(ctx, &b); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b.String(), html.EscapeString(i18n.T(ctx, i18n.KeyPointsExpiryTerms))) {
			t.Errorf("%v: the points page does not state the expiry rule", loc)
		}
	}
}
