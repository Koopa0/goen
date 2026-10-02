package i18n_test

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

// TestTheInvoicingNoteNamesNoSetting: the note is read by shop staff, who cannot
// change the server's environment, so an environment variable name in it is
// a puzzle with no one to ask. The settings are named where an operator reads:
// the deployment notes.
func TestTheInvoicingNoteNamesNoSetting(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		if note := i18n.T(ctx, i18n.KeyAdminQueueNoInvoicing); strings.Contains(note, "GOEN_") {
			t.Errorf("%s note shows staff an environment variable: %q", locale, note)
		}
	}
}
