package faq

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/widget"
)

// TestNoticesCoverRedirects: every redirect the FAQ forms make carries a message.
func TestNoticesCoverRedirects(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("handler.go")
	if err != nil {
		t.Fatalf("read handler.go: %v", err)
	}
	sent := map[string]bool{}
	for _, m := range regexp.MustCompile(`[?&"]([a-z]+)=1`).FindAllStringSubmatch(string(src), -1) {
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

// TestFAQNoticeShown: a redirected notice is shown as its own outcome.
func TestFAQNoticeShown(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		query   string
		failure bool
	}{{"saved", false}, {"refused", true}} {
		ctx := i18n.WithLocale(t.Context(), i18n.En)
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/faq?"+tc.query+"=1", http.NoBody)
		var page strings.Builder
		if err := FAQ(frame.Page{Title: "faq"}, &View{Notice: widget.NoticeFrom(req, notices)}).Render(ctx, &page); err != nil {
			t.Fatal(err)
		}
		got := page.String()
		m, ok := notices[tc.query]
		if !ok {
			t.Fatalf("notices has no entry %q", tc.query)
		}
		if sentence := i18n.T(ctx, m.Key); !strings.Contains(got, sentence) {
			t.Errorf("?%s=1: the page does not show %q", tc.query, sentence)
		}
		if danger := strings.Contains(got, "goen-notice--danger"); danger != tc.failure {
			t.Errorf("?%s=1: danger treatment = %t, want %t", tc.query, danger, tc.failure)
		}
	}
}

// TestNoticesOutcome: a redirected notice keeps its outcome.
func TestNoticesOutcome(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]widget.Outcome{
		"saved":   widget.OutcomeDone,
		"refused": widget.OutcomeRefused,
	} {
		if got := notices[name].Outcome; got != want {
			t.Errorf("notices[%q].Outcome = %d, want %d", name, got, want)
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
