package slides

import (
	"testing"

	"github.com/koopa0/goen/internal/formtext"
	"github.com/koopa0/goen/internal/i18n"
)

func TestOptionalRunDaysDoNotTurnMalformedInputIntoNoExpiry(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	for _, raw := range []string{"forever", "1.5", "-1", "1000001"} {
		days := formtext.ParseCountOrInvalid(raw)
		if days >= 0 {
			t.Fatalf("ParseCountOrInvalid(%q) = %d, want an invalid sentinel", raw, days)
		}
		if errs := (&HeroForm{
			Headline: "Sale", PrimaryLabel: "Shop", PrimaryHref: "/deals", Days: days,
		}).Validate(ctx); errs["days"] == "" {
			t.Errorf("HeroForm accepted malformed days %q as an unbounded slide", raw)
		}
	}
	if got := formtext.ParseCountOrInvalid(""); got != 0 {
		t.Errorf("ParseCountOrInvalid(blank) = %d, want the documented no-expiry value 0", got)
	}
}
