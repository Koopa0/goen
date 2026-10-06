package health

import (
	"testing"

	"github.com/koopa0/goen/internal/ui/components"
)

func TestARedirectedNoticeKeepsItsOutcome(t *testing.T) {
	t.Parallel()
	pinned := map[string]components.Outcome{
		"notflagged": components.OutcomeDone,
	}
	for name, want := range pinned {
		if got := notices[name].Outcome; got != want {
			t.Errorf("notices[%q].Outcome = %d, want %d", name, got, want)
		}
	}
}
