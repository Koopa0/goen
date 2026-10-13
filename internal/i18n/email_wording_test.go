package i18n

import (
	"strings"
	"testing"
)

func TestChineseEmailFieldMessagesUseTheFieldName(t *testing.T) {
	t.Parallel()
	ctx := WithLocale(t.Context(), ZhHant)
	label := T(ctx, KeyFieldEmail)
	for _, k := range []Key{
		KeyEmailRequired, KeyEmailMalformed, KeyRestockBadEmail,
		KeyAdminCustLead, KeyAdminCustPlaceholder, KeyAdminCustEmailUnconfirmed,
		KeyAdminQueueSearchPlaceholder, KeyAdminQueueSearchNote, KeyStaffNeeds,
	} {
		message := T(ctx, k)
		if !strings.Contains(message, label) || strings.Contains(strings.ToLower(message), "email") {
			t.Errorf("%s uses %q for the field labelled %q", k, message, label)
		}
	}
}
