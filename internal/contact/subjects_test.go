package contact

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/koopa0/goen/internal/contactsubject"
	"github.com/koopa0/goen/internal/i18n"
)

func TestEveryOfferedSubjectIsAcceptedAndTranslated(t *testing.T) {
	t.Parallel()

	for _, offered := range contactsubject.Subjects() {
		t.Run(string(offered), func(t *testing.T) {
			t.Parallel()

			msg := Message{
				Name: "王小明", Email: "me@example.com", Subject: offered,
				Body: "我想詢問這件事情",
			}
			if got := Validate(t.Context(), msg); len(got) != 0 {
				t.Errorf("Validate() rejected offered subject %q: %v", offered, got)
			}
			for _, locale := range i18n.Locales() {
				key, ok := offered.LabelKey()
				translated := i18n.T(i18n.WithLocale(t.Context(), locale), key)
				if !ok || strings.TrimSpace(translated) == "" || translated == string(key) {
					t.Errorf("subject %q names %q, which has no %s translation",
						offered, key, locale)
				}
			}
		})
	}
}

func TestContactPickerKeepsTheValuesAndLocalizedLabels(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		locale i18n.Locale
		labels []string
	}{
		{i18n.ZhHant, []string{"訂單問題", "退貨", "保固維修", "商品諮詢", "合作提案"}},
		{i18n.En, []string{"An order", "Returns", "Warranty or repair", "A product question", "A partnership"}},
	} {
		t.Run(tc.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			got := subjectChoices(i18n.WithLocale(t.Context(), tc.locale))
			values := make([]string, 0, len(got))
			labels := make([]string, 0, len(got))
			for _, choice := range got {
				values = append(values, choice.Value)
				labels = append(labels, choice.Label)
			}
			if diff := cmp.Diff([]string{"訂單問題", "退換貨", "保固維修", "商品諮詢", "合作提案"}, values); diff != "" {
				t.Errorf("subjectChoices() values (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.labels, labels); diff != "" {
				t.Errorf("subjectChoices() labels (-want +got):\n%s", diff)
			}
		})
	}
}

func TestAnUnknownSubjectIsRefusedBeforeStorage(t *testing.T) {
	t.Parallel()
	if err := (&Store{}).Create(t.Context(), Message{Subject: "An order"}); err == nil {
		t.Fatal("Create(unknown subject) = nil, want refusal before any database call")
	}
}
