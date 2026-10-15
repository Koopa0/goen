package admin

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestReturnedRateBarsShareOneTrackScale(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(t.Context(), locale)
		view := returnedReport(
			ReturnedProduct{Name: "Large", Returned: 250, Sold: 1000},
			ReturnedProduct{Name: "Small", Returned: 5, Sold: 20},
			ReturnedProduct{Name: "Rare", Returned: 1, Sold: 100000},
			ReturnedProduct{Name: "Too few", Returned: 1, Sold: 4},
		)
		doc := reviewFollowupDocument(t, renderComponent(t, ctx, returnedProducts(view)))
		var classes, widths []string
		for node := range doc.Descendants() {
			if node.Data == "div" && strings.HasPrefix(attr(node, "class"), "goen-chartbar") {
				classes = append(classes, attr(node, "class"))
			}
			if node.Data == "rect" && attr(node, "class") == "goen-chartbar__fill" {
				widths = append(widths, attr(node, "width"))
			}
		}
		if len(classes) != 3 {
			t.Fatalf("%s: rate bars = %d, want three", locale, len(classes))
		}
		for _, class := range classes[1:] {
			if class != classes[0] {
				t.Errorf("%s: rate bars use different track classes %q and %q", locale, classes[0], class)
			}
		}
		if diff := cmp.Diff([]string{"25.00%", "25.00%", "0.60%"}, widths); diff != "" {
			t.Errorf("%s: rate widths (-want +got):\n%s", locale, diff)
		}
	}
}

func TestOnlyUnknownAllowanceAttentionHasEscapedTechnicalDetails(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		category string
		key      i18n.Key
		raw      bool
	}{
		{string(invoice.ReasonAllowanceBuyerUnconfirmed), i18n.KeyAdminQueueAllowanceUnconfirmed, false},
		{string(invoice.ReasonAllowanceAmountStillHeld), i18n.KeyAdminQueueAllowanceAmountHeld, false},
		{string(invoice.ReasonAllowanceSuccessMismatch), i18n.KeyAdminQueueAllowanceMismatch, false},
		{string(invoice.ReasonAllowanceMultipleUnknownCandidates), i18n.KeyAdminQueueAllowanceCandidates, false},
		{"allowance_lookup_mismatch", i18n.KeyAdminQueueAllowanceAttention, true},
		{`unknown_<script>alert("category")</script>`, i18n.KeyAdminQueueAllowanceAttention, true},
	} {
		t.Run(tc.category, func(t *testing.T) {
			t.Parallel()
			for _, locale := range i18n.Locales() {
				ctx := i18n.WithLocale(t.Context(), locale)
				doc := reviewFollowupDocument(t, renderOrder(t, locale, &OrderView{Number: "GO-1", AllowanceAttention: tc.category}))
				var details []*html.Node
				for node := range doc.Descendants() {
					if node.Data == "details" && attr(node, "class") == "goen-admin__rawdetail" {
						details = append(details, node)
					}
				}
				if !hasElement(doc, func(n *html.Node) bool {
					return n.Data == "p" && attr(n, "role") == "status" && textOf(n) == i18n.T(ctx, tc.key)
				}) {
					t.Errorf("%s: the worded allowance explanation is missing", locale)
				}
				if !tc.raw {
					if len(details) != 0 {
						t.Errorf("%s: known category has %d raw disclosures, want none", locale, len(details))
					}
					continue
				}
				if len(details) != 1 {
					t.Errorf("%s: unknown category has %d raw disclosures, want one", locale, len(details))
					continue
				}
				detail := details[0]
				if _, open := attrPresent(detail, "open"); open {
					t.Error("technical details start open")
				}
				var summary, code *html.Node
				for node := range detail.Descendants() {
					if node.Data == "summary" {
						summary = node
					}
					if node.Data == "code" {
						code = node
					}
					if node.Type == html.ElementNode && node.Data == "script" {
						t.Error("the category was interpreted as markup")
					}
				}
				if summary == nil || textOf(summary) != i18n.T(ctx, i18n.KeyAdminTechnicalDetails) {
					t.Errorf("%s: technical details has no localized native summary", locale)
				}
				if code == nil || textOf(code) != tc.category {
					t.Errorf("%s: technical details does not retain the raw category as text", locale)
				}
			}
		})
	}
}

func TestShippingZonesKeepTableRelationshipsWhenRowsBecomeCards(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		ctx := i18n.WithLocale(t.Context(), locale)
		view := ShippingView{Zones: []ShippingZone{
			{ID: "mainland", Code: "mainland", Name: "本島", NameEn: "Mainland"},
			{ID: "offshore", Code: "offshore", Name: "離島", NameEn: "Offshore"},
		}}
		doc := reviewFollowupDocument(t, renderComponent(t, ctx, Shipping(layouts.Page{Title: "Shipping"}, view)))
		var table, heading *html.Node
		for node := range doc.Descendants() {
			if node.Data == "table" && strings.Contains(attr(node, "class"), "goen-admin__zones") {
				table = node
			}
			if attr(node, "id") == "shipping-zones-heading" {
				heading = node
			}
		}
		if table == nil {
			t.Fatal("shipping zones table is missing")
		}
		if attr(table, "role") != "table" {
			t.Errorf("%s: zone table role = %q, want table", locale, attr(table, "role"))
		}
		if heading == nil || attr(table, "aria-labelledby") != "shipping-zones-heading" || textOf(heading) != i18n.T(ctx, i18n.KeyAdminShipZones) {
			t.Errorf("%s: the table is not named by its visible heading", locale)
		}
		counts := make(map[string]int)
		roles := map[string]string{"thead": "rowgroup", "tbody": "rowgroup", "tr": "row", "th": "columnheader", "td": "cell"}
		for node := range table.Descendants() {
			if role, exists := roles[node.Data]; node.Type == html.ElementNode && exists {
				counts[node.Data]++
				if attr(node, "role") != role {
					t.Errorf("%s: %s role = %q, want %q", locale, node.Data, attr(node, "role"), role)
				}
				if node.Data == "th" && attr(node, "scope") != "col" {
					t.Errorf("%s: a column header lost its scope", locale)
				}
			}
		}
		if diff := cmp.Diff(map[string]int{"thead": 1, "tbody": 1, "tr": 3, "th": 3, "td": 6}, counts); diff != "" {
			t.Errorf("%s: table relationships (-want +got):\n%s", locale, diff)
		}
	}
}

func reviewFollowupDocument(t *testing.T, markup string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}
