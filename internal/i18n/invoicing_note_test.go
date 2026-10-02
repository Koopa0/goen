package i18n_test

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

// TestTheInvoicingNoteNamesEverySettingGoenNeeds: setting the merchant id alone
// makes goen refuse to start, so a note that names only it sends the operator
// into that failure.
func TestTheInvoicingNoteNamesEverySettingGoenNeeds(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		note := i18n.T(ctx, i18n.KeyAdminQueueNoInvoicing)
		for _, name := range []string{"GOEN_ECPAY_MERCHANT_ID", "GOEN_ECPAY_HASH_KEY", "GOEN_ECPAY_HASH_IV"} {
			if !strings.Contains(note, name) {
				t.Errorf("%s note does not name %s: %q", locale, name, note)
			}
		}
	}
}
