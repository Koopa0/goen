package orders

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

func TestARedirectedNoticeIsShownAsItsOwnOutcome(t *testing.T) {
	t.Parallel()
	pinned := map[string]components.Outcome{
		"ok":             components.OutcomeDone,
		"shipped":        components.OutcomeDone,
		"refused":        components.OutcomeRefused,
		"paidcancel":     components.OutcomeRefused,
		"voidfailed":     components.OutcomeFailed,
		"refundretry":    components.OutcomeFailed,
		"cancelretry":    components.OutcomeFailed,
		"refundshipped":  components.OutcomeRefused,
		"refundmismatch": components.OutcomeFailed,
		"refundunsure":   components.OutcomeFailed,
		"invoicepending": components.OutcomeFailed,
		"invoicingoff":   components.OutcomeRefused,
		"refundpending":  components.OutcomeFailed,
		"cancelinvoice":  components.OutcomeFailed,
		"invoicefailed":  components.OutcomeFailed,
		"allowfailed":    components.OutcomeFailed,
	}
	for name, want := range pinned {
		if got := notices[name].Outcome; got != want {
			t.Errorf("notices[%q].Outcome = %d, want %d", name, got, want)
		}
	}
	for name, m := range notices {
		ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/orders?"+name+"=1", http.NoBody)
		var page strings.Builder
		view := admin.OrdersView{Notice: web.Notice(req, notices)}
		if err := admin.Orders(layouts.Page{Title: "orders"}, view).Render(ctx, &page); err != nil {
			t.Fatal(err)
		}
		got := page.String()
		sentence := i18n.T(ctx, m.Key)
		if !strings.Contains(got, sentence) {
			t.Fatalf("?%s=1: the page does not show %q", name, sentence)
		}
		refusal := strings.Contains(got, `role="alert"`) && strings.Contains(got, "goen-notice--danger")
		saved := strings.Contains(got, `role="status"`) && strings.Contains(got, "goen-notice--accent")
		if (m.Outcome == components.OutcomeDone) == refusal || (m.Outcome == components.OutcomeDone) != saved {
			t.Errorf("?%s=1 with outcome %d: danger treatment = %t, accent treatment = %t", name, m.Outcome, refusal, saved)
		}
		if lead := strings.Contains(got, i18n.T(ctx, i18n.KeyAdminNoticeLeadRefused)) ||
			strings.Contains(got, i18n.T(ctx, i18n.KeyAdminNoticeLeadFailed)); lead == (m.Outcome == components.OutcomeDone) {
			t.Errorf("?%s=1 with outcome %d: leading word shown = %t", name, m.Outcome, lead)
		}
	}
}

func TestInvoicingOffHasItsOwnSentence(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	off := i18n.T(ctx, i18n.KeyAdminNoticeInvoicingOff)
	for _, tc := range []struct {
		query string
		shown bool
	}{{"invoicingoff", true}, {"refused", false}} {
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/orders/G1?"+tc.query+"=1", http.NoBody)
		var page strings.Builder
		view := admin.OrderView{Notice: web.Notice(req, notices)}
		if err := admin.Order(layouts.Page{Title: "order"}, &view).Render(ctx, &page); err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(page.String(), off); got != tc.shown {
			t.Errorf("?%s=1: page shows %q = %t, want %t", tc.query, off, got, tc.shown)
		}
	}
}
