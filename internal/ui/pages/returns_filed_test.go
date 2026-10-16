package pages

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

func TestFiledReturnShowsRequestSentInPlaceOfTheOpenRequestNotice(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		for _, filed := range []bool{false, true} {
			ctx := i18n.WithLocale(t.Context(), locale)
			body := renderComponent(t, ctx, Returns(ReturnsMeta(ctx, "GO-260101-000001"), ReturnsView{Number: "GO-260101-000001", HasOpen: true, Filed: filed}))
			doc, err := html.Parse(strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			sent := findDescendant(doc, func(n *html.Node) bool { return n.Type == html.ElementNode && attrValue(n, "id") == "return-sent" })
			inflight := findDescendant(doc, func(n *html.Node) bool { return n.Type == html.ElementNode && attrValue(n, "id") == "return-inflight" })
			if filed {
				if sent == nil || attrValue(sent, "role") != "status" || nodeText(sent) != i18n.T(ctx, i18n.KeyReturnRequestSent) || inflight != nil || strings.Contains(nodeText(doc), i18n.T(ctx, i18n.KeyReturnInFlight)) {
					t.Errorf("%s filed return lacks its sole request-sent status", locale.Tag())
				}
			} else if sent != nil || inflight == nil || strings.Contains(nodeText(doc), i18n.T(ctx, i18n.KeyReturnRequestSent)) {
				t.Errorf("%s ordinary open request lost its in-flight notice or claims a new send", locale.Tag())
			}
		}
	}
}
