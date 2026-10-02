package pages

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/ui/layouts"
)

// A refused address re-renders with what was typed, the disclosure open and
// the refused control marked.
func TestARefusedAddressKeepsItsFields(t *testing.T) {
	t.Parallel()
	html := renderToString(t, Account(layouts.Page{Title: "account"}, &AccountView{
		Email: "a@example.com",
		AddressDraft: &AddressDraft{
			Label: "家", Name: "王小明", Phone: "0912345678", PostalCode: "ABC",
			City: "台北市", District: "信義區", Street: "松高路 1 號", Default: true,
		},
		AddressErrors: map[string]string{"postal_code": "郵遞區號格式不正確"},
	}))
	for _, want := range []string{
		`value="ABC"`, `value="王小明"`, `value="松高路 1 號"`, `id="addr-postal-error"`,
		"郵遞區號格式不正確", `aria-invalid="true"`, `<details class="goen-account__disclosure" open`,
		`name="default" value="1" checked`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the refused address form is missing %q", want)
		}
	}
	if blank := renderToString(t, Account(layouts.Page{Title: "account"}, &AccountView{Email: "a@example.com"})); strings.Contains(blank, "aria-invalid") ||
		strings.Contains(blank, `disclosure" open`) {
		t.Error("a plain visit shows a refused address form")
	}
}
