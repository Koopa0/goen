package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestEveryReturnPolicyWindowHasALabel(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		for _, window := range []string{"within", "goodwill", "after", "undelivered"} {
			label := AdminReturn{Window: window}.WindowText(ctx)
			if label == "" || label == window {
				t.Errorf("WindowText(%q) in %s = %q, want a catalogue label", window, locale, label)
			}
		}
	}
}

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

	t.Run("each policy window names itself without claiming a missing fact", func(t *testing.T) {
		ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
		tests := []struct {
			window string
			want   []string
			hide   []string
		}{
			{
				window: "within",
				want:   []string{i18n.T(ctx, i18n.KeyAdminReturnWindowWithin), i18n.T(ctx, i18n.KeyAdminRetMustAccept)},
				hide:   []string{i18n.T(ctx, i18n.KeyAdminRetGoodwillHint), i18n.T(ctx, i18n.KeyAdminRetLateHint)},
			},
			{
				window: "goodwill",
				want:   []string{i18n.T(ctx, i18n.KeyAdminReturnWindowGoodwill), i18n.T(ctx, i18n.KeyAdminRetGoodwillHint)},
				hide:   []string{i18n.T(ctx, i18n.KeyAdminRetMustAccept)},
			},
			{
				window: "after",
				want:   []string{i18n.T(ctx, i18n.KeyAdminReturnWindowAfter), i18n.T(ctx, i18n.KeyAdminRetLateHint)},
				hide:   []string{i18n.T(ctx, i18n.KeyAdminRetMustAccept), i18n.T(ctx, i18n.KeyAdminRetGoodwillHint)},
			},
			{
				window: "undelivered",
				want:   []string{i18n.T(ctx, i18n.KeyAdminReturnWindowUndelivered)},
				hide:   []string{i18n.T(ctx, i18n.KeyAdminRetMustAccept), i18n.T(ctx, i18n.KeyAdminRetLateHint)},
			},
		}
		for _, tt := range tests {
			t.Run(tt.window, func(t *testing.T) {
				row := base("window-" + tt.window)
				row.Status = "requested"
				row.Decided = false
				row.Window = tt.window
				html := render(t, row)
				for _, want := range tt.want {
					if !strings.Contains(html, want) {
						t.Errorf("window %s HTML lacks %q", tt.window, want)
					}
				}
				for _, hide := range tt.hide {
					if strings.Contains(html, hide) {
						t.Errorf("window %s HTML still claims %q", tt.window, hide)
					}
				}
			})
		}
	})

	t.Run("an open request offers both decisions", func(t *testing.T) {
		row := base("open-row")
		row.Status = "requested"
		row.Decided = false
		html := render(t, row)
		for _, want := range []string{
			`action="/admin/returns/open-row/decide"`, `value="approved"`, `value="rejected"`,
			`name="rejection_ground"`, `value="missing_reason"`, `value="ineligible"`,
			`name="resolution"`, `maxlength="300"`,
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
		if strings.Contains(html, `name="resolution"`) {
			t.Error("a payout retry asks for a new decision note which cannot change the approved claim")
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

	t.Run("an inconsistent payout is explained without an unsafe button", func(t *testing.T) {
		row := base("stranded-row")
		row.Decided = true
		row.PayoutOutstanding = true
		row.PayoutBlocked = true
		html := render(t, row)
		stranded := i18n.T(i18n.WithLocale(t.Context(), i18n.ZhHant), i18n.KeyAdminRetPayoutStranded)
		if !strings.Contains(html, stranded) {
			t.Error("the inconsistent-payout explanation is absent")
		}
		if strings.Contains(html, `/admin/returns/stranded-row/decide`) {
			t.Error("an inconsistent payout offers a retry that cannot be proven safe")
		}
	})
}
