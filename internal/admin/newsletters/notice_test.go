package newsletters

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/widget"
)

func TestEveryRedirectTheNewsletterFormsMakeCarriesAMessage(t *testing.T) {
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

func TestARedirectedNoticeIsShownAsItsOwnOutcome(t *testing.T) {
	t.Parallel()
	for _, query := range []string{"saved", "sent", "already"} {
		ctx := i18n.WithLocale(t.Context(), i18n.En)
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/newsletters?"+query+"=1", http.NoBody)
		var page strings.Builder
		if err := Newsletter(layouts.Page{Title: "newsletters"}, NewsletterView{Notice: widget.NoticeFrom(req, notices)}).Render(ctx, &page); err != nil {
			t.Fatal(err)
		}
		got := page.String()
		m, ok := notices[query]
		if !ok {
			t.Fatalf("notices has no entry %q", query)
		}
		if sentence := i18n.T(ctx, m.Key); !strings.Contains(got, sentence) {
			t.Errorf("?%s=1: the page does not show %q", query, sentence)
		}
		if strings.Contains(got, "goen-notice--danger") {
			t.Errorf("?%s=1: a completed send or save is shown as a failure", query)
		}
	}
}

func TestARedirectedNoticeKeepsItsOutcome(t *testing.T) {
	t.Parallel()
	if got := notices["already"].Outcome; got != widget.OutcomeDone {
		t.Errorf(`notices["already"].Outcome = %d, want %d`, got, widget.OutcomeDone)
	}
}
