package web

import "testing"

func TestASlugIsLowerCaseWordsJoinedByHyphens(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]bool{
		"koto-cable": true, "a1": true, "": false, "Koto": false, "a--b": false,
		"-a": false, "a-": false, "a b": false, "a/b": false,
	} {
		if got := ValidSlug(in); got != want {
			t.Errorf("ValidSlug(%q) = %t, want %t", in, got, want)
		}
	}
}
