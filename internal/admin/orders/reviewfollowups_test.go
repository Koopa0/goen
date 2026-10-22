package orders

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
)

// TestOrderAllowanceAttentionDetail: only the unknown allowance attention has
// escaped technical details.
func TestOrderAllowanceAttentionDetail(t *testing.T) {
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

func reviewFollowupDocument(t *testing.T, markup string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}
