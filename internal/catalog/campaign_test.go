package catalog

import (
	"math"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestHumanRemainingDoesNotOverflowAtTheDatabaseRange(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	got := humanRemaining(ctx, math.MaxInt64)
	if got == "" || !strings.Contains(got, "days") {
		t.Fatalf("humanRemaining(MaxInt64) = %q, want a positive day count", got)
	}
}
