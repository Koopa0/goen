package admin

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
)

func TestProductRefusalsRenderDetailErrorsAndTheSpecificationDraft(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			view := ProductView{
				Slug: "editor-test", Summary: strings.Repeat("s", 501), Description: strings.Repeat("d", 20001),
				SpecDraft: SpecDraft{Label: "Capacity & size", Value: " 350 mL ", LabelEn: "Capacity <ml>", ValueEn: "350 <mL> & more"},
				Errors: map[string]string{
					"summary":     i18n.T(ctx, i18n.KeyFormProductSummaryLong),
					"description": i18n.T(ctx, i18n.KeyFormProductDescriptionLong),
					"spec_label":  i18n.T(ctx, i18n.KeyFormSpecLabelDuplicate),
				},
			}
			var out strings.Builder
			if err := productDetails(view).Render(ctx, &out); err != nil {
				t.Fatal(err)
			}
			if err := productSpecs(view).Render(ctx, &out); err != nil {
				t.Fatal(err)
			}
			doc, err := html.Parse(strings.NewReader(out.String()))
			if err != nil {
				t.Fatal(err)
			}
			fields := map[string]map[string]string{}
			paragraphs := map[string]string{}
			for n := range doc.Descendants() {
				if n.Type != html.ElementNode {
					continue
				}
				attrs := map[string]string{}
				for _, a := range n.Attr {
					attrs[a.Key] = a.Val
				}
				if n.Data == "input" || n.Data == "textarea" {
					if n.Data == "textarea" && n.FirstChild != nil {
						attrs["value"] = n.FirstChild.Data
					}
					fields[attrs["id"]] = attrs
				}
				if n.Data == "p" && n.FirstChild != nil {
					paragraphs[attrs["id"]] = n.FirstChild.Data
				}
			}
			for _, tt := range []struct{ id, field, value string }{
				{id: "p-summary", field: "summary", value: view.Summary},
				{id: "p-description", field: "description", value: view.Description},
				{id: "spec-label", field: "spec_label", value: "Capacity & size"},
			} {
				want := []string{tt.value, "true", tt.id + "-error", view.Errors[tt.field]}
				got := []string{fields[tt.id]["value"], fields[tt.id]["aria-invalid"], fields[tt.id]["aria-describedby"], paragraphs[tt.id+"-error"]}
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("refused control %q (-want +got):\n%s", tt.id, diff)
				}
			}
			for id, want := range map[string]string{"spec-value": " 350 mL ", "spec-label-en": "Capacity <ml>", "spec-value-en": "350 <mL> & more"} {
				got := []string{fields[id]["value"], fields[id]["aria-invalid"]}
				if diff := cmp.Diff([]string{want, ""}, got); diff != "" {
					t.Errorf("unrefused specification control %q (-want +got):\n%s", id, diff)
				}
			}
		})
	}
}
