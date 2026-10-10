package admin

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// TestARefusedInspectionKeepsWhatWasTyped holds that a refusal re-renders the
// counts and the note rather than the form's defaults, and marks the counts.
func TestARefusedInspectionKeepsWhatWasTyped(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := ReturnsView{
		Rows: []Return{{
			ID: "r1", OrderNumber: "GO-260930-000012", Status: "approved", Window: "within", Decided: true,
			Lines: []ReturnLine{{
				OrderLineID: "l1", Name: "耳機", Quantity: 1, Restockable: true,
				DraftReceived: "1", DraftRestocked: "3", DraftNote: "外盒破損",
			}},
		}},
		Errors: map[string]string{"r1.inspect": "數量填寫有問題"},
	}
	html := renderComponent(t, ctx, Returns(layouts.Page{}, view))

	for _, want := range []string{
		`name="restocked_l1" value="3"`, `value="外盒破損"`, `aria-invalid="true"`,
		`id="inspect-error-r1"`, `aria-describedby="stock-r1-l1-error"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the refused inspection form is missing %q", want)
		}
	}
}

// TestARefusedAnswerKeepsItsText holds that the reply staff typed stays in its
// box, marked invalid, instead of vanishing with the redirect.
func TestARefusedAnswerKeepsItsText(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := QuestionsView{Rows: []Question{{
		ID: "q1", Body: "多久到貨?", Draft: "一長串回覆", Error: "回覆不能留白",
	}}}
	html := renderComponent(t, ctx, Questions(layouts.Page{}, view))

	for _, want := range []string{`>一長串回覆</textarea>`, `aria-invalid="true"`, `id="answer-error-q1"`} {
		if !strings.Contains(html, want) {
			t.Errorf("the refused answer form is missing %q", want)
		}
	}
}
