package admin

import (
	"bytes"
	"fmt"
	"slices"
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
					Name: "Method name", NameEn: "Method name", Destination: tt.destination, Active: true,
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

func TestShippingZonePrefixesHaveCompleteMultilineEditors(t *testing.T) {
	t.Parallel()
	const islands = "209 210 211 212 880 881 882 883 884 885 890 891 892 893 894 896 951 952"
	islandDistricts := []string{
		"209 連江縣南竿鄉", "210 連江縣北竿鄉", "211 連江縣莒光鄉", "212 連江縣東引鄉",
		"880 澎湖縣馬公市", "881 澎湖縣西嶼鄉", "882 澎湖縣望安鄉", "883 澎湖縣七美鄉", "884 澎湖縣白沙鄉", "885 澎湖縣湖西鄉",
		"890 金門縣金沙鎮", "891 金門縣金湖鎮", "892 金門縣金寧鄉", "893 金門縣金城鎮", "894 金門縣烈嶼鄉", "896 金門縣烏坵鄉",
		"951 臺東縣綠島鄉", "952 臺東縣蘭嶼鄉",
	}
	for _, locale := range i18n.Locales() {
		for _, tt := range []struct {
			name      string
			raw       string
			districts []string
			rows      string
			draft     bool
			newZone   bool
			refused   bool
		}{
			{name: "stored island zone", raw: islands, districts: islandDistricts, rows: "3"},
			{name: "refused existing zone", raw: "\n209,\n880;999 <bad>\n300\t", districts: []string{"209 連江縣南竿鄉", "880 澎湖縣馬公市", "999", "<bad>", "300 新竹市北區 新竹市東區 新竹市香山區"}, rows: "5", draft: true, refused: true},
			{name: "cleared existing zone", rows: "3", draft: true},
			{name: "refused new zone", raw: "\n209,\n880;999 <bad>\n300\t", districts: []string{"209 連江縣南竿鄉", "880 澎湖縣馬公市", "999", "<bad>", "300 新竹市北區 新竹市東區 新竹市香山區"}, rows: "5", newZone: true, refused: true},
			{name: "blank new zone refusal", rows: "3", newZone: true, refused: true},
		} {
			t.Run(locale.Tag()+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				view := ShippingView{Zones: []ShippingZone{
					{ID: "islands", Name: "Islands", NameEn: "Islands", Prefixes: islands},
					{ID: "neighbour", Name: "Neighbour", NameEn: "Neighbour", Prefixes: "100"},
				}, Errors: map[string]string{}}
				message := i18n.T(ctx, i18n.KeyFormZonePrefixRequired)
				if tt.raw != "" {
					message = fmt.Sprintf(i18n.T(ctx, i18n.KeyFormZonePrefixShape), "<bad>")
				}
				if tt.draft {
					view.PrefixDraft = ZonePrefixesDraft{ZoneID: "islands", Prefixes: tt.raw}
					if tt.refused {
						view.Errors["zone_prefixes"] = message
					}
				}
				if tt.newZone {
					view.ZoneDraft.Prefixes = tt.raw
					if tt.refused {
						view.Errors["prefixes"] = message
					}
				}
				body := renderComponent(t, ctx, Shipping(layouts.Page{}, view))
				forms := shippingPrefixControls(t, body)
				if len(forms) != 3 {
					t.Fatalf("prefix editors = %d, want two zones and the new-zone form", len(forms))
				}
				wanted := map[string]shippingPrefixControl{
					"pre-islands":   {Element: "textarea", Raw: islands, Rows: "3", Class: "ui-textarea goen-input--area", Method: "post", FormClass: "goen-admin__form", Action: "/admin/shipping/zone/islands/prefixes", FullWidth: true, VisibleLabel: true, Districts: islandDistricts},
					"pre-neighbour": {Element: "textarea", Raw: "100", Rows: "3", Class: "ui-textarea goen-input--area", Method: "post", FormClass: "goen-admin__form", Action: "/admin/shipping/zone/neighbour/prefixes", FullWidth: true, VisibleLabel: true, Districts: []string{"100 臺北市中正區"}},
					"z-prefixes":    {Element: "textarea", Rows: "3", Class: "ui-textarea goen-input--area", Method: "post", FormClass: "goen-admin__form", Action: "/admin/shipping/zone", FullWidth: true, VisibleLabel: true, Required: true},
				}
				id := "pre-islands"
				if tt.newZone {
					id = "z-prefixes"
				}
				row := wanted[id]
				row.Raw, row.Rows, row.Districts = tt.raw, tt.rows, tt.districts
				if tt.refused {
					row.Invalid, row.DescribedBy, row.Error = "true", id+"-error", message
				}
				wanted[id] = row
				if diff := cmp.Diff(wanted, forms); diff != "" {
					t.Errorf("zone prefix editors and their district lists (-want +got):\n%s", diff)
				}
			})
		}
	}
}

type shippingPrefixControl struct {
	Element, Raw, Rows, Class         string
	Method, FormClass, Action         string
	Invalid, DescribedBy, Error       string
	FullWidth, VisibleLabel, Required bool
	Districts                         []string
}

func shippingPrefixControls(t *testing.T, body string) map[string]shippingPrefixControl {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	controls := make(map[string]shippingPrefixControl)
	for form := range doc.Descendants() {
		if form.Type != html.ElementNode || form.Data != "form" {
			continue
		}
		for node := range form.Descendants() {
			if node.Type != html.ElementNode || attr(node, "name") != "prefixes" {
				continue
			}
			id := attr(node, "id")
			if _, exists := controls[id]; exists {
				t.Fatalf("duplicate prefix control ID %q", id)
			}
			_, required := attrPresent(node, "required")
			row := shippingPrefixControl{
				Element: node.Data, Raw: shippingPrefixNodeText(node), Rows: attr(node, "rows"), Class: attr(node, "class"),
				Method: attr(form, "method"), FormClass: attr(form, "class"), Action: attr(form, "action"),
				Invalid: attr(node, "aria-invalid"), DescribedBy: attr(node, "aria-describedby"),
				FullWidth: attr(form, "class") == "goen-admin__form", Required: required,
			}
			if node.Data == "input" {
				row.Raw = attr(node, "value")
			}
			for ancestor := node.Parent; ancestor != nil && ancestor != form; ancestor = ancestor.Parent {
				if strings.Contains(attr(ancestor, "class"), "goen-admin__fields") {
					row.FullWidth = false
				}
			}
			for child := range form.Descendants() {
				if child.Type != html.ElementNode {
					continue
				}
				if child.Data == "label" && attr(child, "for") == id {
					row.VisibleLabel = !strings.Contains(attr(child, "class"), "goen-sr-only") && strings.TrimSpace(shippingPrefixNodeText(child)) != ""
				}
				if child.Data == "p" && attr(child, "id") == row.DescribedBy && row.DescribedBy != "" {
					row.Error = shippingPrefixNodeText(child)
				}
				if child.Data == "li" {
					row.Districts = append(row.Districts, strings.Join(strings.Fields(shippingPrefixNodeText(child)), " "))
				}
			}
			controls[id] = row
		}
	}
	return controls
}

func shippingPrefixNodeText(node *html.Node) string {
	var text strings.Builder
	for child := range node.Descendants() {
		if child.Type == html.TextNode {
			text.WriteString(child.Data)
		}
	}
	return text.String()
}

func TestZonePrefixEntriesListARepeatedPrefixOnce(t *testing.T) {
	t.Parallel()
	entries := zonePrefixEntries("100, 300\n100;300 600")
	got := make([]string, 0, len(entries))
	for _, e := range entries {
		got = append(got, e.Prefix)
	}
	if want := []string{"100", "300", "600"}; !slices.Equal(got, want) {
		t.Errorf("zonePrefixEntries prefixes = %q, want %q", got, want)
	}
}

func TestShippingNamesAndCountsReadInTheReadersLanguage(t *testing.T) {
	t.Parallel()
	view := ShippingView{
		Methods: []ShippingMethod{{
			MethodID: "home", VersionID: "v1", Destination: destination.Address,
			Name: "宅配到府", NameEn: "Home delivery", EffectiveAt: "2026-10-07", VersionCount: 1,
		}},
		Zones: []ShippingZone{
			{ID: "islands", Code: "islands", Name: "離島", NameEn: "Outlying islands", PrefixCount: 1},
			{ID: "plain", Code: "plain", Name: "本島", PrefixCount: 2},
		},
	}
	for _, tt := range []struct {
		locale i18n.Locale
		want   []string
		absent []string
	}{
		{i18n.En, []string{"Home delivery", "宅配到府", "Outlying islands", "離島", "1 version so far", "1 postal code<", "2 postal codes", "No English"}, []string{"1 versions", "1 postal codes"}},
		{i18n.ZhHant, []string{"宅配到府", "Home delivery", "離島", "Outlying islands", "共 1 個版本", "1 個郵遞區號", "未翻譯"}, nil},
	} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			var out bytes.Buffer
			if err := Shipping(layouts.Page{Title: "Shipping"}, view).Render(ctx, &out); err != nil {
				t.Fatal(err)
			}
			page := out.String()
			for _, w := range tt.want {
				if !strings.Contains(page, w) {
					t.Errorf("%s page lacks %q", tt.locale.Tag(), w)
				}
			}
			for _, a := range tt.absent {
				if strings.Contains(page, a) {
					t.Errorf("%s page still has %q", tt.locale.Tag(), a)
				}
			}
			heading := strings.Index(page, `<h2 class="goen-admin__heading">`)
			first, second := "宅配到府", "Home delivery"
			if tt.locale == i18n.En {
				first, second = second, first
			}
			if heading < 0 || strings.Index(page[heading:], first) > strings.Index(page[heading:], second) {
				t.Errorf("%s method heading does not lead with %q", tt.locale.Tag(), first)
			}
		})
	}
}
