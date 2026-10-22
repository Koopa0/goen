package buyer

import (
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/returns"
)

// TestReturnsExisting_StatusText: StatusText gives every known return status a
// catalogue label, not its raw value, in both locales.
func TestReturnsExisting_StatusText(t *testing.T) {
	t.Parallel()

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		for _, status := range returns.Statuses {
			existing := ReturnsExisting{Status: status}
			label := existing.StatusText(ctx)
			if label == "" || label == string(status) {
				t.Errorf("ReturnsExisting{Status: %q}.StatusText in %s = %q, want a catalogue label",
					status, locale, label)
			}
		}
	}
}

// TestReturnsExisting_StatusTextUnknown: StatusText renders a status with no
// catalogue entry as itself instead of failing.
func TestReturnsExisting_StatusTextUnknown(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.En)
	unknown := returns.Status("legacy_foo")
	existing := ReturnsExisting{Status: unknown}
	if got := existing.StatusText(ctx); got != "legacy_foo" {
		t.Fatalf("StatusText(%q) = %q, want the raw status", unknown, got)
	}
}
