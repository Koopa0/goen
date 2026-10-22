package shipping

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shipping"
	"github.com/koopa0/goen/internal/ui/frame"
)

// The tests below render the shipping page next to nothing else; the helpers they use are copies, since a
// test cannot import another package's.

func TestAMethodWithoutEnglishSaysSoAndMarksTheOtherName(t *testing.T) {
	t.Parallel()
	view := ShippingView{Methods: []Method{
		{MethodID: "a", VersionID: "va", Destination: shipping.DestinationAddress, Name: "宅配到府", NameEn: "Home delivery"},
		{MethodID: "b", VersionID: "vb", Destination: shipping.DestinationAddress, Name: "超商取貨"},
	}}
	for _, tt := range []struct {
		locale    i18n.Locale
		otherLang string
		pills     int
	}{{i18n.En, `lang="zh-Hant">宅配到府<`, 1}, {i18n.ZhHant, `lang="en">Home delivery<`, 1}} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			var out bytes.Buffer
			if err := Shipping(frame.Page{Title: "Shipping"}, view).Render(ctx, &out); err != nil {
				t.Fatal(err)
			}
			heads := strings.Split(out.String(), `<h2 class="goen-admin__heading">`)
			pill := i18n.T(ctx, i18n.KeyAdminUntranslated)
			got := 0
			for _, h := range heads[1:3] {
				head, _, _ := strings.Cut(h, "</h2>")
				got += strings.Count(head, pill)
			}
			if got != tt.pills {
				t.Errorf("%d %q pills in the method headings, want %d", got, pill, tt.pills)
			}
			if !strings.Contains(out.String(), `class="goen-admin__othername" `+tt.otherLang) {
				t.Errorf("other-language name is not marked %s", tt.otherLang)
			}
		})
	}
}

func TestShippingZonesKeepTableRelationshipsWhenRowsBecomeCards(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(t.Context(), locale)
		view := ShippingView{Zones: []Zone{
			{ID: "mainland", Code: "mainland", Name: "本島", NameEn: "Mainland"},
			{ID: "offshore", Code: "offshore", Name: "離島", NameEn: "Offshore"},
		}}
		doc := reviewFollowupDocument(t, renderComponent(t, ctx, Shipping(frame.Page{Title: "Shipping"}, view)))
		var table, heading *html.Node
		for node := range doc.Descendants() {
			if node.Data == "table" && strings.Contains(attr(node, "class"), "goen-admin__zones") {
				table = node
			}
			if attr(node, "id") == "shipping-zones-heading" {
				heading = node
			}
		}
		if table == nil {
			t.Fatal("shipping zones table is missing")
		}
		if attr(table, "role") != "table" {
			t.Errorf("%s: zone table role = %q, want table", locale, attr(table, "role"))
		}
		if heading == nil || attr(table, "aria-labelledby") != "shipping-zones-heading" || textOf(heading) != i18n.T(ctx, i18n.KeyAdminShipZones) {
			t.Errorf("%s: the table is not named by its visible heading", locale)
		}
		counts := make(map[string]int)
		roles := map[string]string{"thead": "rowgroup", "tbody": "rowgroup", "tr": "row", "th": "columnheader", "td": "cell"}
		for node := range table.Descendants() {
			if role, exists := roles[node.Data]; node.Type == html.ElementNode && exists {
				counts[node.Data]++
				if attr(node, "role") != role {
					t.Errorf("%s: %s role = %q, want %q", locale, node.Data, attr(node, "role"), role)
				}
				if node.Data == "th" && attr(node, "scope") != "col" {
					t.Errorf("%s: a column header lost its scope", locale)
				}
			}
		}
		if diff := cmp.Diff(map[string]int{"thead": 1, "tbody": 1, "tr": 3, "th": 3, "td": 6}, counts); diff != "" {
			t.Errorf("%s: table relationships (-want +got):\n%s", locale, diff)
		}
	}
}

func TestZonePrefixRowsFollowLines(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ raw, want string }{
		{"209 210 211 212 880 881 882 883 884 885", "3"},
		{"", "3"},
		{"209\n210\n211\n212", "5"},
	} {
		if got := zonePrefixRows(tc.raw); got != tc.want {
			t.Errorf("zonePrefixRows(%q) = %s, want %s", tc.raw, got, tc.want)
		}
	}
}

func TestShippingExampleIsItsOwnHintLine(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	pages := map[string]string{
		"shipping": renderComponent(t, ctx, Shipping(frame.Page{Title: "Shipping"}, ShippingView{})),
	}
	for name, body := range pages {
		if strings.Contains(body, "。 ") {
			t.Errorf("%s page runs a sentence into the next on one line: %q", name, body[strings.Index(body, "。 ")-20:strings.Index(body, "。 ")+30])
		}
		if !strings.Contains(body, "例如 ") {
			t.Errorf("%s page has no example line", name)
		}
	}
}

func TestEveryShippingExampleHintReadsInFull(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale   i18n.Locale
		sentence func(example string) string
	}{
		{i18n.ZhHant, func(e string) string { return "<p class=\"goen-admin__hint\">例如 " + e + "。</p>" }},
		{i18n.En, func(e string) string { return "<p class=\"goen-admin__hint\">For example, " + e + ".</p>" }},
	} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			for name, c := range map[string]struct {
				body     string
				examples []string
			}{
				"shipping": {renderComponent(t, ctx, Shipping(frame.Page{Title: "Shipping"}, ShippingView{})), []string{"express_delivery", "mountain", "313 546 556"}},
			} {
				for _, e := range c.examples {
					if want := tt.sentence(e); !strings.Contains(c.body, want) {
						t.Errorf("%s page lacks the hint %s", name, want)
					}
				}
			}
		})
	}
}

func TestShippingRefusalsNameTheirFormAndLinkToTheFirstInvalidControl(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			bad := i18n.T(ctx, i18n.KeyAdminNoticeRefused)
			cases := []struct {
				name                  string
				component             templ.Component
				action, target, first string
				title                 i18n.Key
				count                 int
			}{
				{"method", Shipping(frame.Page{}, ShippingView{Errors: map[string]string{"code": bad, "fee": bad}}), "/admin/shipping/method#new-method", "new-method", "m-code", i18n.KeyAdminShipAddMethod, 2},
				{"zone", Shipping(frame.Page{}, ShippingView{Errors: map[string]string{"zone_code": bad, "prefixes": bad}}), "/admin/shipping/zone#new-zone", "new-zone", "z-code", i18n.KeyAdminShipAddZone, 2},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()
					body := renderComponent(t, ctx, tc.component)
					doc, err := html.Parse(strings.NewReader(body))
					if err != nil {
						t.Fatal(err)
					}
					var alert *html.Node
					var form, target, first *html.Node
					ids := map[string]*html.Node{}
					for n := range doc.Descendants() {
						if n.Type != html.ElementNode {
							continue
						}
						id := refusalAttr(n, "id")
						if id != "" {
							ids[id] = n
						}
						if n.Data == "form" && refusalAttr(n, "action") == tc.action {
							form = n
						}
						if id == tc.target {
							target = n
						}
						if id == tc.first {
							first = n
						}
						if refusalAttr(n, "role") == "alert" && strings.Contains(refusalText(n), i18n.T(ctx, tc.title)) {
							for link := range n.Descendants() {
								if link.Data == "a" && refusalAttr(link, "href") == "#"+tc.first {
									alert = n
								}
							}
						}
					}
					if form == nil || target == nil {
						t.Errorf("form must post to its section: %s", tc.action)
					}
					if alert == nil {
						t.Fatalf("page-top alert must name %q and link to #%s", i18n.T(ctx, tc.title), tc.first)
					}
					count := "2"
					if tc.count == 1 {
						count = "1"
					}
					if !strings.Contains(refusalText(alert), count) {
						t.Errorf("alert does not count %d invalid fields", tc.count)
					}
					for n := range doc.Descendants() {
						if n == alert {
							break
						}
						if n.Data == "form" && strings.Contains(refusalAttr(n, "class"), "goen-admin__") {
							t.Error("summary must precede the forms")
							break
						}
					}
					if first == nil || refusalAttr(first, "aria-invalid") != "true" {
						t.Errorf("first invalid control %s is not marked", tc.first)
					}
					for n := range doc.Descendants() {
						if n.Type != html.ElementNode || refusalAttr(n, "aria-invalid") != "true" {
							continue
						}
						describes := strings.Fields(refusalAttr(n, "aria-describedby"))
						if len(describes) == 0 {
							t.Errorf("invalid control %s has no description", refusalAttr(n, "id"))
						}
						for _, id := range describes {
							if ids[id] == nil || strings.TrimSpace(refusalText(ids[id])) == "" {
								t.Errorf("invalid control references missing error %s", id)
							}
						}
					}
				})
			}
		})
	}
}

// renderComponent renders c under ctx, which the caller owns so the locale stays explicit at the call site.
func renderComponent(t *testing.T, ctx context.Context, c templ.Component) string {
	t.Helper()
	var b strings.Builder
	if err := c.Render(ctx, &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}

func refusalAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func refusalText(n *html.Node) string {
	if n == nil {
		return ""
	}
	var out strings.Builder
	if n.Type == html.TextNode {
		out.WriteString(n.Data)
	}
	for child := range n.Descendants() {
		if child.Type == html.TextNode {
			out.WriteString(child.Data)
		}
	}
	return out.String()
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

func reviewFollowupDocument(t *testing.T, markup string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func textOf(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(textOf(c))
	}
	return strings.TrimSpace(b.String())
}
