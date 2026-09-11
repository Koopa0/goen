package pages

import (
	"fmt"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestAdminOrdersEmptyCopyMatchesTheQueueContext(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	render := func(v AdminOrdersView) string {
		return renderToString(t, AdminOrders(AdminOrdersMeta(ctx), v))
	}

	t.Run("a search miss names the term and not the status filter", func(t *testing.T) {
		t.Parallel()
		html := render(AdminOrdersView{Term: "GO-MISSING", Searched: true})
		want := i18n.T(ctx, i18n.KeyAdminQueueNoneFound)
		if !strings.Contains(html, fmt.Sprintf(want, "GO-MISSING")) {
			t.Fatalf("search miss HTML lacks %q", fmt.Sprintf(want, "GO-MISSING"))
		}
		if strings.Contains(html, i18n.T(ctx, i18n.KeyAdminQueueEmpty)) {
			t.Error("a search miss still claims the status filter is empty")
		}
	})

	t.Run("an empty all-orders queue says there are none yet", func(t *testing.T) {
		t.Parallel()
		html := render(AdminOrdersView{})
		want := i18n.T(ctx, i18n.KeyAdminQueueNoneYet)
		if !strings.Contains(html, want) {
			t.Fatalf("empty shop HTML lacks %q", want)
		}
		if strings.Contains(html, i18n.T(ctx, i18n.KeyAdminQueueEmpty)) {
			t.Error("an empty shop borrows the status-tab empty copy")
		}
	})

	t.Run("an empty status tab keeps the state-specific copy", func(t *testing.T) {
		t.Parallel()
		html := render(AdminOrdersView{Status: "picking"})
		want := i18n.T(ctx, i18n.KeyAdminQueueEmpty)
		if !strings.Contains(html, want) {
			t.Fatalf("empty status tab HTML lacks %q", want)
		}
	})
}
