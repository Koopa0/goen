package campaigns

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	nethtml "golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestCampaignSaveButtonsNameTheirPart: the editor saves its dates, tone and
// header image through separate forms, so a button that reads only "Save" lets
// a person who edits two parts press the wrong one and lose the other. The
// section that searches for products does not share its heading with the
// button on each result.
func TestCampaignSaveButtonsNameTheirPart(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale  i18n.Locale
		buttons map[string]string // form action to the label of its submit button
		heading string
		row     string
	}{
		{
			locale: i18n.ZhHant,
			buttons: map[string]string{
				"/admin/campaigns/event/window":   "儲存檔期",
				"/admin/campaigns/event/tone":     "儲存色調",
				"/admin/campaigns/event/image":    "儲存圖片",
				"/admin/campaigns/event/products": "加入活動",
			},
			heading: "加入商品",
			row:     "加入活動",
		},
		{
			locale: i18n.En,
			buttons: map[string]string{
				"/admin/campaigns/event/window":   "Save dates",
				"/admin/campaigns/event/tone":     "Save tone",
				"/admin/campaigns/event/image":    "Save image",
				"/admin/campaigns/event/products": "Add to the campaign",
			},
			heading: "Add products",
			row:     "Add to the campaign",
		},
	} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			v := CampaignView{Slug: "event", Term: "kettle", Matches: []Product{{Slug: "kettle", Name: "Kettle", HasDiscountedVariant: true}}}
			doc, err := nethtml.Parse(strings.NewReader(renderComponent(t, ctx, Campaign(frame.Page{}, v))))
			if err != nil {
				t.Fatal(err)
			}
			buttons := map[string]string{}
			var headings []string
			for n := range doc.Descendants() {
				if n.Type != nethtml.ElementNode {
					continue
				}
				switch n.Data {
				case "h2":
					headings = append(headings, textOf(n))
				case "form":
					// The frame's locale and sign-out forms and the page head's
					// active toggle post too; none is one of the parts.
					action := attr(n, "action")
					if attr(n, "method") != "post" || !strings.HasPrefix(action, "/admin/campaigns/event/") || action == v.ActiveAction() {
						continue
					}
					for b := range n.Descendants() {
						if b.Type == nethtml.ElementNode && b.Data == "button" && attr(b, "type") == "submit" {
							if _, dup := buttons[action]; dup {
								t.Errorf("form %s has more than one submit button", action)
							}
							buttons[action] = textOf(b)
						}
					}
				}
			}
			if diff := cmp.Diff(tt.buttons, buttons); diff != "" {
				t.Errorf("submit button labels by form (-want +got):\n%s", diff)
			}
			if !slices.Contains(headings, tt.heading) {
				t.Errorf("section headings = %q, want one to read %q", headings, tt.heading)
			}
			if slices.Contains(headings, tt.row) {
				t.Errorf("section headings = %q, want none to repeat the row button %q", headings, tt.row)
			}
		})
	}
}
