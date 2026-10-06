package contactsubject_test

import (
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/koopa0/goen/internal/contactsubject"
)

var durableSubjects = []string{"訂單問題", "退換貨", "保固維修", "商品諮詢", "合作提案"}

func TestChoicesPreserveTheDurableSubjects(t *testing.T) {
	t.Parallel()
	choices := contactsubject.Choices()
	got := make([]string, 0, len(choices))
	for _, choice := range choices {
		got = append(got, string(choice.Value))
	}
	if diff := cmp.Diff(durableSubjects, got); diff != "" {
		t.Errorf("Choices() subjects (-want +got):\n%s", diff)
	}
}

func FuzzSubjectKnown(f *testing.F) {
	for _, value := range durableSubjects {
		f.Add(value)
	}
	for _, value := range []string{"", "An order", "退貨", "訂單問題\x00"} {
		f.Add(value)
	}
	f.Fuzz(func(t *testing.T, value string) {
		want := slices.Contains(durableSubjects, value)
		if got := contactsubject.Subject(value).Known(); got != want {
			t.Errorf("Subject(%q).Known() = %t, want %t", value, got, want)
		}
	})
}
