package questions

import (
	"fmt"
	"strings"
	"testing"

	xhtml "golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

// badgeClasses returns the class attribute of every badge whose text contains word.
func badgeClasses(t *testing.T, page, word string) []string {
	t.Helper()
	root, err := xhtml.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var out []string
	var walk func(n *xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.ElementNode {
			class := attr(n, "class")
			if strings.HasPrefix(class, "goen-badge") && strings.Contains(textOf(n), word) {
				out = append(out, class)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}

func textOf(n *xhtml.Node) string {
	if n.Type == xhtml.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(textOf(c))
	}
	return strings.TrimSpace(b.String())
}

func wantBadge(t *testing.T, page, word, class string) {
	t.Helper()
	got := badgeClasses(t, page, word)
	if len(got) == 0 {
		t.Errorf("no badge says %q", word)
		return
	}
	for _, c := range got {
		if c != class {
			t.Errorf("badge %q has class %q, want %q", word, c, class)
		}
	}
}

// From statusbadge_test.go, moved whole.
func TestQuestionQueueWaitingNeedsYouAndTheCountIsPlainText(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := QuestionsView{Rows: []Question{{ID: "q", ProductName: "P"}}}
	page := renderComponent(t, ctx, Questions(frame.Page{}, view))
	wantBadge(t, page, i18n.T(ctx, i18n.KeyAdminQWaiting), "goen-badge goen-badge--warn")
	count := fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminQuestionsWaiting), "1")
	if !strings.Contains(page, `<p class="goen-admin__headcount">`+count+`</p>`) {
		t.Errorf("the waiting count %q is not plain text in the page head", count)
	}
	if got := badgeClasses(t, page, count); len(got) != 0 {
		t.Errorf("the count is still a badge: %v", got)
	}
}

// From refusedforms_test.go, moved whole.
// TestARefusedAnswerKeepsItsText holds that the reply staff typed stays in its
// box, marked invalid, instead of vanishing with the redirect.
func TestARefusedAnswerKeepsItsText(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := QuestionsView{Rows: []Question{{
		ID: "q1", Body: "多久到貨?", Draft: "一長串回覆", Error: "回覆不能留白",
	}}}
	page := renderComponent(t, ctx, Questions(frame.Page{}, view))

	for _, want := range []string{`>一長串回覆</textarea>`, `aria-invalid="true"`, `id="answer-error-q1"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the refused answer form is missing %q", want)
		}
	}
}
