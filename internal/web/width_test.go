package web

import "testing"

func TestFoldWidth(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"0912345678", "0912345678"},
		{"０９１２３４５６７８", "0912345678"},
		{"１１０", "110"},
		{"Ｐｉｘｅｌｉｇｈｔ", "Pixelight"},
		{"６５Ｗ", "65W"},
		{"藍牙　耳機", "藍牙 耳機"},
		{"＋８８６－９１２", "+886-912"},
		{"ｱｲｳ", "ｱｲｳ"},   // half-width kana is not ASCII's to fold
		{"耳機：１", "耳機:1"}, // U+FF1A is in the folded range
		{"｟", "｟"},       // just past U+FF5E
	}
	for _, tt := range tests {
		if got := FoldWidth(tt.in); got != tt.want {
			t.Errorf("FoldWidth(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
