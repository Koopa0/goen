package admin

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/destination"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestShippingStaleSurchargeKeepsTheDraftOnTheCurrentForm(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		for _, amount := range []string{"200", ""} {
			t.Run(locale.Tag()+"/amount="+amount, func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				message := i18n.T(ctx, i18n.KeyAdminShipVersionChanged)
				view := ShippingView{
					Methods: []ShippingMethod{
						{MethodID: "one", VersionID: "current", Destination: destination.Address, Surcharges: []ZoneSurcharge{{ZoneID: "offshore", Cents: 10000}, {ZoneID: "other", Cents: 30000}}},
						{MethodID: "two", VersionID: "neighbour", Destination: destination.Address, Surcharges: []ZoneSurcharge{{ZoneID: "offshore", Cents: 40000}}},
					},
					Zones:          []ShippingZone{{ID: "offshore"}, {ID: "other"}},
					Errors:         map[string]string{"surcharge": message},
					SurchargeDraft: SurchargeDraft{MethodID: "one", ZoneID: "offshore", Amount: amount},
				}
				var out bytes.Buffer
				if err := Shipping(layouts.Page{Title: "Shipping"}, view).Render(ctx, &out); err != nil {
					t.Fatal(err)
				}
				doc, err := html.Parse(strings.NewReader(out.String()))
				if err != nil {
					t.Fatal(err)
				}
				inputs := map[string]map[string]string{}
				messages := map[string]string{}
				for n := range doc.Descendants() {
					attrs := map[string]string{}
					for _, a := range n.Attr {
						attrs[a.Key] = a.Val
					}
					if n.Type == html.ElementNode && n.Data == "input" {
						inputs[attrs["id"]] = attrs
					}
					if n.Type == html.ElementNode && n.Data == "p" && n.FirstChild != nil {
						if _, exists := messages[attrs["id"]]; attrs["id"] != "" && exists {
							t.Errorf("duplicate message ID %q", attrs["id"])
						}
						messages[attrs["id"]] = n.FirstChild.Data
					}
				}
				id := "sur-current-offshore"
				got := []string{inputs[id]["value"], inputs[id]["aria-invalid"], inputs[id]["aria-describedby"], messages[id+"-error"]}
				want := []string{amount, "true", id + "-error", message}
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("refused current form (-want +got):\n%s", diff)
				}
				for otherID, wantValue := range map[string]string{"sur-current-other": "300", "sur-neighbour-offshore": "400"} {
					if diff := cmp.Diff([]string{wantValue, "", ""}, []string{inputs[otherID]["value"], inputs[otherID]["aria-invalid"], inputs[otherID]["aria-describedby"]}); diff != "" {
						t.Errorf("unaffected form %q (-want +got):\n%s", otherID, diff)
					}
				}
			})
		}
	}
}
