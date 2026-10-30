package stock

import (
	"slices"
	"strings"
	"testing"

	nethtml "golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/widget"
)

const noticeSentence = "Notice sentence for the test."

// noticePlaces renders the list with a notice that names noticeIn and returns
// where the sentence stands, once per appearance: "head" outside the table, or
// the id of the row whose own row it follows.
func noticePlaces(t *testing.T, noticeIn string) (places []string) {
	t.Helper()
	view := VariantsView{
		Variants: []Variant{{SKU: "A-1", ProductName: "x"}, {SKU: "B-2", ProductName: "y"}},
		Notice:   widget.Result{Outcome: widget.OutcomeDone, Text: noticeSentence},
		NoticeIn: noticeIn,
	}
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	doc, err := nethtml.Parse(strings.NewReader(renderComponent(t, ctx, Variants(frame.Page{}, view))))
	if err != nil {
		t.Fatal(err)
	}
	for n := range doc.Descendants() {
		if n.Type != nethtml.TextNode || !strings.Contains(n.Data, noticeSentence) {
			continue
		}
		place := "head"
		for p := n.Parent; p != nil; p = p.Parent {
			if p.Type != nethtml.ElementNode || p.Data != "tr" {
				continue
			}
			place = "table"
			for prev := p.PrevSibling; prev != nil; prev = prev.PrevSibling {
				if prev.Type == nethtml.ElementNode {
					place = attr(prev, "id")
					break
				}
			}
			break
		}
		places = append(places, place)
	}
	return places
}

// TestVariantsNoticeUnderItsRow: a notice addressed to a row is shown once, in
// the row under that row, and not again at the head of the page.
func TestVariantsNoticeUnderItsRow(t *testing.T) {
	t.Parallel()
	for _, sku := range []string{"A-1", "B-2"} {
		t.Run(sku, func(t *testing.T) {
			t.Parallel()
			if got, want := noticePlaces(t, sku), []string{"row-" + sku}; !slices.Equal(got, want) {
				t.Errorf("a notice for %s stands at %v, want %v", sku, got, want)
			}
		})
	}
}

// TestVariantsNoticeStaysAtTheHead: a notice that names no row is shown once at
// the head of the page.
func TestVariantsNoticeStaysAtTheHead(t *testing.T) {
	t.Parallel()
	if got, want := noticePlaces(t, ""), []string{"head"}; !slices.Equal(got, want) {
		t.Errorf("a notice for no row stands at %v, want %v", got, want)
	}
}

// TestVariantsView_listed: a redirect's row has a notice to hold only while the
// page still lists it.
func TestVariantsView_listed(t *testing.T) {
	t.Parallel()
	v := VariantsView{Variants: []Variant{{SKU: "A-1"}, {SKU: "B-2"}}}
	for sku, want := range map[string]string{"A-1": "A-1", "B-2": "B-2", "C-3": "", "a-1": "", "": ""} {
		if got := v.listed(sku); got != want {
			t.Errorf("listed(%q) = %q, want %q", sku, got, want)
		}
	}
}
