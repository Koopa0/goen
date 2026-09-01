package contact

import (
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

// This is a white-box test on purpose: the private array is the one source
// rendered by subjectChoices and accepted by Validate, so the test must not
// reproduce a second list that can drift away from it.
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
			if _, ok := i18n.MessageFor(offered.LabelKey); !ok {
				t.Errorf("subject %q names %q, which the catalogue does not define",
					offered.Value, offered.LabelKey)
			}
		})
	}
}
