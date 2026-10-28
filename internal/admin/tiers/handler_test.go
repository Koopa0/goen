package tiers

import (
	"os"
	"regexp"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/widget"
)

// TestNoticesCoverRedirects: every redirect the tier forms make carries a message.
func TestNoticesCoverRedirects(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("handler.go")
	if err != nil {
		t.Fatalf("read handler.go: %v", err)
	}
	sent := map[string]bool{}
	for _, m := range regexp.MustCompile(`/admin/tiers\?(?:[a-z]+=[^&"]+&)*([a-z]+)=1`).FindAllStringSubmatch(string(src), -1) {
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

// TestNoticesDoneSentencesDiffer: no two actions share a sentence, so a save says
// which change it was.
func TestNoticesDoneSentencesDiffer(t *testing.T) {
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
