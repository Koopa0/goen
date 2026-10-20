package uitest

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// RefusedForm is one refused form on a rendered back-office page, as its
// page-top summary has to present it.
type RefusedForm struct {
	// Action is where the form posts, including its #section.
	Action string
	// Target is the id of the section the form returns to.
	Target string
	// First is the id of the first refused control, the one the summary links.
	First string
	// Title is the summary's localized name for the form.
	Title string
	// Count is how many fields the summary says were refused.
	Count int
}

// AssertRefusalSummary checks a refused form's page: the form posts to its
// own section; a page-top alert names the form, counts its refused fields and
// links to the first one; that alert comes before every back-office form; and
// every invalid control is described by an element that exists and has text.
func AssertRefusalSummary(t *testing.T, body string, f RefusedForm) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var alert, form, target, first *html.Node
	ids := map[string]*html.Node{}
	for n := range doc.Descendants() {
		if n.Type != html.ElementNode {
			continue
		}
		id := attr(n, "id")
		if id != "" {
			ids[id] = n
		}
		if n.Data == "form" && attr(n, "action") == f.Action {
			form = n
		}
		if id == f.Target {
			target = n
		}
		if id == f.First {
			first = n
		}
		if attr(n, "role") == "alert" && strings.Contains(descendantText(n), f.Title) {
			for link := range n.Descendants() {
				if link.Data == "a" && attr(link, "href") == "#"+f.First {
					alert = n
				}
			}
		}
	}
	if form == nil || target == nil {
		t.Errorf("form must post to its section: %s", f.Action)
	}
	if alert == nil {
		t.Fatalf("page-top alert must name %q and link to #%s", f.Title, f.First)
	}
	count := "2"
	if f.Count == 1 {
		count = "1"
	}
	if !strings.Contains(descendantText(alert), count) {
		t.Errorf("alert does not count %d invalid fields", f.Count)
	}
	for n := range doc.Descendants() {
		if n == alert {
			break
		}
		if n.Data == "form" && strings.Contains(attr(n, "class"), "goen-admin__") {
			t.Error("summary must precede the forms")
			break
		}
	}
	if first == nil || attr(first, "aria-invalid") != "true" {
		t.Errorf("first invalid control %s is not marked", f.First)
	}
	for n := range doc.Descendants() {
		if n.Type != html.ElementNode || attr(n, "aria-invalid") != "true" {
			continue
		}
		describes := strings.Fields(attr(n, "aria-describedby"))
		if len(describes) == 0 {
			t.Errorf("invalid control %s has no description", attr(n, "id"))
		}
		for _, id := range describes {
			if ids[id] == nil || strings.TrimSpace(descendantText(ids[id])) == "" {
				t.Errorf("invalid control references missing error %s", id)
			}
		}
	}
}

// descendantText is every text node under n, untrimmed and in document order.
func descendantText(n *html.Node) string {
	var out strings.Builder
	for child := range n.Descendants() {
		if child.Type == html.TextNode {
			out.WriteString(child.Data)
		}
	}
	return out.String()
}
