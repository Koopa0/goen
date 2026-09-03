package pages

import (
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestAClawbackSaysWhatWasRequestedAndWhatWasShort(t *testing.T) {
	entry := PointsEntry{
		Kind: "clawback", Points: -40, RequestedPoints: 100, ShortfallPoints: 60,
	}

	for _, tc := range []struct {
		locale i18n.Locale
		what   string
		detail string
	}{
		{i18n.ZhHant, "退貨扣回", "應扣回 100 點；實際扣回 40 點；未扣回 60 點"},
		{i18n.En, "Reversed for a return", "Requested 100 points; reversed 40; shortfall 60"},
	} {
		t.Run(tc.locale.Tag(), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), tc.locale)
			if got := entry.What(ctx); got != tc.what {
				t.Errorf("What() = %q, want %q", got, tc.what)
			}
			if got := entry.Detail(ctx); got != tc.detail {
				t.Errorf("Detail() = %q, want %q", got, tc.detail)
			}
		})
	}

	whollyConsumed := PointsEntry{
		Kind: "clawback", Points: 0, RequestedPoints: 75, ShortfallPoints: 75,
	}
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	if got := whollyConsumed.Detail(ctx); got != "Requested 75 points; reversed 0; shortfall 75" {
		t.Errorf("zero-point clawback detail = %q", got)
	}
}

func TestEveryPointsEntryKindHasACompletePresentation(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)

	for _, tt := range []struct {
		kind   PointsEntryKind
		points int64
		earned bool
		amount string
		what   string
	}{
		{kind: "award", points: 12, earned: true, amount: "+12", what: "Earned on a purchase"},
		{kind: "spend", points: -10, amount: "-10", what: "Redeemed for store credit"},
		{kind: "clawback", points: -7, amount: "-7", what: "Reversed for a return"},
	} {
		t.Run(string(tt.kind), func(t *testing.T) {
			t.Parallel()
			entry := PointsEntry{Kind: tt.kind, Points: tt.points}
			if got := entry.Earned(); got != tt.earned {
				t.Errorf("Earned() = %v, want %v", got, tt.earned)
			}
			if got := entry.Amount(); got != tt.amount {
				t.Errorf("Amount() = %q, want %q", got, tt.amount)
			}
			if got := entry.What(ctx); got != tt.what {
				t.Errorf("What() = %q, want %q", got, tt.what)
			}
			_ = entry.Detail(ctx)
		})
	}
}

func TestAnUnknownPointsEntryKindIsAProgrammingError(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	entry := PointsEntry{Kind: "future_kind"}

	for name, present := range map[string]func(){
		"Earned": func() { _ = entry.Earned() },
		"Amount": func() { _ = entry.Amount() },
		"What":   func() { _ = entry.What(ctx) },
		"Detail": func() { _ = entry.Detail(ctx) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			defer func() {
				if recover() == nil {
					t.Error("unknown closed ledger kind did not panic")
				}
			}()
			present()
		})
	}
}
