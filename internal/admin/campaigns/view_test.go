package campaigns

import (
	"bytes"
	stdhtml "html"
	"slices"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/keyset"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/widget"
)

// The tests in this file are the campaign halves of tests that render several
// desks' pages: each half lives with the page it renders.

// From TestEveryAdminEmptyPageKeepsItsRestartLink.
func TestCampaignsEmptyPageKeepsItsRestartLink(t *testing.T) {
	t.Parallel()
	b := keyset.Bound{PastEnd: true, First: "/admin/products?q=test"}
	p := frame.Page{Title: "Pagination"}
	cases := map[string]templ.Component{
		"campaigns": Campaigns(p, CampaignsView{Bound: b}),
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			if err := c.Render(i18n.WithLocale(t.Context(), i18n.En), &out); err != nil {
				t.Fatal(err)
			}
			body := out.String()
			if !strings.Contains(body, `href="/admin/products?q=test"`) || !strings.Contains(body, "First page") {
				t.Fatal("empty page lost its plain restart link")
			}
			if strings.Count(body, "There are no entries on this page.") != 1 {
				t.Fatal("empty page lost its shared empty state")
			}
		})
	}
}

// From TestEveryQueueKeepsItsOwnFirstPageEmptyState.
func TestCampaignsQueueKeepsItsOwnFirstPageEmptyState(t *testing.T) {
	t.Parallel()
	ctx := func(k i18n.Key) string { return i18n.T(i18n.WithLocale(t.Context(), i18n.En), k) }
	generic := ctx(i18n.KeyPageEmpty)
	_, b := keyset.PageBound("/admin/queue", false, []db.Order{}, keyset.PageSize, func(*db.Order) string { return "" })
	p := frame.Page{Title: "Queue"}
	for name, c := range map[string]struct {
		page templ.Component
		want string
	}{
		"campaigns": {Campaigns(p, CampaignsView{Bound: b}), ctx(i18n.KeyAdminCampEmpty)},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			body := render(t, c.page)
			if !strings.Contains(body, stdhtml.EscapeString(c.want)) {
				t.Errorf("the queue's own empty text %q is missing", c.want)
			}
			if strings.Contains(body, generic) {
				t.Error("an empty first page shows the later-page sentence")
			}
		})
	}
}

// From TestEveryQueueWithMoreRowsOffersNext.
func TestCampaignsQueueWithMoreRowsOffersNext(t *testing.T) {
	t.Parallel()
	ctx := func(k i18n.Key) string { return i18n.T(i18n.WithLocale(t.Context(), i18n.En), k) }
	rows := make([]db.Order, keyset.PageLimit)
	_, b := keyset.PageBound("/admin/queue", false, rows, keyset.PageSize,
		func(*db.Order) string { return `{"ID":"12345678-1234-1234-1234-123456789abc"}` })
	p := frame.Page{Title: "Queue"}
	for name, page := range map[string]templ.Component{
		"campaigns": Campaigns(p, CampaignsView{Bound: b}),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			body := render(t, page)
			if !strings.Contains(body, `rel="next"`) {
				t.Error("a first page with more rows has no Next link")
			}
			if strings.Contains(body, ctx(i18n.KeyPageEmpty)) {
				t.Error("a first page with more rows says it is empty")
			}
		})
	}
}

// From the examples test the admin screens once shared.
func TestCampaignExamplesStayOutsideRefusedInputs(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		for _, draft := range []struct{ value, refusal string }{{"", ""}, {"Submitted <& value", ""}, {"", "Refused"}, {"Submitted <& value", "Refused"}} {
			typed, refusal := draft.value, draft.refusal
			t.Run(locale.Tag()+"/"+typed+"/"+refusal, func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				errors := func(fields ...string) map[string]string {
					if refusal == "" {
						return nil
					}
					result := make(map[string]string, len(fields))
					for _, field := range fields {
						result[field] = refusal
					}
					return result
				}
				prefix := "Example: "
				title := "Summer sale"
				if locale == i18n.ZhHant {
					prefix = "例："
					title = "夏季特賣"
				}
				p := frame.Page{}
				for _, page := range []struct {
					name      string
					component templ.Component
					fields    map[string]string
				}{
					{"campaign", Campaigns(p, CampaignsView{Draft: CampaignDraft{Title: typed, Slug: typed}, Errors: errors("title", "slug")}), map[string]string{"k-title": prefix + title, "k-slug": prefix + "summer-sale"}},
				} {
					t.Run(page.name, func(t *testing.T) {
						markup := renderComponent(t, ctx, page.component)
						z := html.NewTokenizer(strings.NewReader(markup))
						for z.Next() != html.ErrorToken {
							token := z.Token()
							if token.Data != "input" {
								continue
							}
							var descriptions int
							for _, attribute := range token.Attr {
								if attribute.Key == "aria-describedby" {
									descriptions++
								}
							}
							if descriptions > 1 {
								t.Error("an input repeats aria-describedby")
							}
						}
						doc, err := html.Parse(strings.NewReader(markup))
						if err != nil {
							t.Fatal(err)
						}
						for id, example := range page.fields {
							input := exampleElementByID(doc, id)
							if input == nil || input.Data != "input" {
								t.Fatalf("refused %s has no input %s", page.name, id)
							}
							if attr(input, "placeholder") != "" || attr(input, "value") != typed {
								t.Errorf("%s placeholder/value = %q/%q, want empty/%q", id, attr(input, "placeholder"), attr(input, "value"), typed)
							}
							var linkedExample bool
							for _, description := range strings.Fields(attr(input, "aria-describedby")) {
								hint := exampleElementByID(doc, description)
								if hint == nil {
									t.Errorf("%s description %s has no text", id, description)
									continue
								}
								_, hidden := attrPresent(hint, "hidden")
								if textOf(hint) == example && !hidden && !strings.Contains(attr(hint, "class"), "sr-only") {
									linkedExample = true
								}
							}
							if !linkedExample {
								t.Errorf("refused %s has no visible linked example %q", id, example)
							}
						}
					})
				}
			})
		}
	}
}

func exampleElementByID(n *html.Node, id string) *html.Node {
	if n.Type == html.ElementNode && attr(n, "id") == id {
		return n
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if found := exampleElementByID(child, id); found != nil {
			return found
		}
	}
	return nil
}

// From TestImageRefusalRendersDraftsApartFromSavedImages.
func TestCampaignImageRefusalRendersDraftsApartFromSavedImages(t *testing.T) {
	t.Parallel()
	const alt = ` 新照片「正面」 & 側面 `
	const altEn = ` New photograph "front" & side `
	const saved = "abababababababababababababababababababababababababababababababab"
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(t.Context(), locale)
		for _, tt := range []struct {
			name      string
			component templ.Component
			want      map[string]string
			errorID   string
		}{
			{name: "campaign", component: CampaignForm(frame.Page{}, CampaignView{Slug: "c", Image: widget.HeaderImage{Image: assets.Image{Digest: saved}, Alt: "Saved description"}, ImageAltDraft: alt, ImageAltEnDraft: altEn, Errors: map[string]string{"image": "Replace the image"}}), want: map[string]string{"c-alt": alt, "c-alt-en": altEn}, errorID: "c-image"},
		} {
			t.Run(locale.Tag()+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				body := renderComponent(t, ctx, tt.component)
				assertImageDraftControls(t, body, tt.want, tt.errorID)
				if !strings.Contains(body, `alt="Saved description"`) {
					t.Error("refusal replaced the saved image description")
				}
			})
		}
	}
}

func assertImageDraftControls(t *testing.T, body string, want map[string]string, errorID string) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	marked := false
	errorShown := false
	for n := range doc.Descendants() {
		if n.Type != html.ElementNode {
			continue
		}
		attrs := map[string]string{}
		for _, a := range n.Attr {
			attrs[a.Key] = a.Val
		}
		id := attrs["id"]
		if _, ok := want[id]; ok {
			got[id] = imageDraftControlValue(n, attrs)
		}
		if attrs["aria-invalid"] == "true" && id != errorID {
			t.Errorf("unrefused control %q inherited an error", id)
		}
		if id == errorID {
			marked = attrs["aria-invalid"] == "true" && slices.Contains(strings.Fields(attrs["aria-describedby"]), errorID+"-error")
			if n.Data == "input" && attrs["type"] == "file" && attrs["value"] != "" {
				t.Error("file input was repopulated")
			}
		}
		if id == errorID+"-error" && n.FirstChild != nil && n.FirstChild.Data != "" {
			errorShown = true
		}
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("image refusal controls (-want +got):\n%s", diff)
	}
	if !marked || !errorShown {
		t.Errorf("image refusal %q marked/error = %t/%t, want true/true", errorID, marked, errorShown)
	}
}

func imageDraftControlValue(n *html.Node, attrs map[string]string) string {
	switch n.Data {
	case "textarea":
		if n.FirstChild != nil {
			return n.FirstChild.Data
		}
	case "select":
		var value string
		for option := range n.Descendants() {
			optionAttrs := make(map[string]string, len(option.Attr))
			for _, a := range option.Attr {
				optionAttrs[a.Key] = a.Val
			}
			if _, selected := optionAttrs["selected"]; selected {
				value = optionAttrs["value"]
			}
		}
		return value
	}
	return attrs["value"]
}

// From TestRemoveButtonsNameTheirRecord.
func TestCampaignRemoveButtonsNameTheirRecord(t *testing.T) {
	t.Parallel()
	for _, locale := range []struct {
		locale i18n.Locale
		remove string
		image  string
	}{
		{locale: i18n.ZhHant, remove: "移除", image: "活動頁首圖片"},
		{locale: i18n.En, remove: "Remove", image: "Campaign header image"},
	} {
		for _, tt := range []struct {
			name      string
			component templ.Component
			action    string
			field     string
			want      map[string]string
		}{
			{name: "featured products", component: CampaignForm(frame.Page{}, CampaignView{Slug: "event", Products: []CampaignProduct{{Slug: "pixelight", Name: "Pixelight <Pro> & more"}, {Slug: "meridian", Name: "Meridian"}}}), action: "/admin/campaigns/event/products", field: "product", want: map[string]string{"pixelight": "Pixelight <Pro> & more", "meridian": "Meridian"}},
			{name: "campaign image", component: CampaignForm(frame.Page{}, CampaignView{Slug: "event", Image: widget.HeaderImage{Image: assets.Image{Digest: "abababababababababababababababababababababababababababababababab"}, Alt: "Summer <sale>"}}), action: "/admin/campaigns/event/image/remove", want: map[string]string{"": locale.image + " Summer <sale>"}},
		} {
			t.Run(locale.locale.Tag()+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale.locale)
				doc, err := html.Parse(strings.NewReader(renderComponent(t, ctx, tt.component)))
				if err != nil {
					t.Fatal(err)
				}
				got := map[string]string{}
				for form := range doc.Descendants() {
					if form.Type != html.ElementNode || form.Data != "form" || attr(form, "action") != tt.action {
						continue
					}
					if attr(form, "method") != "post" {
						t.Errorf("removal form method = %q, want post", attr(form, "method"))
					}
					var record string
					var button *html.Node
					for n := range form.Descendants() {
						if n.Data == "input" && attr(n, "name") == tt.field {
							record = attr(n, "value")
						}
						if n.Data == "button" && attr(n, "type") == "submit" {
							button = n
						}
					}
					if button == nil {
						t.Fatal("removal form has no submit button")
					}
					var visible, hidden strings.Builder
					seenRecord := false
					for n := button.FirstChild; n != nil; n = n.NextSibling {
						if n.Type == html.TextNode {
							if seenRecord && strings.TrimSpace(n.Data) != "" {
								t.Errorf("record %q precedes the visible action in the accessible name", record)
							}
							visible.WriteString(n.Data)
							continue
						}
						if n.Data != "span" || attr(n, "class") != "goen-sr-only" {
							t.Errorf("record %q uses a visible or unexpected label element", record)
							continue
						}
						if _, present := attrPresent(n, "hidden"); present || attr(n, "aria-hidden") == "true" {
							t.Errorf("record %q is hidden from assistive technology", record)
						}
						seenRecord = true
						for text := range n.Descendants() {
							if text.Type == html.TextNode {
								hidden.WriteString(text.Data)
							}
						}
					}
					if label := strings.TrimSpace(visible.String()); label != locale.remove {
						t.Errorf("record %q visible label = %q, want %q", record, label, locale.remove)
					}
					if attr(button, "aria-label") != "" || attr(button, "aria-labelledby") != "" {
						t.Errorf("record %q overrides its visible label and record text", record)
					}
					got[record] = strings.TrimSpace(hidden.String())
				}
				if diff := cmp.Diff(tt.want, got); diff != "" {
					t.Errorf("removal button records (-want +got):\n%s", diff)
				}
			})
		}
	}
}
