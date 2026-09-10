package icons_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/koopa0/goen/internal/ui/icons"
)

func TestCategoryKeysMatchTheRenderer(t *testing.T) {
	want := []string{"phone", "laptop", "tablet", "headphones", "watch", "plug", "shield"}
	keys := icons.CategoryKeys()
	if diff := cmp.Diff(want, keys); diff != "" {
		t.Fatalf("CategoryKeys() mismatch (-want +got):\n%s", diff)
	}

	for _, key := range keys {
		if !icons.KnownCategory(key) {
			t.Errorf("CategoryKeys() includes %q, but KnownCategory rejects it", key)
		}
		var rendered strings.Builder
		if err := icons.Category(key).Render(t.Context(), &rendered); err != nil {
			t.Fatalf("render Category(%q): %v", key, err)
		}
		if rendered.Len() == 0 {
			t.Errorf("CategoryKeys() includes %q, but Category renders nothing", key)
		}
	}

	if icons.KnownCategory("rocket") {
		t.Error("KnownCategory accepted an icon Category does not render")
	}
	var unknown strings.Builder
	if err := icons.Category("rocket").Render(t.Context(), &unknown); err != nil {
		t.Fatalf("render unknown category: %v", err)
	}
	if unknown.Len() != 0 {
		t.Errorf("Category(rocket) rendered %q, want nothing", unknown.String())
	}
}

func TestCategoryKeysReturnsFreshStorage(t *testing.T) {
	got := icons.CategoryKeys()
	want := append([]string(nil), got...)
	got[0] = "rocket"

	if diff := cmp.Diff(want, icons.CategoryKeys()); diff != "" {
		t.Errorf("mutating CategoryKeys() changed the next result (-want +got):\n%s", diff)
	}
	if icons.KnownCategory("rocket") {
		t.Error("mutating CategoryKeys() changed KnownCategory")
	}
	if !icons.KnownCategory(want[0]) {
		t.Errorf("mutating CategoryKeys() made %q unknown", want[0])
	}
}
