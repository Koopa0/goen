package web

import "testing"

func TestHasControlChars(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		in   string
		want bool
	}{
		{"plain text", "王小明 Ada", false},
		{"empty", "", false},
		{"newline", "a\nb", true},
		{"NUL", "a\x00b", true},
		{"C1 control", "a\u0085b", true},
	} {
		if got := HasControlChars(tt.in); got != tt.want {
			t.Errorf("HasControlChars(%q) = %t, want %t", tt.in, got, tt.want)
		}
	}
}
