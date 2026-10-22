package orders

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/widget"
)

// TestOrdersRedirectedNotice: the queue shows a redirected notice as its own
// outcome.
func TestOrdersRedirectedNotice(t *testing.T) {
	t.Parallel()
	want := []struct {
		name    string
		outcome widget.Outcome
	}{
		{"ok", widget.OutcomeDone},
		{"refused", widget.OutcomeRefused},
		{"shipped", widget.OutcomeDone},
		{"parceldelivered", widget.OutcomeDone},
		{"toolate", widget.OutcomeRefused},
		{"deliveryneeds", widget.OutcomeRefused},
		{"paidcancel", widget.OutcomeRefused},
		{"refunded", widget.OutcomeDone},
		{"refundpending", widget.OutcomeFailed},
		{"cancelinvoice", widget.OutcomeFailed},
		{"refundretry", widget.OutcomeFailed},
		{"cancelretry", widget.OutcomeFailed},
		{"refundshipped", widget.OutcomeRefused},
		{"refundhasreturn", widget.OutcomeRefused},
		{"refundcancelled", widget.OutcomeRefused},
		{"refundunpaid", widget.OutcomeRefused},
		{"refundchanged", widget.OutcomeRefused},
		{"refundpicking", widget.OutcomeRefused},
		{"refundreason", widget.OutcomeRefused},
		{"refundmismatch", widget.OutcomeFailed},
		{"refundunsure", widget.OutcomeFailed},
		{"unfunded", widget.OutcomeRefused},
		{"owesparcel", widget.OutcomeRefused},
		{"invoiced", widget.OutcomeDone},
		{"voided", widget.OutcomeDone},
		{"hasinvoice", widget.OutcomeRefused},
		{"noinvoice", widget.OutcomeRefused},
		{"invoicefailed", widget.OutcomeFailed},
		{"invoicingoff", widget.OutcomeRefused},
		{"invoicepending", widget.OutcomeFailed},
		{"allowed", widget.OutcomeDone},
		{"allowsent", widget.OutcomeDone},
		{"allowtoomuch", widget.OutcomeRefused},
		{"allowclaimed", widget.OutcomeRefused},
		{"voidreason", widget.OutcomeRefused},
		{"voidfailed", widget.OutcomeFailed},
		{"allowfailed", widget.OutcomeFailed},
		{"unsentrefunded", widget.OutcomeDone},
		{"unsentallowance", widget.OutcomeFailed},
		{"unsentdiffers", widget.OutcomeRefused},
		{"unsentbounds", widget.OutcomeRefused},
		{"unsentreason", widget.OutcomeRefused},
		{"unsentineligible", widget.OutcomeRefused},
		{"unsentundecided", widget.OutcomeRefused},
		{"unsenthold", widget.OutcomeFailed},
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
		view := View{Notice: widget.NoticeFrom(req, notices)}
		if err := Orders(frame.Page{Title: "orders"}, view).Render(ctx, &page); err != nil {
			t.Fatal(err)
		}
		got := page.String()
		if sentence := i18n.T(ctx, m.Key); !strings.Contains(got, sentence) {
			t.Fatalf("?%s=1: the page does not show %q", tc.name, sentence)
		}
		refusal := strings.Contains(got, `role="alert"`) && strings.Contains(got, "goen-notice--danger")
		saved := strings.Contains(got, `role="status"`) && strings.Contains(got, "goen-notice--accent")
		if (tc.outcome == widget.OutcomeDone) == refusal || (tc.outcome == widget.OutcomeDone) != saved {
			t.Errorf("?%s=1 with outcome %d: danger treatment = %t, accent treatment = %t", tc.name, tc.outcome, refusal, saved)
		}
		refused := strings.Contains(got, i18n.T(ctx, i18n.KeyAdminNoticeLeadRefused))
		failed := strings.Contains(got, i18n.T(ctx, i18n.KeyAdminNoticeLeadFailed))
		if refused != (tc.outcome == widget.OutcomeRefused) || failed != (tc.outcome == widget.OutcomeFailed) {
			t.Errorf("?%s=1 with outcome %d: refused lead = %t, failed lead = %t", tc.name, tc.outcome, refused, failed)
		}
	}
}

// TestOrderInvoicingOffSentence: the order page has its own sentence when
// invoicing is off.
func TestOrderInvoicingOffSentence(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	off := i18n.T(ctx, i18n.KeyAdminNoticeInvoicingOff)
	for _, tc := range []struct {
		query string
		shown bool
	}{{"invoicingoff", true}, {"refused", false}} {
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/orders/G1?"+tc.query+"=1", http.NoBody)
		var page strings.Builder
		view := OrderView{Notice: widget.NoticeFrom(req, notices)}
		if err := Order(frame.Page{Title: "order"}, &view).Render(ctx, &page); err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(page.String(), off); got != tc.shown {
			t.Errorf("?%s=1: page shows %q = %t, want %t", tc.query, off, got, tc.shown)
		}
	}
}
