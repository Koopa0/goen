// Package pagestest holds assertions shared by the tests of pages rendered
// from internal/ui/pages.
package pagestest

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"
)

// AssertEmailLink checks that an emailed-link page names exactly one heading,
// one reason and, unless destination is empty, one primary action pointing at
// destination, all inside its notice panel.
func AssertEmailLink(t *testing.T, body, heading, reason, destination string) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Error(err)
		return
	}
	want := emailLinkSelection{Headings: []string{heading}, Reasons: []string{reason}}
	if destination != "" {
		want.Destinations = []string{destination}
	}
	if diff := cmp.Diff(want, selectEmailLink(doc)); diff != "" {
		t.Errorf("email link selection (-want +got):\n%s", diff)
	}
}

type emailLinkSelection struct {
	Headings, Reasons, Destinations []string
}

// selectEmailLink reads the notice panels only: the footer also offers signup
// and contact, and only the notice is the recovery.
func selectEmailLink(doc *html.Node) emailLinkSelection {
	var got emailLinkSelection
	for panel := range doc.Descendants() {
		if !hasClass(panel, "notice") {
			continue
		}
		for n := range panel.Descendants() {
			if n.Type != html.ElementNode {
				continue
			}
			switch {
			case n.Data == "h1":
				got.Headings = append(got.Headings, text(n))
			case hasClass(n, "notice__body"):
				got.Reasons = append(got.Reasons, text(n))
			case hasClass(n, "goen-btn--primary"):
				got.Destinations = append(got.Destinations, destinationOf(n, panel))
			}
		}
	}
	return got
}

// destinationOf is a link's href, or the action of the form a button submits.
func destinationOf(n, panel *html.Node) string {
	if n.Data == "a" {
		return attr(n, "href")
	}
	for p := n.Parent; p != nil && p != panel; p = p.Parent {
		if p.Data == "form" {
			return attr(p, "action")
		}
	}
	return attr(n, "href")
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func hasClass(n *html.Node, class string) bool {
	for value := range strings.FieldsSeq(attr(n, "class")) {
		if value == class {
			return true
		}
	}
	return false
}

func text(n *html.Node) string {
	var result strings.Builder
	for child := range n.Descendants() {
		if child.Type == html.TextNode {
			result.WriteString(child.Data)
		}
	}
	return result.String()
}
