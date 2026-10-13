package labelling

import (
	"slices"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestNetUnitSymbolForPiecesUsesTheLocaleCountUnit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		locale i18n.Locale
		want   string
	}{
		{locale: i18n.En, want: "pcs"},
		{locale: i18n.ZhHant, want: "件"},
	} {
		t.Run(string(tc.locale), func(t *testing.T) {
			t.Parallel()
			if got := Piece.Symbol(i18n.WithLocale(t.Context(), tc.locale)); got != tc.want {
				t.Errorf("Piece.Symbol() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNetUnitVocabulary(t *testing.T) {
	t.Parallel()
	want := []NetUnit{"g", "kg", "ml", "l", "piece"}
	if !slices.Equal(Units(), want) {
		t.Fatalf("net units=%v, want %v", Units(), want)
	}
	for _, unit := range want {
		if !unit.Known() {
			t.Errorf("known unit %q refused", unit)
		}
	}
	for _, unit := range []NetUnit{"", "oz", "G"} {
		if unit.Known() {
			t.Errorf("unknown unit %q accepted", unit)
		}
	}
}
