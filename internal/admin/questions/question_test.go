package questions

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestQuestionsAnswerList: question answers keep list semantics and show only the metadata present.
func TestQuestionsAnswerList(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, tt := range []struct {
			name, author, wantMetadata string
		}{
			{name: "named author", author: "Mina", wantMetadata: "Mina · 2026-10-08 09:30"},
			{name: "missing author", wantMetadata: "2026-10-08 09:30"},
		} {
			t.Run(string(locale)+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				view := QuestionsView{Rows: []Question{{ID: "question", Answers: []Answer{
					{ID: "answer", Body: "Reply", Author: tt.author, At: "2026-10-08 09:30"},
				}}}}
				page := renderComponent(t, ctx, Questions(frame.Page{}, view))
				doc, err := html.Parse(strings.NewReader(page))
				if err != nil {
					t.Fatal(err)
				}
				got := make(map[string]string)
				hasElement(doc, func(n *html.Node) bool {
					if n.Type != html.ElementNode || n.Data != "p" || n.FirstChild == nil || n.FirstChild.Data != "Reply" {
						return false
					}
					item := n.Parent
					list := item.Parent
					got["item"] = item.Data
					got["list"] = list.Data
					got["role"] = attr(list, "role")
					for _, class := range strings.Fields(attr(list, "class")) {
						if class == "goen-modqueue" {
							got["list reset"] = class
						}
					}
					for child := item.FirstChild; child != nil; child = child.NextSibling {
						if child.Type == html.ElementNode && child.Data == "span" && attr(child, "class") == "goen-admin__meta" {
							var metadata strings.Builder
							for text := child.FirstChild; text != nil; text = text.NextSibling {
								if text.Type == html.TextNode {
									metadata.WriteString(text.Data)
								}
							}
							got["metadata"] = strings.TrimSpace(metadata.String())
						}
					}
					return true
				})
				want := map[string]string{
					"item": "li", "list": "ul", "role": "list", "list reset": "goen-modqueue", "metadata": tt.wantMetadata,
				}
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("rendered answer differs (-want +got):\n%s", diff)
				}
			})
		}
	}
}

// TestQuestionsAnswerText: the question queue shows each answer without executing its text.
func TestQuestionsAnswerText(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(string(locale), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), locale)
			view := QuestionsView{Rows: []Question{{ID: "question", Answers: []Answer{
				{ID: "official", Body: "Original specification <script>alert(1)</script>", IsStaff: true},
				{ID: "withdrawn", Body: "Earlier reply", Hidden: true},
			}}}}
			page := renderComponent(t, ctx, Questions(frame.Page{}, view))
			for _, text := range []string{"Original specification", "Earlier reply", i18n.T(ctx, i18n.KeyStaffAnswer), i18n.T(ctx, i18n.KeyAdminQHidden)} {
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

// TestQuestionsHiddenRestore: hidden questions offer a restore button without an answer box.
func TestQuestionsHiddenRestore(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	view := QuestionsView{Hidden: true, Rows: []Question{{ID: "question", Hidden: true}}}
	page := renderComponent(t, ctx, Questions(frame.Page{}, view))
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

// TestQuestionsEmptyHiddenQueue: an empty hidden queue does not say nobody has asked.
func TestQuestionsEmptyHiddenQueue(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	page := renderComponent(t, ctx, Questions(frame.Page{}, QuestionsView{Hidden: true}))
	if !strings.Contains(page, "No hidden questions") || strings.Contains(page, "Nobody has asked") {
		t.Error("empty hidden view misstates the visible question queue")
	}
}
