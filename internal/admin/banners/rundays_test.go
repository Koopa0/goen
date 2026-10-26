package banners

import (
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/web/formtext"
)

// TestForm_ValidateRunDays: optional run days do not turn malformed input into no expiry.
func TestForm_ValidateRunDays(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	for _, raw := range []string{"forever", "1.5", "-1", "1000001"} {
		days := formtext.ParseCountOrInvalid(raw)
		if days >= 0 {
			t.Fatalf("ParseCountOrInvalid(%q) = %d, want an invalid sentinel", raw, days)
		}
		if errs := (&Form{Message: "Sale", Days: days}).Validate(ctx); errs["days"] == "" {
			t.Errorf("Form accepted malformed days %q as an unbounded banner", raw)
		}
	}
}
