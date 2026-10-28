package i18n

import (
	"strings"
	"testing"
)

// TestMessagesZhHantEmailField checks that the Chinese messages about the email
// field use the field's name.
func TestMessagesZhHantEmailField(t *testing.T) {
	t.Parallel()
	ctx := WithLocale(t.Context(), ZhHant)
	label := T(ctx, KeyFieldEmail)
	for _, k := range []Key{
		KeyEmailRequired, KeyEmailMalformed, KeyRestockBadEmail,
		KeyAdminCustLead, KeyAdminCustPlaceholder, KeyAdminCustEmailUnconfirmed,
		KeyAdminQueueSearchPlaceholder, KeyAdminQueueSearchHint, KeyStaffNeeds,
	} {
		message := T(ctx, k)
		if !strings.Contains(message, label) || strings.Contains(strings.ToLower(message), "email") {
			t.Errorf("%s uses %q for the field labelled %q", k, message, label)
		}
	}
}
