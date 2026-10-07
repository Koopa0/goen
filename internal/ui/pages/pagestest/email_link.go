package pagestest

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"
)

func AssertEmailLink(t *testing.T, body, heading, reason, destination string) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Error(err)
		return
	}
	type selection struct {
		Headings, Reasons, Destinations []string
	}
	var got selection
	// The footer also offers signup and contact; only the notice is the recovery.
	for panel := range doc.Descendants() {
		if !hasClass(panel, "notice") {
			continue
		}
		for n := range panel.Descendants() {
			if n.Type != html.ElementNode {
				continue
			}
			if n.Data == "h1" {
				got.Headings = append(got.Headings, text(n))
			}
			if hasClass(n, "notice__body") {
				got.Reasons = append(got.Reasons, text(n))
			}
			if hasClass(n, "goen-btn--primary") {
				dest := attr(n, "href")
				if n.Data != "a" {
					for p := n.Parent; p != nil && p != panel; p = p.Parent {
						if p.Data == "form" {
							dest = attr(p, "action")
							break
						}
					}
				}
				got.Destinations = append(got.Destinations, dest)
			}
		}
	}
	want := selection{Headings: []string{heading}, Reasons: []string{reason}}
	if destination != "" {
		want.Destinations = []string{destination}
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("email link selection (-want +got):\n%s", diff)
	}
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
