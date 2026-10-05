package i18n

import "testing"

func TestMembershipTierWording(t *testing.T) {
	for _, tt := range []struct {
		key    Key
		zhHant string
		en     string
	}{
		{key: "admin.tier.col.band", zhHant: "等級", en: "Tier"},
		{key: "admin.tier.add", zhHant: "新增等級", en: "Add a tier"},
		{
			key:    "admin.tier.nameen.hint",
			zhHant: "會員頁會把等級名稱放進句子裡，所以英文缺一半會讀起來像壞掉。",
			en:     "The account page puts a tier name inside a sentence, so a missing English one leaves it reading as broken.",
		},
	} {
		t.Run(string(tt.key), func(t *testing.T) {
			for _, loc := range Locales() {
				t.Run(string(loc), func(t *testing.T) {
					want := tt.en
					if loc == ZhHant {
						want = tt.zhHant
					}
					if got := T(WithLocale(t.Context(), loc), tt.key); got != want {
						t.Errorf("T(%s, %s) = %q, want %q", loc, tt.key, got, want)
					}
				})
			}
		})
	}
}
