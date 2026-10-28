package sales

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

// TestComparison_SentenceAtSameTime: a comparison whose previous run is cut at
// the same time of day says so in each of the five sentences it can be, and
// one that is not says nothing of the kind, as the report's has always read.
// Mutation: ignoring AtSameTime in Sentence turns every case with the flag red.
func TestComparison_SentenceAtSameTime(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		c    Comparison
	}{
		{"no orders before", Comparison{Days: 7, Orders: 30, PreviousOrders: 0, Current: 3000, Previous: 0}},
		{"inside the noise", Comparison{Days: 7, Orders: 30, PreviousOrders: 30, Current: 3100, Previous: 3000, Noise: 1e18, PreviousText: "NT$30"}},
		{"more", Comparison{Days: 7, Orders: 300, PreviousOrders: 100, Current: 3000, Previous: 1000, Noise: 1}},
		{"less", Comparison{Days: 7, Orders: 100, PreviousOrders: 300, Current: 1000, Previous: 3000, Noise: 1}},
		{"about the same", Comparison{Days: 7, Orders: 300, PreviousOrders: 300, Current: 3001, Previous: 3000, Noise: 0.0001}},
	}
	for _, loc := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), loc)
		phrase := map[i18n.Locale]string{i18n.ZhHant: "同一時刻", i18n.En: "at the same time"}[loc]
		for _, tt := range tests {
			whole := tt.c.Sentence(ctx)
			tt.c.AtSameTime = true
			timed := tt.c.Sentence(ctx)
			if strings.Contains(whole, phrase) {
				t.Errorf("%v %s: a whole-run comparison reads %q", loc, tt.name, whole)
			}
			if !strings.Contains(timed, phrase) {
				t.Errorf("%v %s: a comparison cut at the same time reads %q, want it to say %q", loc, tt.name, timed, phrase)
			}
		}
	}
}
