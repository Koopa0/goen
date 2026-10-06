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

func TestShippingPickupAvailabilityIsVisibleAtTheMethodHeading(t *testing.T) {
	t.Parallel()
	for _, locale := range []struct {
		locale i18n.Locale
		badge  string
		body   string
	}{
		{locale: i18n.ZhHant, badge: "結帳未提供", body: "顧客結帳時看不到超商取貨：還沒接上綠界物流。這要由架站的人設定。"},
		{locale: i18n.En, badge: "Not offered at checkout", body: "Customers cannot pick this at checkout: ECPay logistics is not connected yet. Whoever runs the server sets that up."},
	} {
		for _, tt := range []struct {
			name        string
			destination destination.Kind
			unavailable bool
		}{
			{name: "unavailable pickup", destination: destination.PickupPoint, unavailable: true},
			{name: "working pickup", destination: destination.PickupPoint},
			{name: "home delivery", destination: destination.Address},
		} {
			t.Run(locale.locale.Tag()+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale.locale)
				view := ShippingView{Methods: []ShippingMethod{{
					MethodID: "method", VersionID: "version", Code: "internal_method_code",
					Name: "Method name", Destination: tt.destination, Active: true,
					PickupUnavailable: tt.unavailable,
				}}}
				body := renderComponent(t, ctx, Shipping(layouts.Page{}, view))
				doc, err := html.Parse(strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				var badges []string
				for n := range doc.Descendants() {
					if n.Type != html.ElementNode || n.Data != "h2" {
						continue
					}
					var text strings.Builder
					for child := range n.Descendants() {
						if child.Type == html.TextNode {
							text.WriteString(child.Data)
						}
					}
					if !strings.Contains(text.String(), "Method name") {
						continue
					}
					for child := range n.Descendants() {
						if child.Type != html.ElementNode || child.Data != "span" || attr(child, "class") != "goen-badge goen-badge--warn" {
							continue
						}
						var label strings.Builder
						for part := range child.Descendants() {
							if part.Type == html.TextNode {
								label.WriteString(part.Data)
							}
						}
						badges = append(badges, strings.TrimSpace(label.String()))
					}
				}
				var wantBadges []string
				if tt.unavailable {
					wantBadges = []string{locale.badge}
				}
				if diff := cmp.Diff(wantBadges, badges); diff != "" {
					t.Errorf("method heading warning (-want +got):\n%s", diff)
				}
				if got := strings.Contains(body, locale.body); got != tt.unavailable {
					t.Errorf("pickup setup explanation present = %t, want %t: %q", got, tt.unavailable, locale.body)
				}
				for _, forbidden := range []string{"GOEN_", "internal_method_code"} {
					if strings.Contains(body, forbidden) {
						t.Errorf("delivery page still exposes %q", forbidden)
					}
				}
			})
		}
	}
}
