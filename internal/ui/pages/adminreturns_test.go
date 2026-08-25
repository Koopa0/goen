package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestAnApprovedReturnWithMoneyOutstandingOffersToSendItAgain(t *testing.T) {
	base := func(id string) AdminReturn {
		return AdminReturn{
			ID: id, OrderNumber: "GO-TEST", Status: "approved", StatusText: "已同意",
			Reason: "不合用", Window: "within",
		}
	}
	render := func(t *testing.T, row AdminReturn) string {
		t.Helper()
		return renderToString(t, AdminReturns(layouts.Page{Title: "退貨"}, AdminReturnsView{
			Rows: []AdminReturn{row},
		}))
	}

	t.Run("an open request offers both decisions", func(t *testing.T) {
		row := base("open-row")
		row.Status = "requested"
		row.Decided = false
		html := render(t, row)
		for _, want := range []string{
			`action="/admin/returns/open-row/decide"`, `value="approved"`, `value="rejected"`,
		} {
			if !strings.Contains(html, want) {
				t.Errorf("open request HTML lacks %s", want)
			}
		}
	})

	t.Run("an outstanding payout offers only the retry", func(t *testing.T) {
		row := base("retry-row")
		row.Decided = true
		row.PayoutOutstanding = true
		html := render(t, row)
		for _, want := range []string{
			`method="post"`, `action="/admin/returns/retry-row/decide"`,
			`name="decision"`, `value="approved"`, i18n.T(i18n.WithLocale(t.Context(), i18n.ZhHant), i18n.KeyAdminRetRetryPayout),
		} {
			if !strings.Contains(html, want) {
				t.Errorf("retry HTML lacks %s", want)
			}
		}
		if strings.Contains(html, `value="rejected"`) {
			t.Error("a payout retry offers to retake the rejection decision")
		}
	})

	t.Run("a settled payout has no decision action", func(t *testing.T) {
		row := base("settled-row")
		row.Decided = true
		html := render(t, row)
		if strings.Contains(html, `/admin/returns/settled-row/decide`) {
			t.Error("a settled return still offers a decision or payout action")
		}
	})

	t.Run("a terminal refund is explained without a dead button", func(t *testing.T) {
		row := base("stranded-row")
		row.Decided = true
		row.PayoutOutstanding = true
		row.PayoutBlocked = true
		html := render(t, row)
		stranded := i18n.T(i18n.WithLocale(t.Context(), i18n.ZhHant), i18n.KeyAdminRetPayoutStranded)
		if !strings.Contains(html, stranded) {
			t.Error("the terminal refund explanation is absent")
		}
		if strings.Contains(html, `/admin/returns/stranded-row/decide`) {
			t.Error("a terminal refund offers a retry that cannot succeed")
		}
	})
}
