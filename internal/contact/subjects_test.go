package contact

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

// White-box on purpose: the private array is the one source rendered and
// validated, so the test must not reproduce a second list that can drift.
func TestEveryOfferedSubjectIsAcceptedAndTranslated(t *testing.T) {
	t.Parallel()

	for _, offered := range subjects {
		t.Run(offered.Value, func(t *testing.T) {
			t.Parallel()

			msg := Message{
				Name: "王小明", Email: "me@example.com", Subject: offered.Value,
				Body: "我想詢問這件事情",
			}
			if got := Validate(t.Context(), msg); len(got) != 0 {
				t.Errorf("Validate() rejected offered subject %q: %v", offered.Value, got)
			}
			for _, locale := range i18n.Locales() {
				translated := i18n.T(i18n.WithLocale(t.Context(), locale), offered.LabelKey)
				if strings.TrimSpace(translated) == "" || translated == string(offered.LabelKey) {
					t.Errorf("subject %q names %q, which has no %s translation",
						offered.Value, offered.LabelKey, locale)
				}
			}
		})
	}
}
