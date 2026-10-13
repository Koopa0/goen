package i18n

import (
	"strings"
	"testing"
)

func TestReturnDeskDecisionWording(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		key Key
		zh  string
		en  string
	}{
		{key: KeyAdminRetGoodwillHint, zh: "未使用且包裝與配件齊全才是政策內受理。請先逐項評估這三項：有未知就不能同意或不同意；三項皆符合才記政策內權利。", en: "Unused and complete with box and accessories is the advertised offer. Assess those three facts first: unknown cannot be approved or declined, and only all-met records a policy entitlement."},
		{key: KeyAdminRetException, zh: "以人工例外同意", en: "Approve as a staff exception"},
		{key: KeyAdminRetErrStatutoryReject, zh: "七日內的有效申請不能因未填原因、拆封或第 8–14 天條件而不同意。", en: "A valid request inside seven days cannot be declined for a missing reason, for opening the parcel, or for the days 8–14 conditions."},
		{key: KeyAdminRetErrIncomplete, zh: "還有未觀察的品項。補齊三項評估後才能同意或不同意；未知不算符合，也不算不符合。", en: "Some facts are still unknown. Finish the three observations before approving or declining; unknown is neither a pass nor a failure."},
		{key: KeyAdminRetErrNeedException, zh: "這筆不能記成政策內權利。若要同意，請明確選擇人工例外。", en: "This cannot be recorded as a policy entitlement. To pay it, choose a staff exception."},
		{key: KeyAdminRetErrUnmetApprove, zh: "已有不符合的觀察，不能記成第 8–14 天政策內權利。不同意須引用該事實，或改選人工例外。", en: "An unmet observation cannot be recorded as the days 8–14 policy entitlement. Decline by citing that fact, or approve as a staff exception."},
		{key: KeyAdminRetErrNoUnmet, zh: "沒有已確認不符合的事實可以引用，因此不能選擇「不同意」。", en: "There is no confirmed unmet fact to cite, so this cannot be declined."},
		{key: KeyAdminRetErrExceptionReason, zh: "人工例外必須寫下同意理由，空白或只有空白字元不能付款。", en: "A staff exception needs a recorded reason; blank or whitespace cannot pay."},
		{key: KeyAdminRetPayoutOutstanding, zh: "這筆退貨已同意，但款項尚未退回。重新送出會沿用同一個退款識別，不會重複付款。", en: "This return is approved, but its money has not gone back. Sending it again uses the same refund key, so it cannot pay twice."},
		{key: KeyAdminRetInspectButton, zh: "記錄驗貨", en: "Record the inspection"},
		{key: KeyAdminNoticeRefundFailed, zh: "這筆退貨已同意，但退款沒有完成。請到 Stripe 後台確認後，按退貨列上的「重新退款」；不需要重新同意。", en: "This return is approved, but the refund did not complete. Check the Stripe dashboard, then press “Send the refund again” on its row. The approval stands."},
		{key: KeyAdminNoticeAssessed, zh: "已記錄評估。同意或不同意之後，評估就不能再改。", en: "Assessment recorded. Once you approve or decline, it can no longer change."},
		{key: KeyAdminRetConfirmReject, zh: "確認不同意退貨", en: "Confirm declining the return"},
		{key: KeyAdminRetErrRejectionReason, zh: "請填寫不同意退貨的原因。", en: "Enter a reason for declining this return."},
		{key: KeyAdminRetMustAccept, zh: "（依法不得拒絕退貨，退款含原運費）", en: "(the law does not allow this one to be refused, and the refund includes the original delivery fee)"},
		{key: KeyAdminRetPayoutStranded, zh: "付款服務已明確拒絕這筆退款，無法從這裡重送。請在 Stripe 手動退款，並查看系統健康頁。", en: "The provider refused this refund outright, so it cannot be re-sent here. Refund it manually in Stripe and check the health page."},
	} {
		t.Run(string(tt.key), func(t *testing.T) {
			t.Parallel()
			for _, text := range []struct {
				locale Locale
				want   string
			}{{ZhHant, tt.zh}, {En, tt.en}} {
				if got := T(WithLocale(t.Context(), text.locale), tt.key); got != text.want {
					t.Errorf("T(%s, %s) = %q, want %q", text.locale, tt.key, got, text.want)
				}
			}
		})
	}
}

func TestReturnDeskDoesNotUseOldDecisionOrCreditTerms(t *testing.T) {
	t.Parallel()
	for k, message := range messages {
		if !strings.HasPrefix(string(k), "admin.ret.") && !strings.HasPrefix(string(k), "admin.return.") && k != KeyAdminNoticeRefundFailed && k != KeyAdminNoticeAssessed && k != KeyPointsReturnUnsettled {
			continue
		}
		for _, term := range []string{"店儲", "額度", "核准"} {
			if strings.Contains(message.ZhHant, term) {
				t.Errorf("return desk message %s contains %q: %s", k, term, message.ZhHant)
			}
		}
	}
}
