package pages

import (
	"regexp"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

var inputTag = regexp.MustCompile(`<input[^>]*>`)

// The reviewer approves what the page shows, so the checks read the text with
// every <input> removed: a value that only rides in a hidden field is not shown.
func TestCreditConfirmationShowsWhatWillBeGranted(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		v := AdminCreditView{
			Confirm: true, CustomerID: "11111111-2222-3333-4444-555555555555", CustomerName: "Ada Wong", Email: "ada@example.test",
			BalanceCents: 34000, GrantCents: 12500, Amount: "125", Reason: "goodwill for a late parcel", OperationID: "op-1",
		}
		var body strings.Builder
		if err := AdminCredit(layouts.Page{Title: "credit"}, v).Render(ctx, &body); err != nil {
			t.Fatal(err)
		}
		visible := inputTag.ReplaceAllString(body.String(), "")
		for name, want := range map[string]string{
			"grant amount": "NT$125", "current balance": "NT$340", "customer name": "Ada Wong",
			"customer email": "ada@example.test", "reason": "goodwill for a late parcel",
		} {
			if !strings.Contains(visible, want) {
				t.Errorf("%s: confirmation does not show %s %q", locale, name, want)
			}
		}
	}
}
