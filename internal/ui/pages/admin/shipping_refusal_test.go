package admin

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/destination"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestShippingRefusalsKeepTheirOwnDraftAndExplainOnlyTheirOwnControls(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			view := ShippingView{
				FeeMaxDollars: 5000, FreeOverMaxDollars: 100000000,
				Methods: []ShippingMethod{
					{MethodID: "owned", VersionID: "owned-version", Destination: destination.Address, Name: "Stored", FeeCents: 10000},
					{MethodID: "other", VersionID: "other-version", Destination: destination.Address, Name: "Other", FeeCents: 20000},
				},
				Zones:        []ShippingZone{{ID: "zone-a", Name: "Zone A"}, {ID: "zone-b", Name: "Zone B"}},
				VersionDraft: VersionDraft{MethodID: "owned", Name: " Draft & name ", NameEn: " Draft name EN ", Carrier: " Draft carrier ", CarrierEn: " Draft carrier EN ", Fee: " malformed ", FreeOver: "+1000"},
				Errors:       map[string]string{"version_fee": fmt.Sprintf(i18n.T(ctx, i18n.KeyFormMethodFee), "NT$5,000")},
			}
			body := renderComponent(t, ctx, Shipping(layouts.Page{}, view))
			for id, want := range map[string]string{
				"name-owned": view.VersionDraft.Name, "name-en-owned": view.VersionDraft.NameEn,
				"carrier-owned": view.VersionDraft.Carrier, "carrier-en-owned": view.VersionDraft.CarrierEn,
				"fee-owned": view.VersionDraft.Fee, "free-owned": view.VersionDraft.FreeOver,
				"name-other": "Other", "fee-other": "200", "sur-owned-version-zone-b": "", "sur-other-version-zone-a": "", "m-fee": "",
			} {
				attrs := shippingControlAttributes(t, body, id)
				if attrs["value"] != want {
					t.Errorf("input %q value=%q, want %q", id, attrs["value"], want)
				}
				if id != "fee-owned" && attrs["aria-invalid"] != "" {
					t.Errorf("unrefused input %q inherited an error", id)
				}
			}
			attrs := shippingControlAttributes(t, body, "fee-owned")
			if diff := cmp.Diff([]string{"text", "true", "fee-owned-error", "5000"}, []string{attrs["type"], attrs["aria-invalid"], attrs["aria-describedby"], attrs["max"]}); diff != "" {
				t.Errorf("refused fee attributes (-want +got):\n%s", diff)
			}
			if !strings.Contains(body, `id="fee-owned-error"`) || !strings.Contains(body, fmt.Sprintf(i18n.T(ctx, i18n.KeyFormMethodFee), "NT$5,000")) {
				t.Error("the refused fee has no linked translated explanation")
			}
			view.VersionDraft = VersionDraft{}
			view.SurchargeDraft = SurchargeDraft{MethodID: "owned", ZoneID: "zone-a", Amount: " 5010 "}
			view.Errors = map[string]string{"surcharge": fmt.Sprintf(i18n.T(ctx, i18n.KeyFormShippingSurcharge), "NT$5,000")}
			body = renderComponent(t, ctx, Shipping(layouts.Page{}, view))
			attrs = shippingControlAttributes(t, body, "sur-owned-version-zone-a")
			if diff := cmp.Diff([]string{" 5010 ", "text", "true", "sur-owned-version-zone-a-error", "5000"}, []string{attrs["value"], attrs["type"], attrs["aria-invalid"], attrs["aria-describedby"], attrs["max"]}); diff != "" {
				t.Errorf("refused surcharge attributes (-want +got):\n%s", diff)
			}
			for _, id := range []string{"sur-owned-version-zone-b", "sur-other-version-zone-a", "sur-other-version-zone-b", "fee-owned", "m-fee"} {
				if attrs := shippingControlAttributes(t, body, id); attrs["aria-invalid"] != "" {
					t.Errorf("surcharge refusal leaked to %q", id)
				}
			}
			if !strings.Contains(body, `id="sur-owned-version-zone-a-error"`) || !strings.Contains(body, fmt.Sprintf(i18n.T(ctx, i18n.KeyFormShippingSurcharge), "NT$5,000")) {
				t.Error("the surcharge has no linked translated explanation")
			}
			for id, max := range map[string]string{"fee-owned": "5000", "free-owned": "100000000", "sur-other-version-zone-b": "5000"} {
				attrs := shippingControlAttributes(t, body, id)
				if attrs["type"] != "number" || attrs["max"] != max {
					t.Errorf("normal numeric control %s type/max=%q/%q", id, attrs["type"], attrs["max"])
				}
			}
		})
	}
}

func TestShippingDatabaseRefusalIsAnnouncedOnTheOwningForm(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			message := i18n.T(ctx, i18n.KeyAdminShipRefused)
			view := ShippingView{Methods: []ShippingMethod{{MethodID: "owned", VersionID: "version", Name: "Stored", Destination: destination.Address}}, VersionDraft: VersionDraft{MethodID: "owned", Name: "Draft", Fee: "100"}, Errors: map[string]string{"version_form": message}}
			body := renderComponent(t, ctx, Shipping(layouts.Page{}, view))
			doc, err := html.Parse(strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			var alerts []string
			for n := range doc.Descendants() {
				if n.Type != html.ElementNode {
					continue
				}
				var id, role string
				for _, attr := range n.Attr {
					if attr.Key == "id" {
						id = attr.Val
					}
					if attr.Key == "role" {
						role = attr.Val
					}
				}
				if role == "alert" {
					var text strings.Builder
					for child := range n.Descendants() {
						if child.Type == html.TextNode {
							text.WriteString(child.Data)
						}
					}
					alerts = append(alerts, id+":"+text.String())
				}
			}
			if diff := cmp.Diff([]string{"version-owned-error:" + message}, alerts); diff != "" {
				t.Errorf("form refusal announcements (-want +got):\n%s", diff)
			}
		})
	}
}

func shippingControlAttributes(t *testing.T, body, id string) map[string]string {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for n := range doc.Descendants() {
		if n.Type != html.ElementNode || n.Data != "input" {
			continue
		}
		attrs := make(map[string]string, len(n.Attr))
		for _, a := range n.Attr {
			attrs[a.Key] = a.Val
		}
		if attrs["id"] == id {
			return attrs
		}
	}
	t.Fatalf("no input %q", id)
	return nil
}
