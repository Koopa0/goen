package admin

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/ui/layouts"
)

// TestTheAllowanceResendFormRendersOnlyWhenAuthorizable holds the resend
// form's markup. The layout job cannot reach it: an operation becomes
// authorizable only 15 minutes after its last send, and no door backdates one.
func TestTheAllowanceResendFormRendersOnlyWhenAuthorizable(t *testing.T) {
	t.Parallel()
	const operation = "01a10697-c708-7b9d-95ec-f699dc0e1a9b"
	for _, tc := range []struct {
		name       string
		authorized bool
	}{
		{name: "authorizable", authorized: true},
		{name: "not authorizable", authorized: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			view := &WorkerHealthView{StrandedClaims: []StrandedClaim{{
				Operation: operation, OrderNumber: "GO-261004-000002", Kind: "allowance",
				Status: "pending", AmountCents: 2590000, Attempts: 1, Sends: 1,
				LastError: "allowance_not_yet_visible", CanAuthorizeResend: tc.authorized,
			}}}
			doc, err := html.Parse(strings.NewReader(renderToString(t, Health(layouts.Page{Title: "health"}, view))))
			if err != nil {
				t.Fatalf("parse the health page: %v", err)
			}
			forms := formsCarrying(doc, "invoice_operation")
			if !tc.authorized {
				if len(forms) != 0 {
					t.Errorf("Health with CanAuthorizeResend false renders %d resend forms, want none", len(forms))
				}
				return
			}
			if len(forms) != 1 {
				t.Fatalf("Health with CanAuthorizeResend true renders %d resend forms, want 1", len(forms))
			}
			form := forms[0]
			if got := attr(form, "method"); got != "post" {
				t.Errorf("resend form method = %q, want post", got)
			}
			if got := attr(form, "action"); got != "/admin/health/reconcile" {
				t.Errorf("resend form action = %q, want /admin/health/reconcile", got)
			}
			if !hasElement(form, func(n *html.Node) bool {
				return n.Data == "input" && attr(n, "type") == "hidden" &&
					attr(n, "name") == "invoice_operation" && attr(n, "value") == operation
			}) {
				t.Errorf("resend form carries no hidden invoice_operation=%s", operation)
			}
			if !hasElement(form, func(n *html.Node) bool {
				_, required := attrPresent(n, "required")
				return n.Data == "input" && attr(n, "type") == "checkbox" && required &&
					attr(n, "name") == "invoice_resolution" && attr(n, "value") == "confirmed_absent"
			}) {
				t.Error("resend form has no required invoice_resolution=confirmed_absent checkbox")
			}
			if !hasElement(form, func(n *html.Node) bool {
				return n.Data == "button" && attr(n, "type") == "submit"
			}) {
				t.Error("resend form has no submit button")
			}
		})
	}
}

// formsCarrying returns the forms holding an input named name.
func formsCarrying(doc *html.Node, name string) []*html.Node {
	var forms []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "form" && hasElement(n, func(c *html.Node) bool {
			return c.Data == "input" && attr(c, "name") == name
		}) {
			forms = append(forms, n)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return forms
}

func hasElement(n *html.Node, match func(*html.Node) bool) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && match(c) || hasElement(c, match) {
			return true
		}
	}
	return false
}

func attr(n *html.Node, key string) string {
	v, _ := attrPresent(n, key)
	return v
}

func attrPresent(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}
