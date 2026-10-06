package pages

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestWarrantySerialRefusalKeepsOnlyItsOwnDraftAndExplanation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		locale  i18n.Locale
		refusal string
	}{
		{i18n.En, "That serial number is already registered. Please check the number on the unit."},
		{i18n.ZhHant, "這個序號已經登錄過了。請再確認一次機身上的號碼。"},
	} {
		t.Run(tc.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tc.locale)
			view := WarrantyOrderView{Number: "GO-000001", Lines: []WarrantyLine{
				{ID: "owned", Name: "Refused item", HasTerm: true, Months: 12, Delivered: 1, DraftSerial: " Raw & \"serial\" ", SerialRefusal: tc.refusal},
				{ID: "sibling", Name: "Other item", HasTerm: true, Months: 12, Delivered: 1},
			}}
			body := renderWarrantyInLocale(t, ctx, WarrantyOrder(layouts.Page{}, view))
			nodes := warrantyNodesByID(t, body)
			input := nodes["serial-owned"]
			if input == nil {
				t.Fatal("refused serial input is missing")
			}
			attrs := warrantyAttributes(input)
			if attrs["value"] != " Raw & \"serial\" " || attrs["aria-invalid"] != "true" {
				t.Errorf("refused serial value=%q, invalid=%q; want raw draft and true", attrs["value"], attrs["aria-invalid"])
			}
			if got := attrs["aria-describedby"]; got != "serial-hint-owned serial-owned-error" {
				t.Errorf("serial explanation IDs=%q, want hint and owning error", got)
			}
			reason := nodes["serial-owned-error"]
			if reason == nil || warrantyNodeText(reason) != tc.refusal {
				t.Errorf("serial error=%q, want %q", warrantyNodeText(reason), tc.refusal)
			}
			sibling := warrantyAttributes(nodes["serial-sibling"])
			if sibling["value"] != "" || sibling["aria-invalid"] != "" || sibling["aria-describedby"] != "serial-hint-sibling" || nodes["serial-sibling-error"] != nil {
				t.Errorf("sibling inherited a refused draft or error: %v", sibling)
			}
		})
	}
}

func warrantyNodesByID(t *testing.T, body string) map[string]*html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	nodes := make(map[string]*html.Node)
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for _, a := range n.Attr {
			if a.Key == "id" {
				if nodes[a.Val] != nil {
					t.Errorf("duplicate ID %q", a.Val)
				}
				nodes[a.Val] = n
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return nodes
}

func warrantyAttributes(n *html.Node) map[string]string {
	attrs := make(map[string]string)
	if n != nil {
		for _, a := range n.Attr {
			attrs[a.Key] = a.Val
		}
	}
	return attrs
}

func warrantyNodeText(n *html.Node) string {
	if n == nil {
		return ""
	}
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		b.WriteString(warrantyNodeText(child))
	}
	return b.String()
}
