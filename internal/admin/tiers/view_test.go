package tiers

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/google/go-cmp/cmp"
	xhtml "golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

// The tests in this file are the tiers halves of tests that render several
// desks' pages: each half lives with the page it renders.

// TestTiersExampleLine: the tiers example is its own hint line.
// From TestProductFormExampleLine.
func TestTiersExampleLine(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	body := renderComponent(t, ctx, Tiers(frame.Page{Title: "Tiers"}, TiersView{}))
	if strings.Contains(body, "。 ") {
		t.Errorf("tiers page runs a sentence into the next on one line: %q", body[strings.Index(body, "。 ")-20:strings.Index(body, "。 ")+30])
	}
	if !strings.Contains(body, "例如 ") {
		t.Error("tiers page has no example line")
	}
}

// TestTiersExampleHints: every tiers example hint reads in full.
// From TestProductFormExampleHints.
func TestTiersExampleHints(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale   i18n.Locale
		sentence func(example string) string
	}{
		{i18n.ZhHant, func(e string) string { return "<p class=\"goen-admin__hint\">例如 " + e + "。</p>" }},
		{i18n.En, func(e string) string { return "<p class=\"goen-admin__hint\">For example, " + e + ".</p>" }},
	} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			body := renderComponent(t, ctx, Tiers(frame.Page{Title: "Tiers"}, TiersView{}))
			for _, e := range []string{"Silver"} {
				if want := tt.sentence(e); !strings.Contains(body, want) {
					t.Errorf("tiers page lacks the hint %s", want)
				}
			}
		})
	}
}

// TestTiersContentColumn: the tiers screen puts its work in the content column.
// From TestNewsletterContentColumn.
func TestTiersContentColumn(t *testing.T) {
	t.Parallel()
	ctx := frame.WithStaff(i18n.WithLocale(t.Context(), i18n.ZhHant), true)

	name := "tiers"
	page := renderComponent(t, ctx, Tiers(frame.Page{}, TiersView{}))

	grid := strings.Index(page, `<div class="goen-admin">`)
	rail := strings.Index(page, "goen-admin__nav")
	column := strings.Index(page, `class="goen-admin__main"`)
	work := strings.Index(page, `class="ui-page-head"`)
	if grid < 0 || rail < 0 || column < 0 || work < 0 {
		t.Fatalf("%s: rendered no back-office frame at all (frame=%d rail=%d "+
			"column=%d work=%d); the order below would prove nothing",
			name, grid, rail, column, work)
	}
	if grid > rail || rail > column || column > work {
		t.Errorf("%s: the frame reads frame=%d rail=%d column=%d work=%d — the "+
			"work has to open INSIDE the content column, or it is a sibling of "+
			"the rail and the grid puts the rail on a row of its own",
			name, grid, rail, column, work)
	}
	if n := strings.Count(page, `class="goen-admin__main"`); n != 1 {
		t.Errorf("%s: %d content columns, want exactly 1 — a second one is a "+
			"second grid child and the same defect one level down", name, n)
	}
}

// TestTiersRemoveButtons: the tiers remove buttons name their record.
// From TestProductImagesRemoveButtons (the "membership tiers" case).
func TestTiersRemoveButtons(t *testing.T) {
	t.Parallel()
	for _, locale := range []struct {
		locale i18n.Locale
		remove string
	}{
		{locale: i18n.ZhHant, remove: "移除"},
		{locale: i18n.En, remove: "Remove"},
	} {
		tt := struct {
			name   string
			action string
			field  string
			want   map[string]string
		}{name: "membership tiers", action: "/admin/tiers/delete", field: "tier", want: map[string]string{"silver": "Silver & more", "gold": "Gold"}}
		t.Run(locale.locale.Tag()+"/"+tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale.locale)
			component := Tiers(frame.Page{}, TiersView{Rows: []Tier{{ID: "silver", Name: "Silver & more", Code: "silver"}, {ID: "gold", Name: "Gold", Code: "gold"}}})
			doc, err := xhtml.Parse(strings.NewReader(renderComponent(t, ctx, component)))
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]string{}
			for form := range doc.Descendants() {
				if form.Type != xhtml.ElementNode || form.Data != "form" || attr(form, "action") != tt.action {
					continue
				}
				if attr(form, "method") != "post" {
					t.Errorf("removal form method = %q, want post", attr(form, "method"))
				}
				var record string
				var button *xhtml.Node
				for n := range form.Descendants() {
					if n.Data == "input" && attr(n, "name") == tt.field {
						record = attr(n, "value")
					}
					if n.Data == "button" && attr(n, "type") == "submit" {
						button = n
					}
				}
				if button == nil {
					t.Fatal("removal form has no submit button")
				}
				var visible, hidden strings.Builder
				seenRecord := false
				for n := button.FirstChild; n != nil; n = n.NextSibling {
					if n.Type == xhtml.TextNode {
						if seenRecord && strings.TrimSpace(n.Data) != "" {
							t.Errorf("record %q precedes the visible action in the accessible name", record)
						}
						visible.WriteString(n.Data)
						continue
					}
					if n.Data != "span" || attr(n, "class") != "goen-sr-only" {
						t.Errorf("record %q uses a visible or unexpected label element", record)
						continue
					}
					if _, present := attrPresent(n, "hidden"); present || attr(n, "aria-hidden") == "true" {
						t.Errorf("record %q is hidden from assistive technology", record)
					}
					seenRecord = true
					for text := range n.Descendants() {
						if text.Type == xhtml.TextNode {
							hidden.WriteString(text.Data)
						}
					}
				}
				if label := strings.TrimSpace(visible.String()); label != locale.remove {
					t.Errorf("record %q visible label = %q, want %q", record, label, locale.remove)
				}
				if attr(button, "aria-label") != "" || attr(button, "aria-labelledby") != "" {
					t.Errorf("record %q overrides its visible label and record text", record)
				}
				got[record] = strings.TrimSpace(hidden.String())
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("removal button records (-want +got):\n%s", diff)
			}
		})
	}
}

// TestTiersMultiplierColumn: the tiers multiplier column is numeric.
// Moved whole from small_defects_test.go.
func TestTiersMultiplierColumn(t *testing.T) {
	t.Parallel()
	body := render(t, Tiers(frame.Page{Title: "Tiers"}, TiersView{Rows: []Tier{{ID: "1", Code: "gold", Name: "Gold", MinSpend: 10000, MultiplierBP: 11000, Members: 4}}}))
	if !strings.Contains(body, `<td class="goen-admin__cellnum">1.1`) {
		t.Errorf("tiers multiplier cell is not numeric:\n%s", body)
	}
	if !strings.Contains(body, `<th class="goen-admin__cellnum" scope="col">Points rate`) {
		t.Errorf("tiers multiplier head is not numeric:\n%s", body)
	}
}

// TestTier_Multiplier: tier multipliers retain basis-point precision.
// From TestMemberStanding_MultiplierPrecision (the "staff" format).
func TestTier_Multiplier(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		bp   int32
		en   string
		zh   string
	}{
		{name: "100 percent", bp: 10000, en: "1×", zh: "1 倍"},
		{name: "101 percent", bp: 10100, en: "1.01×", zh: "1.01 倍"},
		{name: "125 percent", bp: 12500, en: "1.25×", zh: "1.25 倍"},
		{name: "150 percent", bp: 15000, en: "1.5×", zh: "1.5 倍"},
		{name: "300 percent", bp: 30000, en: "3×", zh: "3 倍"},
		{name: "one basis point above minimum", bp: 10001, en: "1.0001×", zh: "1.0001 倍"},
		{name: "four significant decimal places", bp: 12501, en: "1.2501×", zh: "1.2501 倍"},
		{name: "one basis point below maximum", bp: 29999, en: "2.9999×", zh: "2.9999 倍"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			format := func(ctx context.Context) string { return Tier{MultiplierBP: tt.bp}.Multiplier(ctx) }
			for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
				t.Run("staff/"+locale.Tag(), func(t *testing.T) {
					t.Parallel()
					want := tt.en
					if locale == i18n.ZhHant {
						want = tt.zh
					}
					if got := format(i18n.WithLocale(t.Context(), locale)); got != want {
						t.Errorf("Multiplier(%d) = %q, want %q", tt.bp, got, want)
					}
				})
			}
		})
	}
}

// render is the page tests' helper: c under the English locale.
func render(t *testing.T, c templ.Component) string {
	t.Helper()
	var out bytes.Buffer
	if err := c.Render(i18n.WithLocale(t.Context(), i18n.En), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}
