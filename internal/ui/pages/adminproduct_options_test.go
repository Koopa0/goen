package pages

import (
	"context"
	"html"
	"io"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestOptionFormIsReplacedByTheNoteOnceAProductHasVariants(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		note := i18n.T(ctx, i18n.KeyFormOptionBeforeVariants)
		for _, tt := range []struct {
			name     string
			variants []AdminProductVariant
			wantForm bool
		}{{"no variants", nil, true}, {"one variant", []AdminProductVariant{{SKU: "A"}}, false}} {
			v := AdminProductView{Slug: "p", Variants: tt.variants}
			var b strings.Builder
			if err := adminProductOptions(v).Render(ctx, &b); err != nil {
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
	view := AdminProductView{
		Slug: "p",
		Options: []AdminOption{
			{ID: "axis-colour", Name: "顏色", Values: []AdminOptionValue{
				{ID: "val-white", Value: "白"}, {ID: "val-black", Value: "黑"},
			}},
			{ID: "axis-edition", Name: "版本", Values: []AdminOptionValue{
				{ID: "val-std", Value: "標準版"}, {ID: "val-pro", Value: "專業版"},
			}},
		},
		VariantDraft: AdminVariantDraft{
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
	options := render(adminProductOptions(view))
	variants := render(adminProductVariants(view))

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
