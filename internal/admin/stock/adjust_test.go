package stock

import (
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	nethtml "golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

// formsPosting returns the forms of a page that post to action.
func formsPosting(t *testing.T, markup, action string) []*nethtml.Node {
	t.Helper()
	doc, err := nethtml.Parse(strings.NewReader(markup))
	if err != nil {
		t.Fatal(err)
	}
	var found []*nethtml.Node
	for n := range doc.Descendants() {
		if n.Type == nethtml.ElementNode && n.Data == "form" && attr(n, "action") == action {
			found = append(found, n)
		}
	}
	return found
}

func inDetails(n *nethtml.Node) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Type == nethtml.ElementNode && p.Data == "details" {
			return true
		}
	}
	return false
}

func hasDescendant(n *nethtml.Node, match func(*nethtml.Node) bool) bool {
	for d := range n.Descendants() {
		if d.Type == nethtml.ElementNode && match(d) {
			return true
		}
	}
	return false
}

// TestMovementsCorrectionForm: the SKU page carries the correction, with a key
// of its own.
func TestMovementsCorrectionForm(t *testing.T) {
	t.Parallel()
	view := MovementsView{SKU: "SKU", Slug: "product", FormID: "first-page", Active: true}
	markup := renderComponent(t, i18n.WithLocale(t.Context(), i18n.En), Movements(frame.Page{}, &view))
	forms := formsPosting(t, markup, "/admin/stock/adjust")
	if len(forms) != 1 {
		t.Fatalf("%d correction forms, want 1", len(forms))
	}
	for _, want := range []func(*nethtml.Node) bool{
		func(n *nethtml.Node) bool {
			return n.Data == "input" && attr(n, "name") == "idempotency" && attr(n, "value") == "adj:SKU:first-page"
		},
		func(n *nethtml.Node) bool {
			return n.Data == "input" && attr(n, "name") == "sku" && attr(n, "value") == "SKU"
		},
		func(n *nethtml.Node) bool {
			return n.Data == "input" && attr(n, "name") == "delta" && attr(n, "id") == "adjust-delta"
		},
	} {
		if !hasDescendant(forms[0], want) {
			t.Error("the correction form lost a field")
		}
	}
	if strings.Contains(markup, `aria-invalid="true"`) {
		t.Error("a fresh correction form reports a refusal before submission")
	}
}

// TestMovementsRetireDisclosure: switching a variant off asks for a second
// click, and switching it on does not.
func TestMovementsRetireDisclosure(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	t.Run("active", func(t *testing.T) {
		t.Parallel()
		markup := renderComponent(t, ctx, Movements(frame.Page{}, &MovementsView{SKU: "SKU", Active: true}))
		forms := formsPosting(t, markup, "/admin/stock/active")
		if len(forms) != 1 {
			t.Fatalf("%d active forms, want 1", len(forms))
		}
		if !inDetails(forms[0]) {
			t.Error("the switch-off form is not behind the disclosure")
		}
		if !hasDescendant(forms[0], func(n *nethtml.Node) bool {
			return n.Data == "input" && attr(n, "name") == "active" && attr(n, "value") == "0"
		}) || !hasDescendant(forms[0], func(n *nethtml.Node) bool {
			return n.Data == "button" && strings.Contains(attr(n, "class"), "goen-btn--danger")
		}) {
			t.Error("the switch-off form does not post active=0 from a danger button")
		}
	})
	t.Run("inactive", func(t *testing.T) {
		t.Parallel()
		markup := renderComponent(t, ctx, Movements(frame.Page{}, &MovementsView{SKU: "SKU"}))
		forms := formsPosting(t, markup, "/admin/stock/active")
		if len(forms) != 1 || !hasDescendant(forms[0], func(n *nethtml.Node) bool {
			return n.Data == "input" && attr(n, "name") == "active" && attr(n, "value") == "1"
		}) {
			t.Fatal("an inactive variant has no switch-on form")
		}
		if inDetails(forms[0]) {
			t.Error("switching on is behind a disclosure")
		}
	})
}

// TestHandler_AdjustmentRefusalForm: a refused correction is the SKU page at
// 422 with the typed figure, its reason and the key it was posted with.
func TestHandler_AdjustmentRefusalForm(t *testing.T) {
	t.Parallel()
	for _, locale := range i18n.Locales() {
		for _, raw := range []string{"0", " 12<x> ", ""} {
			t.Run(locale.Tag()+"/"+raw, func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				form := url.Values{"sku": {"SKU"}, "delta": {raw}, "idempotency": {`adj:SKU:original<&>`}}
				req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/stock/adjust", strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				if err := req.ParseForm(); err != nil {
					t.Fatal(err)
				}
				rec := httptest.NewRecorder()
				h := Handler{log: slog.New(slog.DiscardHandler)}
				h.renderAdjustmentRefusal(rec, req, &MovementsView{SKU: "SKU", Slug: "product", FormID: "new-page", Active: true}, i18n.KeyAdminStockDeltaError)
				if rec.Code != http.StatusUnprocessableEntity || rec.Header().Get("Location") != "" {
					t.Errorf("adjustment refusal = %d %q, want 422 without redirect", rec.Code, rec.Header().Get("Location"))
				}
				body := rec.Body.String()
				input := regexp.MustCompile(`<input[^>]*id="adjust-delta"[^>]*>`).FindString(body)
				for _, want := range []string{`value="` + html.EscapeString(raw) + `"`, `aria-invalid="true"`, `aria-describedby="adjust-delta-example adjust-delta-error"`} {
					if !strings.Contains(input, want) {
						t.Errorf("correction control is missing %q", want)
					}
				}
				for _, want := range []string{
					`name="idempotency" value="adj:SKU:original&lt;&amp;&gt;"`,
					`id="adjust-delta-error" role="alert">` + html.EscapeString(i18n.T(ctx, i18n.KeyAdminStockDeltaError)) + `</p>`,
				} {
					if !strings.Contains(body, want) {
						t.Errorf("adjustment refusal page is missing %q", want)
					}
				}
			})
		}
	}
}
