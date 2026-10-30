package returns

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestConfirmReturnSelectedDecision: the return confirmation offers only the
// selected decision.
func TestConfirmReturnSelectedDecision(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, decision := range []string{"approved", "rejected", "exception"} {
			ctx := i18n.WithLocale(t.Context(), locale)
			v := Confirmation{ID: "return-id", OrderNumber: "order-number", Decision: decision, Reason: "customer reason", Resolution: "staff explanation", AmountCents: 12500, Required: decision != "approved"}
			var body strings.Builder
			if err := ConfirmReturn(frame.Page{Title: v.Title(ctx)}, v).Render(ctx, &body); err != nil {
				t.Fatal(err)
			}
			html := body.String()
			if strings.Count(html, `name="confirm"`) != 1 || !strings.Contains(html, v.Title(ctx)) || !strings.Contains(html, "order-number") || !strings.Contains(html, "NT$125") || !strings.Contains(html, `method="post"`) {
				t.Fatalf("incomplete single-decision confirmation: %s", html)
			}
			if strings.Contains(html, `required`) != v.Required {
				t.Fatalf("%s required reason control disagrees with decision", decision)
			}
			for _, other := range []string{"approved", "rejected", "exception"} {
				if other != decision && strings.Contains(html, `value="`+other+`"`) {
					t.Fatalf("%s confirmation also submits %s", decision, other)
				}
			}
		}
	}
}

// TestConfirmReturnRequiredMark: the return decision marks its note only when
// the decision requires one.
//
// From orderflow_test.go, moved whole.
// The return decision marks its note only when the decision needs one.
func TestConfirmReturnRequiredMark(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for _, required := range []bool{true, false} {
		html := renderComponent(t, ctx, ConfirmReturn(frame.Page{Title: "r"},
			Confirmation{ID: "id", OrderNumber: "GO-1", Decision: "rejected", Required: required}))
		if got := strings.Contains(html, i18n.T(ctx, i18n.KeyAdminRequiredMark)); got != required {
			t.Errorf("required=%t but the mark is shown=%t", required, got)
		}
	}
}

// TestConfirmationBack: a decision asked from a return's own page goes back to
// that page, from both the link and the final post.
func TestConfirmationBack(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for _, own := range []bool{true, false} {
		v := Confirmation{ID: "return-id", OrderNumber: "GO-1", Decision: "approved", OwnPage: own}
		wantBack := "/admin/returns"
		if own {
			wantBack = "/admin/returns?request=return-id"
		}
		if got := v.Back(); got != wantBack {
			t.Errorf("Confirmation{OwnPage: %t}.Back() = %q, want %q", own, got, wantBack)
		}
		html := renderComponent(t, ctx, ConfirmReturn(frame.Page{}, v))
		if got := strings.Contains(html, `href="`+wantBack+`"`); !got {
			t.Errorf("OwnPage=%t: the page does not link back to %q", own, wantBack)
		}
		if got := strings.Contains(html, `<input type="hidden" name="from" value="return">`); got != own {
			t.Errorf("OwnPage=%t: the final post carries the flag = %t", own, got)
		}
	}
}
