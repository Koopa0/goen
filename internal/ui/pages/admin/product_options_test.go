package admin

import (
	"context"
	"html"
	"io"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestOptionFormIsReplacedByTheNoteOnceAProductHasVariants(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		note := i18n.T(ctx, i18n.KeyFormOptionBeforeVariants)
		for _, tt := range []struct {
			name     string
			variants []ProductVariant
			wantForm bool
		}{{"no variants", nil, true}, {"one variant", []ProductVariant{{SKU: "A"}}, false}} {
			v := ProductView{Slug: "p", Variants: tt.variants}
			var b strings.Builder
			if err := productOptions(v).Render(ctx, &b); err != nil {
				t.Fatal(err)
			}
			page := b.String()
			if got := strings.Contains(page, `name="name_en"`) && strings.Contains(page, `id="opt-name"`); got != tt.wantForm {
				t.Errorf("%s %s: add-option form present = %v, want %v", locale, tt.name, got, tt.wantForm)
			}
			if got := strings.Contains(page, "opt-frozen"); got == tt.wantForm {
				t.Errorf("%s %s: note present = %v", locale, tt.name, got)
			}
			if !tt.wantForm && !strings.Contains(page, html.EscapeString(note)) {
				t.Errorf("%s: note text missing", locale)
			}
		}
		if strings.Contains(note, "規格軸") {
			t.Error("staff copy must say 規格項目, not 規格軸")
		}
	}
}

// A refused option or variant form comes back holding what was submitted. The
// selects matter most: they default to the first choice, so a re-render that
// forgets them files the corrected resubmit under the first axis or on the
// first combination.
func TestARefusedOptionOrVariantFormKeepsTheChoicesMade(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := ProductView{
		Slug: "p",
		Options: []Option{
			{ID: "axis-colour", Name: "顏色", Values: []OptionValue{
				{ID: "val-white", Value: "白"}, {ID: "val-black", Value: "黑"},
			}},
			{ID: "axis-edition", Name: "版本", Values: []OptionValue{
				{ID: "val-std", Value: "標準版"}, {ID: "val-pro", Value: "專業版"},
			}},
		},
		VariantDraft: VariantDraft{
			OptionValueIDs: []string{"val-black", "val-std"},
			OptionName:     "材質", OptionNameEn: "Material",
			ValueOption: "axis-edition", Value: "標準版", ValueEn: "Standard", Swatch: "#12",
		},
	}
	render := func(c interface {
		Render(context.Context, io.Writer) error
	},
	) string {
		var b strings.Builder
		if err := c.Render(ctx, &b); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	options := render(productOptions(view))
	variants := render(productVariants(view))

	for _, want := range []string{
		`<option value="axis-edition" selected>`,
		`value="標準版"`, `value="Standard"`, `value="#12"`,
	} {
		if !strings.Contains(options, want) {
			t.Errorf("option forms lost %s", want)
		}
	}
	if strings.Contains(options, `<option value="axis-colour" selected>`) {
		t.Error("the axis select came back on the first axis")
	}
	for _, want := range []string{`<option value="val-black" selected>`, `<option value="val-std" selected>`} {
		if !strings.Contains(variants, want) {
			t.Errorf("variant selects lost %s", want)
		}
	}
	for _, unwanted := range []string{`<option value="val-white" selected>`, `<option value="val-pro" selected>`} {
		if strings.Contains(variants, unwanted) {
			t.Errorf("variant selects chose %s", unwanted)
		}
	}
}

func TestTheProductPageJumpsToItsSectionsAndKeepsItsStatusMovesAtTheTop(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	var b strings.Builder
	view := ProductView{Slug: "p", Status: "active", Variants: []ProductVariant{{SKU: "A"}}}
	if err := ProductForm(layouts.Page{Title: "p"}, view).Render(ctx, &b); err != nil {
		t.Fatal(err)
	}
	page := b.String()

	_, nav, found := strings.Cut(page, `class="goen-admin__sectionnav"`)
	if !found {
		t.Fatal("the product page has no section navigation")
	}
	nav, _, _ = strings.Cut(nav, "</nav>")
	links := regexp.MustCompile(`href="#([a-z-]+)"`).FindAllStringSubmatch(nav, -1)
	want := []string{"sec-details", "sec-label", "sec-invoice", "sec-images", "sec-options", "sec-variants", "sec-specs", "sec-standing"}
	got := make([]string, 0, len(links))
	for _, link := range links {
		got = append(got, link[1])
	}
	if !slices.Equal(got, want) {
		t.Errorf("section anchors = %v, want %v", got, want)
	}
	for _, m := range links {
		if !strings.Contains(page, `id="`+m[1]+`"`) {
			t.Errorf("the link to #%s lands nowhere", m[1])
		}
	}

	action := `action="/admin/products/p/status"`
	if strings.Count(page, action) != 1 {
		t.Fatalf("the status form appears %d times, want once", strings.Count(page, action))
	}
	if strings.Index(page, action) > strings.Index(page, `id="sec-details"`) {
		t.Error("the status moves sit below the first section, not in the top action area")
	}
	for _, want := range []string{`name="status" value="draft"`, `name="status" value="archived"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the top action area lacks %q", want)
		}
	}
}
