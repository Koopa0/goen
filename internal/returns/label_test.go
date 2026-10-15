package returns

import (
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestEveryReturnPolicyWindowHasALabel(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		for _, window := range knownPolicyWindows {
			label := window.Label(ctx)
			if label == "" || label == string(window) {
				t.Errorf("%q.Label in %s = %q, want a catalogue label", window, locale, label)
			}
		}
	}
}

// TestEveryUnsentReasonHasALabel holds the form's closed set against its words:
// a reason with no catalogue entry reaches the staff as its bare code.
func TestEveryUnsentReasonHasALabel(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		for _, reason := range UnsentReasons {
			label := reason.Label(ctx)
			if label == "" || label == string(reason) {
				t.Errorf("%q.Label in %s = %q, want a catalogue label", reason, locale, label)
			}
		}
	}
}
