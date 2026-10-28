package returns

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/returns"
	"github.com/koopa0/goen/internal/ui/widget"
)

// TestNoticesCoverRedirects: every redirect the return forms make carries a
// message.
func TestNoticesCoverRedirects(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("handler.go")
	if err != nil {
		t.Fatalf("read handler.go: %v", err)
	}
	sent := map[string]bool{}
	for _, m := range regexp.MustCompile(`/admin/returns\?([a-z]+)=1`).FindAllStringSubmatch(string(src), -1) {
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

// TestDecidedTargetSaysTheDecision: each decision the form allows has its own
// notice, and none of them says money moved.
func TestDecidedTargetSaysTheDecision(t *testing.T) {
	t.Parallel()
	for kind, want := range map[returns.DecisionKind]string{
		returns.DecisionApprove:   "/admin/returns?approved=1",
		returns.DecisionException: "/admin/returns?exception=1",
		returns.DecisionReject:    "/admin/returns?declined=1",
	} {
		got := decidedTarget(kind)
		if got != want {
			t.Errorf("decidedTarget(%q) = %q, want %q", kind, got, want)
		}
		code := strings.TrimSuffix(strings.TrimPrefix(got, "/admin/returns?"), "=1")
		entry, ok := notices[code]
		if !ok || entry.Outcome != widget.OutcomeDone {
			t.Errorf("decidedTarget(%q) names ?%s=1, which is not a done notice", kind, code)
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
