package contactsubject_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/koopa0/goen/internal/contactsubject"
)

func TestSubjectsPreserveTheDurableChoices(t *testing.T) {
	t.Parallel()
	want := []contactsubject.Subject{"訂單問題", "退換貨", "保固維修", "商品諮詢", "合作提案"}
	if diff := cmp.Diff(want, contactsubject.Subjects()); diff != "" {
		t.Errorf("Subjects() mismatch (-want +got):\n%s", diff)
	}
}

func FuzzSubjectKnown(f *testing.F) {
	for _, value := range []string{"訂單問題", "退換貨", "保固維修", "商品諮詢", "合作提案", "", "An order", "退貨", "訂單問題\x00"} {
		f.Add(value)
	}
	f.Fuzz(func(t *testing.T, value string) {
		want := value == "訂單問題" || value == "退換貨" || value == "保固維修" || value == "商品諮詢" || value == "合作提案"
		if got := contactsubject.Subject(value).Known(); got != want {
			t.Errorf("Subject(%q).Known() = %t, want %t", value, got, want)
		}
	})
}
