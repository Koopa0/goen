package pages

import (
	"regexp"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

// quantityField matches the buy form's number input, whatever is drawn around it.
var quantityField = regexp.MustCompile(`<input[^>]*\bid="quantity"[^>]*>`)

// TestTheQuantityFieldSurvivesItsStepper holds the half of the stepper that is
// not decoration.
//
// The two buttons beside the field are an enhancement: they carry type="button",
// they are hidden until a stylesheet is told scripting is on, and a browser
// without either still has to be able to post a quantity. That only works while
// the field itself keeps the name the handler reads and the bounds the browser
// refuses out-of-range values against — which a control that wraps it is exactly
// the kind of change to lose.
func TestTheQuantityFieldSurvivesItsStepper(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	v := &ProductView{
		Name: "Nimbus Buds Pro", Brand: "Nimbus", Slug: "nimbus-buds-pro",
		SelectionOK: true, Exact: true, Sellable: true, AnySellable: true,
		Available: 7, PriceCents: 590000,
	}

	field := quantityField.FindString(renderProductInLocale(t, ctx, v))
	if field == "" {
		t.Fatal("the buy form rendered no quantity field")
	}

	for _, want := range []string{
		`name="quantity"`,
		`type="number"`,
		`min="1"`,
		`max="7"`,
		`inputmode="numeric"`,
	} {
		if !regexp.MustCompile(regexp.QuoteMeta(want)).MatchString(field) {
			t.Errorf("the quantity field is missing %s\ngot: %s", want, field)
		}
	}
}

// TestTheStepperButtonsNeverSubmit holds the other half. A button inside a form
// submits it unless it says otherwise, so a stepper built without type="button"
// posts the cart line every time somebody adjusts the count.
func TestTheStepperButtonsNeverSubmit(t *testing.T) {
	t.Parallel()

	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	v := &ProductView{
		Name: "Nimbus Buds Pro", Brand: "Nimbus", Slug: "nimbus-buds-pro",
		SelectionOK: true, Exact: true, Sellable: true, AnySellable: true,
		Available: 7, PriceCents: 590000,
	}

	html := renderProductInLocale(t, ctx, v)
	steps := regexp.MustCompile(`<button[^>]*data-stepper-step[^>]*>`).FindAllString(html, -1)
	if len(steps) != 2 {
		t.Fatalf("found %d stepper buttons, want 2", len(steps))
	}
	for _, step := range steps {
		if !regexp.MustCompile(`type="button"`).MatchString(step) {
			t.Errorf("a stepper button would submit the form: %s", step)
		}
		if !regexp.MustCompile(`aria-label="[^"]+"`).MatchString(step) {
			t.Errorf("a stepper button has no name a screen reader can read: %s", step)
		}
	}
}
