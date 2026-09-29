package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestEmptyOlderPointsPageOffersTheFirstPage(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	body := renderComponent(t, ctx, Points(layouts.Page{Title: "Points"}, PointsView{
		ListBound: ListBound{PastEnd: true, First: "/account/points#ledger-heading"},
	}))
	if !strings.Contains(body, `href="/account/points#ledger-heading"`) || !strings.Contains(body, i18n.T(ctx, i18n.KeyPageEmpty)) {
		t.Fatal("empty older ledger lost its restart door")
	}
	if strings.Contains(body, i18n.T(ctx, i18n.KeyPointsEmpty)) || strings.Contains(body, `<ul class="goen-points"`) {
		t.Fatal("empty older page claimed the ledger never had entries")
	}
}
