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
	want := []struct {
		name    string
		outcome components.Outcome
	}{
		{"ok", components.OutcomeDone},
		{"refused", components.OutcomeRefused},
		{"shipped", components.OutcomeDone},
		{"toolate", components.OutcomeRefused},
		{"deliveryneeds", components.OutcomeRefused},
		{"paidcancel", components.OutcomeRefused},
		{"refunded", components.OutcomeDone},
		{"refundpending", components.OutcomeFailed},
		{"cancelinvoice", components.OutcomeFailed},
		{"refundretry", components.OutcomeFailed},
		{"cancelretry", components.OutcomeFailed},
		{"refundshipped", components.OutcomeRefused},
		{"refundhasreturn", components.OutcomeRefused},
		{"refundcancelled", components.OutcomeRefused},
		{"refundunpaid", components.OutcomeRefused},
		{"refundchanged", components.OutcomeRefused},
		{"refundpicking", components.OutcomeRefused},
		{"refundreason", components.OutcomeRefused},
		{"refundmismatch", components.OutcomeFailed},
		{"refundunsure", components.OutcomeFailed},
		{"unfunded", components.OutcomeRefused},
		{"owesparcel", components.OutcomeRefused},
		{"invoiced", components.OutcomeDone},
		{"voided", components.OutcomeDone},
		{"hasinvoice", components.OutcomeRefused},
		{"noinvoice", components.OutcomeRefused},
		{"invoicefailed", components.OutcomeFailed},
		{"invoicingoff", components.OutcomeRefused},
		{"invoicepending", components.OutcomeFailed},
		{"allowed", components.OutcomeDone},
		{"allowsent", components.OutcomeDone},
		{"allowtoomuch", components.OutcomeRefused},
		{"allowclaimed", components.OutcomeRefused},
		{"voidreason", components.OutcomeRefused},
		{"voidfailed", components.OutcomeFailed},
		{"allowfailed", components.OutcomeFailed},
	}
	if len(want) != len(notices) {
		t.Fatalf("the table names %d redirects, notices has %d", len(want), len(notices))
	}
	for _, tc := range want {
		m, ok := notices[tc.name]
		if !ok {
			t.Fatalf("notices has no entry %q", tc.name)
		}
		if m.Outcome != tc.outcome {
			t.Errorf("notices[%q].Outcome = %d, want %d", tc.name, m.Outcome, tc.outcome)
		}
		ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/orders?"+tc.name+"=1", http.NoBody)
		var page strings.Builder
		view := admin.OrdersView{Notice: web.Notice(req, notices)}
		if err := admin.Orders(layouts.Page{Title: "orders"}, view).Render(ctx, &page); err != nil {
			t.Fatal(err)
		}
		got := page.String()
		if sentence := i18n.T(ctx, m.Key); !strings.Contains(got, sentence) {
			t.Fatalf("?%s=1: the page does not show %q", tc.name, sentence)
		}
		refusal := strings.Contains(got, `role="alert"`) && strings.Contains(got, "goen-notice--danger")
		saved := strings.Contains(got, `role="status"`) && strings.Contains(got, "goen-notice--accent")
		if (tc.outcome == components.OutcomeDone) == refusal || (tc.outcome == components.OutcomeDone) != saved {
			t.Errorf("?%s=1 with outcome %d: danger treatment = %t, accent treatment = %t", tc.name, tc.outcome, refusal, saved)
		}
		refused := strings.Contains(got, i18n.T(ctx, i18n.KeyAdminNoticeLeadRefused))
		failed := strings.Contains(got, i18n.T(ctx, i18n.KeyAdminNoticeLeadFailed))
		if refused != (tc.outcome == components.OutcomeRefused) || failed != (tc.outcome == components.OutcomeFailed) {
			t.Errorf("?%s=1 with outcome %d: refused lead = %t, failed lead = %t", tc.name, tc.outcome, refused, failed)
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
