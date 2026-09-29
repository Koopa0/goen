package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestReturnConfirmationOffersOnlyTheSelectedDecision(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, decision := range []string{"approved", "rejected", "exception"} {
			ctx := i18n.WithLocale(t.Context(), locale)
			v := AdminReturnConfirmation{ID: "return-id", OrderNumber: "order-number", Decision: decision, Reason: "customer reason", Resolution: "staff explanation", AmountCents: 12500, Required: decision != "approved"}
			var body strings.Builder
			if err := ConfirmReturn(layouts.Page{Title: v.Title(ctx)}, v).Render(ctx, &body); err != nil {
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
