package contact

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/contactsubject"
	"github.com/koopa0/goen/internal/i18n"
)

func TestEveryOfferedSubjectIsAcceptedAndTranslated(t *testing.T) {
	t.Parallel()

	for _, choice := range contactsubject.Choices() {
		offered := choice.Value
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

func TestAnUnknownSubjectIsRefusedBeforeStorage(t *testing.T) {
	t.Parallel()
	if err := (&Store{}).Create(t.Context(), Message{Subject: "An order"}); err == nil {
		t.Fatal("Create(unknown subject) = nil, want refusal before any database call")
	}
}
