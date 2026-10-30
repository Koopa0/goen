package i18n

import (
	"strings"
	"testing"
)

// TestMessagesReturnDeskTerms checks that the return desk messages do not use
// the old decision or credit terms.
func TestMessagesReturnDeskTerms(t *testing.T) {
	t.Parallel()
	for k, message := range messages {
		if !strings.HasPrefix(string(k), "admin.ret.") && !strings.HasPrefix(string(k), "admin.return.") && k != KeyAdminNoticeRefundFailed && k != KeyAdminNoticeReturnFollowUp && k != KeyAdminNoticeAssessed && k != KeyPointsReturnUnsettled {
			continue
		}
		for _, term := range []string{"店儲", "額度", "核准"} {
			if strings.Contains(message.ZhHant, term) {
				t.Errorf("return desk message %s contains %q: %s", k, term, message.ZhHant)
			}
		}
	}
}
