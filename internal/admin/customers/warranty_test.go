package customers

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
)

func TestWarrantiesLinkTheirCustomer(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	v := WarrantiesView{Term: "SN-1", Searched: true, Rows: []WarrantyRow{
		{Serial: "SN-1", Order: "GO-1", CustomerID: "c1", CustomerName: "Ada"},
		{Serial: "SN-2", Order: "GO-2"},
	}}
	body := renderComponent(t, ctx, Warranties(Meta(ctx), v))
	for name, want := range map[string]string{
		"column named 顧客":           `<th scope="col">顧客</th>`,
		"linked customer":           `<a href="/admin/customers/c1">Ada</a>`,
		"erased account stays text": i18n.T(ctx, i18n.KeyAdminWarrantyErasedAccount),
	} {
		if !strings.Contains(body, want) {
			t.Errorf("warranty lookup lacks the %s %q", name, want)
		}
	}
	if n := strings.Count(body, `href="/admin/customers/`); n != 1 {
		t.Errorf("warranty lookup links %d customers, want 1", n)
	}
}

func TestWarrantiesShortSearchHint(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	body := renderComponent(t, ctx, Warranties(Meta(ctx), WarrantiesView{Term: "a"}))
	for _, want := range []string{
		`<p class="goen-admin__hint" id="warranty-search-hint">Please enter at least two characters.</p>`,
		`aria-describedby="warranty-search-hint"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("short warranty search lacks %q", want)
		}
	}
}
