package frame

import "testing"

// TestNavItemInitial holds that a department without a photograph shows the
// first character of its name, a whole one: a Chinese character or a letter,
// never half of a multi-byte one.
func TestNavItemInitial(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]string{
		"廚房":      "廚",
		"Kitchen": "K",
		"éclair":  "é",
		"":        "",
	} {
		if got := (NavItem{Name: name}).Initial(); got != want {
			t.Errorf("Initial of %q = %q, want %q", name, got, want)
		}
	}
}
