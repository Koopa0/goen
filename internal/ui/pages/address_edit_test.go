package pages

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

func TestSavedAddressesOfferEditAndNativeDeleteConfirmation(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			id := "11111111-1111-4111-8111-111111111111"
			body := renderComponent(t, ctx, Account(AccountMeta(ctx), &AccountView{Addresses: []AccountAddress{{
				ID: id, Label: "Home", Name: "Recipient", Phone: "0912345678", PostalCode: "110", City: "Taipei", District: "Xinyi", Street: "1 Test Road", Default: true,
			}}}))
			doc, err := html.Parse(strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			edit := findDescendant(doc, func(n *html.Node) bool {
				return n.Type == html.ElementNode && n.Data == "a" && attrValue(n, "href") == "/account/addresses/"+id+"/edit#addresses-heading"
			})
			if edit == nil {
				t.Error("saved address has no edit link to the address section")
			}
			form := findDescendant(doc, func(n *html.Node) bool {
				return n.Type == html.ElementNode && n.Data == "form" && attrValue(n, "action") == "/account/addresses/delete"
			})
			if form == nil {
				t.Fatal("saved address lost its delete form")
			}
			if attrValue(form, "method") != "post" {
				t.Error("saved-address delete is not a plain POST")
			}
			disclosure := form.Parent
			for disclosure != nil && disclosure.Data != "details" {
				disclosure = disclosure.Parent
			}
			if disclosure == nil {
				t.Fatal("delete submits without a native confirmation disclosure")
			}
			for _, a := range disclosure.Attr {
				if a.Key == "open" {
					t.Error("delete confirmation starts open")
				}
			}
			summary := findDescendant(disclosure, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "summary" })
			question, buttonLabel := "Delete “Home”?", "Delete address"
			if locale == i18n.ZhHant {
				question = "\u78ba\u5b9a\u522a\u9664\u300cHome\u300d\uff1f"
				buttonLabel = "\u522a\u9664\u5730\u5740"
			}
			if summary == nil || !strings.Contains(nodeText(disclosure), question) {
				t.Errorf("native confirmation lacks a summary or question %q", question)
			}
			button := findDescendant(form, func(n *html.Node) bool {
				return n.Type == html.ElementNode && n.Data == "button" && attrValue(n, "type") == "submit"
			})
			if button == nil || nodeText(button) != buttonLabel || !strings.Contains(attrValue(button, "class"), "goen-btn--danger") {
				t.Errorf("confirmation does not expose danger submit %q", buttonLabel)
			}
		})
	}
}

func TestAddressEditKeepsItsDraftAndDefaultWithoutOfferingDefaultChanges(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			id := "11111111-1111-4111-8111-111111111111"
			v := &AccountView{
				AddressDraft:  &AddressDraft{ID: id, Label: "  Edited home  ", Name: "Edited recipient", Phone: "wrong phone", PostalCode: "110", City: "Taipei", District: "Xinyi", Street: "  2 Test Road  ", Default: true},
				AddressErrors: map[string]string{"phone": "That phone number cannot be used."},
			}
			body := renderComponent(t, ctx, Account(AccountMeta(ctx), v))
			doc, err := html.Parse(strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			form := findDescendant(doc, func(n *html.Node) bool {
				return n.Type == html.ElementNode && n.Data == "form" && attrValue(n, "action") == "/account/addresses/update#addresses-heading"
			})
			if form == nil {
				t.Fatal("edit draft is rendered as an add form")
			}
			if attrValue(form, "method") != "post" {
				t.Error("address edit is not a plain POST")
			}
			hasOpen := false
			for _, attr := range form.Parent.Attr {
				hasOpen = hasOpen || attr.Key == "open"
			}
			if !hasOpen {
				t.Error("refused edit form remains collapsed")
			}
			for name, value := range map[string]string{"address": id, "label": v.AddressDraft.Label, "name": v.AddressDraft.Name, "phone": v.AddressDraft.Phone, "postal_code": "110", "city": "Taipei", "district": "Xinyi", "street": v.AddressDraft.Street} {
				field := findDescendant(form, func(n *html.Node) bool {
					return n.Type == html.ElementNode && n.Data == "input" && attrValue(n, "name") == name
				})
				if field == nil || attrValue(field, "value") != value {
					t.Errorf("edit field %s lost submitted value %q", name, value)
				}
				if name == "phone" && field != nil && (attrValue(field, "aria-invalid") != "true" || !strings.Contains(attrValue(field, "aria-describedby"), "addr-phone-error")) {
					t.Error("refused phone is not linked to its field error")
				}
			}
			if findDescendant(form, func(n *html.Node) bool {
				return n.Type == html.ElementNode && n.Data == "input" && attrValue(n, "name") == "default"
			}) != nil {
				t.Error("editing text offers a change to the saved default flag")
			}
		})
	}
}

func TestAddressEmptyStateExplainsTheCheckoutChoice(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			body := renderComponent(t, ctx, Account(AccountMeta(ctx), &AccountView{}))
			want := "No saved addresses yet. Add one to use it at checkout."
			if locale == i18n.ZhHant {
				want = "\u9084\u6c92\u6709\u5e38\u7528\u5730\u5740\u3002\u65b0\u589e\u5f8c\uff0c\u7d50\u5e33\u6642\u53ef\u4ee5\u76f4\u63a5\u9078\u7528\u3002"
			}
			if !strings.Contains(body, want) {
				t.Errorf("empty address book omits %q", want)
			}
		})
	}
}
