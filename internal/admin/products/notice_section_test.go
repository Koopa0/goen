package products

import (
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/widget"
)

const noticeSentence = "Notice sentence for the test."

// noticeHolders renders the editor with a notice and returns the ids of the
// sections that hold it, one entry per time the sentence appears.
func noticeHolders(t *testing.T, noticeIn Section) (holders [][]string) {
	t.Helper()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	view := ProductView{
		Slug: "test", Name: "Mug", NoticeIn: noticeIn,
		Notice: widget.Result{Outcome: widget.OutcomeDone, Text: noticeSentence},
	}
	doc, err := html.Parse(strings.NewReader(renderComponent(t, ctx, Product(frame.Page{Title: "Mug"}, view))))
	if err != nil {
		t.Fatal(err)
	}
	for n := range doc.Descendants() {
		if n.Type != html.TextNode || !strings.Contains(n.Data, noticeSentence) {
			continue
		}
		ids := []string{}
		for p := n.Parent; p != nil; p = p.Parent {
			if p.Type == html.ElementNode && strings.Contains(refusalAttr(p, "class"), "goen-admin__section") {
				ids = append(ids, refusalAttr(p, "id"))
			}
		}
		holders = append(holders, ids)
	}
	return holders
}

// TestSection_NoticeSitsInItsSection: a notice a redirect addressed to a section is
// shown once, inside that section, and not again at the head of the page.
func TestSection_NoticeSitsInItsSection(t *testing.T) {
	t.Parallel()
	for _, section := range sections {
		t.Run(string(section), func(t *testing.T) {
			t.Parallel()
			location, err := url.Parse("/admin/products/test?saved=1" + section.Query())
			if err != nil {
				t.Fatal(err)
			}
			named := sectionOf(location.Query().Get("in"))
			if named != section {
				t.Fatalf("a redirect that names %q reads back as %q", section, named)
			}
			got := noticeHolders(t, named)
			if len(got) != 1 {
				t.Fatalf("the notice appears %d times, want once", len(got))
			}
			if len(got[0]) != 1 || got[0][0] != "sec-"+string(section) {
				t.Errorf("the notice sits in sections %v, want only %s", got[0], "sec-"+string(section))
			}
		})
	}
}

// TestSection_NoticeOfNoSectionStaysAtTheHead: a notice that names no section, or one
// the page does not have, is shown once at the head of the page.
func TestSection_NoticeOfNoSectionStaysAtTheHead(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"", "bogus", "images", "SPECS", "sec-specs"} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			got := noticeHolders(t, sectionOf(raw))
			if len(got) != 1 {
				t.Fatalf("in=%q: the notice appears %d times, want once", raw, len(got))
			}
			if len(got[0]) != 0 {
				t.Errorf("in=%q: the notice sits in sections %v, want none", raw, got[0])
			}
		})
	}
}

// TestSection_FormPostsToItsOwnSection: a form whose action carries a fragment carries
// the fragment of the section it sits in, so a redirect that names that section
// puts the notice where the browser lands.
func TestSection_FormPostsToItsOwnSection(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	doc, err := html.Parse(strings.NewReader(renderComponent(t, ctx, Product(frame.Page{Title: "Mug"}, ProductView{
		Slug: "test", Name: "Mug",
		Options: []Option{{ID: "o", Name: "Colour", Values: []OptionValue{{ID: "v", Value: "Red", Option: "Colour"}}}},
	}))))
	if err != nil {
		t.Fatal(err)
	}
	var withFragment int
	for n := range doc.Descendants() {
		if n.Type != html.ElementNode || n.Data != "form" {
			continue
		}
		_, fragment, ok := strings.Cut(refusalAttr(n, "action"), "#")
		if !ok {
			continue
		}
		withFragment++
		if !within(n, fragment) {
			t.Errorf("a form posts to #%s but sits in another section", fragment)
		}
		if section := Section(strings.TrimPrefix(fragment, "sec-")); sectionOf(string(section)) != section {
			t.Errorf("a form posts to #%s, which is no Section", fragment)
		}
	}
	if withFragment != 5 {
		t.Errorf("forms posting to a fragment = %d, want 5: details, the two option forms, variants, specs", withFragment)
	}
}
