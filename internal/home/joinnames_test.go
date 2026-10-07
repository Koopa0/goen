package home

import "testing"

func TestJoinNamesKeepsADotOffTheStartOfALine(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		names []string
		want  string
	}{
		{"none", nil, ""},
		{"one", []string{"手機"}, "手機"},
		{"two", []string{"手機", "筆電"}, "手機 · 筆電"},
		{"space inside a name", []string{"Bags & hats", "Clothing"}, "Bags & hats · Clothing"},
	} {
		if got := joinNames(tt.names); got != tt.want {
			t.Errorf("%s: joinNames(%q) = %q, want %q", tt.name, tt.names, got, tt.want)
		}
	}
}
