package admin

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestQuestionQueueShowsEachAnswerWithoutExecutingItsText(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(string(locale), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), locale)
			view := QuestionsView{Rows: []Question{{ID: "question", Replies: []QuestionAnswer{
				{ID: "official", Body: "Original specification <script>alert(1)</script>", Staff: true},
				{ID: "withdrawn", Body: "Earlier reply", Hidden: true},
			}}}}
			page := renderComponent(t, ctx, Questions(layouts.Page{}, view))
			for _, text := range []string{`data-answer-id="official"`, `data-answer-id="withdrawn"`, "Original specification", "Earlier reply", i18n.T(ctx, i18n.KeyStaffAnswer), i18n.T(ctx, i18n.KeyAdminQHidden)} {
				if !strings.Contains(page, text) {
					t.Errorf("answer queue lacks %q", text)
				}
			}
			if strings.Contains(page, "<script>alert(1)</script>") {
				t.Error("answer text became executable markup")
			}
		})
	}
}

func TestHiddenQuestionsOfferRestoreWithoutAnAnswerBox(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	view := QuestionsView{Hidden: true, Rows: []Question{{ID: "question", Hidden: true}}}
	page := renderComponent(t, ctx, Questions(layouts.Page{}, view))
	for _, text := range []string{`method="post"`, `action="/admin/questions/question"`, "Show this question again"} {
		if !strings.Contains(page, text) {
			t.Errorf("restore form lacks %q", text)
		}
	}
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Data == "button" {
			var name, value string
			for _, attr := range n.Attr {
				if attr.Key == "name" {
					name = attr.Val
				}
				if attr.Key == "value" {
					value = attr.Val
				}
			}
			found = found || (name == "action" && value == "show")
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(doc)
	if !found {
		t.Error("restore form has no show action button")
	}
	if strings.Contains(page, `<textarea`) || strings.Contains(page, `value="answer"`) || strings.Contains(page, `value="hide"`) || view.Waiting() != 0 {
		t.Error("a hidden question still offers answering/hiding or counts as awaiting an answer")
	}
}

func TestEmptyHiddenQueueDoesNotSayNobodyHasAsked(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	page := renderComponent(t, ctx, Questions(layouts.Page{}, QuestionsView{Hidden: true}))
	if !strings.Contains(page, "No hidden questions") || strings.Contains(page, "Nobody has asked") {
		t.Error("empty hidden view misstates the visible question queue")
	}
}
