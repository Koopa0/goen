package layouts_test

import (
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestJoinListFollowsTheReadersLanguage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		locale i18n.Locale
		names  []string
		want   string
	}{
		{i18n.ZhHant, nil, ""},
		{i18n.ZhHant, []string{"書籍"}, "書籍"},
		{i18n.ZhHant, []string{"書籍", "文具"}, "書籍、文具"},
		{i18n.ZhHant, []string{"書籍", "文具", "食品"}, "書籍、文具、食品"},
		{i18n.En, []string{"Books"}, "Books"},
		{i18n.En, []string{"Books", "Stationery"}, "Books and Stationery"},
		{i18n.En, []string{"Books", "Stationery", "Food"}, "Books, Stationery and Food"},
	}
	for _, tt := range tests {
		ctx := i18n.WithLocale(t.Context(), tt.locale)
		if got := layouts.JoinList(ctx, tt.names); got != tt.want {
			t.Errorf("%s JoinList(%q) = %q, want %q", tt.locale, tt.names, got, tt.want)
		}
	}
}
