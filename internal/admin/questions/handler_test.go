package questions

import (
	"os"
	"regexp"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/widget"
)

// TestNoticesCoverRedirects: every redirect the question forms make carries a message.
func TestNoticesCoverRedirects(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("handler.go")
	if err != nil {
		t.Fatalf("read handler.go: %v", err)
	}
	sent := map[string]bool{}
	for _, m := range regexp.MustCompile(`/admin/questions\?([a-z]+)=1`).FindAllStringSubmatch(string(src), -1) {
		sent[m[1]] = true
	}
	if len(sent) == 0 {
		t.Fatal("no redirect parameter found; the parser stopped matching")
	}
	for name := range sent {
		if _, ok := notices[name]; !ok {
			t.Errorf("?%s=1 carries no message: the page renders nothing after the button", name)
		}
	}
	for name := range notices {
		if !sent[name] {
			t.Errorf("notice %q names a parameter no redirect writes", name)
		}
	}
}

// TestSavedTargetSaysTheAction: each moderation action goes to its own notice,
// and an answer, or none named, to the reply's.
func TestSavedTargetSaysTheAction(t *testing.T) {
	t.Parallel()
	for action, want := range map[Action]string{
		ActionHide:       "/admin/questions?questionhidden=1",
		ActionShow:       "/admin/questions?questionshown=1",
		ActionHideAnswer: "/admin/questions?answerhidden=1",
		ActionAnswer:     "/admin/questions?answered=1",
		"":               "/admin/questions?answered=1",
	} {
		if got := savedTarget(action); got != want {
			t.Errorf("savedTarget(%q) = %q, want %q", action, got, want)
		}
	}
}

// TestDoneNoticesAreDistinct: no two actions share a sentence, so a save says
// which change it was.
func TestDoneNoticesAreDistinct(t *testing.T) {
	t.Parallel()
	seen := map[i18n.Key]string{}
	for name, e := range notices {
		if e.Outcome != widget.OutcomeDone {
			continue
		}
		if first, dup := seen[e.Key]; dup {
			t.Errorf("?%s=1 and ?%s=1 both say %q", first, name, e.Key)
		}
		seen[e.Key] = name
	}
}
