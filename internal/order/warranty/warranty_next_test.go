package warranty

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestWarrantyOrderNextUnit: the next registration requires its own delivered,
// unregistered unit: without the next unit's expiry the order shows one neutral
// state and no form, and with it the form, unit and own-parcel expiry.
func TestWarrantyOrderNextUnit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		locale  i18n.Locale
		neutral string
	}{
		{i18n.ZhHant, "目前沒有可登錄的下一件商品。"},
		{i18n.En, "No next item is available for registration at present."},
	} {
		t.Run(tc.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tc.locale)
			line := Line{ID: "next", Name: "Parcel item", HasTerm: true, Months: 12, Delivered: 1}
			if line.Registrable() {
				t.Error("a net-delivered count without the next unit's expiry is registrable")
			}
			body := renderWarrantyInLocale(t, ctx, WarrantyOrder(frame.Page{}, OrderView{Number: "GO-NEXT", Lines: []Line{line}}))
			if strings.Contains(body, `name="unit"`) || strings.Contains(body, `id="serial-next"`) {
				t.Error("unavailable next ordinal renders a registration form")
			}
			if strings.Count(body, tc.neutral) != 1 || strings.Contains(body, i18n.T(ctx, i18n.KeyWarrantyOrderAllDone)) || strings.Contains(body, i18n.T(ctx, i18n.KeyWarrantyAfterShipping)) {
				t.Errorf("unavailable next ordinal lacks its single neutral workflow state: %s", body)
			}
			if text := line.CoverText(ctx); text != "" {
				t.Errorf("unavailable next cover = %q, want no expiry claim", text)
			}
			if strings.Contains(body, `<span class="goen-badge"></span>`) {
				t.Error("unavailable next unit renders an empty coverage badge")
			}
			line.DraftSerial = " Raw & \"serial\" "
			body = renderWarrantyInLocale(t, ctx, WarrantyOrder(frame.Page{}, OrderView{Number: "GO-NEXT", Lines: []Line{line}}))
			attrs := warrantyAttributes(warrantyNodesByID(t, body)["serial-next"])
			_, readonly := attrs["readonly"]
			if attrs["value"] != line.DraftSerial || !readonly || strings.Contains(body, `name="unit"`) {
				t.Errorf("unavailable serial draft = %v, want raw readonly input without a registration unit", attrs)
			}
			end := shoptime.Date{Year: 2027, Month: 8, Day: 20}
			line.CoverEnds = &end
			if !line.Registrable() {
				t.Fatal("a delivered next unit with an expiry is not registrable")
			}
			body = renderWarrantyInLocale(t, ctx, WarrantyOrder(frame.Page{}, OrderView{Number: "GO-NEXT", Lines: []Line{line}}))
			if !strings.Contains(body, `name="unit" value="1"`) || !strings.Contains(body, "2027-08-20") || strings.Contains(body, tc.neutral) {
				t.Error("eligible next unit lost its unit1 form or own-parcel expiry")
			}
			attrs = warrantyAttributes(warrantyNodesByID(t, body)["serial-next"])
			_, readonly = attrs["readonly"]
			if readonly || strings.Count(body, `id="serial-next"`) != 1 {
				t.Errorf("eligible serial field = %v, want one editable input", attrs)
			}
		})
	}
}
