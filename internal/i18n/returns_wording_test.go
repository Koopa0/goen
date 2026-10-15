package i18n

import (
	"strings"
	"testing"
)

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
