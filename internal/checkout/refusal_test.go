package checkout

import (
	"errors"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/order/delivery"
	"github.com/koopa0/goen/internal/shipping"
)

func hasAttr(n *html.Node, key string) bool {
	return slices.ContainsFunc(n.Attr, func(a html.Attribute) bool { return a.Key == key })
}

func nodeByID(doc *html.Node, id string) *html.Node {
	return findDescendant(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && attrValue(n, "id") == id
	})
}

// assertSummary holds what the refusal alert is made of: the classes that make it an
// error alert, the focus it takes, its heading and the links under it.
func assertSummary(t *testing.T, alert *html.Node, lead string, links []string) {
	t.Helper()
	for _, class := range []string{"ui-alert", "ui-alert--error"} {
		if !hasClass(alert, class) {
			t.Errorf("the alert lacks class %s", class)
		}
	}
	if attrValue(alert, "tabindex") != "-1" || !hasAttr(alert, "autofocus") {
		t.Errorf("the alert does not take focus when the page opens: %v", alert.Attr)
	}
	heading := findDescendant(alert, func(n *html.Node) bool { return hasClass(n, "goen-checkout__refusedlead") })
	if heading == nil {
		t.Fatal("the alert has no heading")
	}
	if got := nodeText(heading); got != lead {
		t.Errorf("the alert is headed %q, want %q", got, lead)
	}
	anchors := descendants(alert, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "a" })
	got := make([]string, 0, len(anchors))
	for _, a := range anchors {
		got = append(got, attrValue(a, "href"))
	}
	if diff := cmp.Diff(links, got); diff != "" {
		t.Errorf("alert links (-want +got):\n%s", diff)
	}
}

// TestPageRefusalSummary: Page leads a refused form with one alert that takes focus
// without a script, counts the refused fields in its heading and links to each of
// them, while every refusal's sentence is on the page once, under its field. The
// discount code is reported under its field alone: the alert never lists it, and
// when the code is the only refusal there is no alert and the code's field takes
// focus instead. A form nobody has refused has neither.
func TestPageRefusalSummary(t *testing.T) {
	t.Parallel()
	const (
		noPhone = "請填寫聯絡電話。"
		noEmail = "請填寫電子郵件。"
		noCode  = "找不到這組折扣碼。"
	)
	for name, tt := range map[string]struct {
		errs      map[string]string
		lead      string
		links     []string
		focusCode bool
	}{
		"unrefused": {},
		"two fields": {
			errs:  map[string]string{"phone": noPhone, "email": noEmail},
			lead:  "請修正 2 個欄位",
			links: []string{"#email", "#phone"},
		},
		"code alone": {
			errs:      map[string]string{"coupon": noCode},
			focusCode: true,
		},
		"code and a field": {
			errs:  map[string]string{"coupon": noCode, "phone": noPhone},
			lead:  "請修正 1 個欄位",
			links: []string{"#phone"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			view := couponTestView()
			view.Errors = tt.errs
			markup := renderToString(t, Page(Meta(i18n.WithLocale(t.Context(), i18n.ZhHant)), &view))
			doc, err := html.Parse(strings.NewReader(markup))
			if err != nil {
				t.Fatal(err)
			}

			alert := nodeByID(doc, "checkout-refusal")
			if (alert != nil) != (tt.links != nil) {
				t.Fatalf("alert present = %v, want %v", alert != nil, tt.links != nil)
			}
			if alert != nil {
				assertSummary(t, alert, tt.lead, tt.links)
			}

			focused := descendants(doc, func(n *html.Node) bool { return n.Type == html.ElementNode && hasAttr(n, "autofocus") })
			want := 0
			if len(tt.errs) > 0 {
				want = 1
			}
			if len(focused) != want {
				t.Errorf("%d elements ask for focus when the page opens, want %d", len(focused), want)
			}
			if tt.focusCode && (len(focused) != 1 || attrValue(focused[0], "id") != "coupon") {
				t.Error("the only refusal is the code, and the code's field does not take focus")
			}
			for _, msg := range tt.errs {
				if n := strings.Count(markup, msg); n != 1 {
					t.Errorf("%q is on the page %d times, want once, under its field", msg, n)
				}
			}
		})
	}

	t.Run("english count", func(t *testing.T) {
		ctx := i18n.WithLocale(t.Context(), i18n.En)
		for n, want := range map[int]string{1: "Fix 1 field", 2: "Fix 2 fields"} {
			if got := i18n.Count(ctx, i18n.KeyCheckoutFixFields, int64(n), n); got != want {
				t.Errorf("Count(%d) = %q, want %q", n, got, want)
			}
		}
	})
}

// TestPageRefusalSummaryLinks: each link of the refusal summary goes to an element the
// page renders when that field is refused, in the form that asks for it. A link to
// an id the page lacks scrolls nowhere and moves no focus.
func TestPageRefusalSummaryLinks(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)

	address := couponTestView()
	address.SavedAddresses = []SavedAddress{{ID: "a1", Name: "王小明", Phone: "0912345678", PostalCode: "110", City: "台北市", District: "信義區", Street: "松高路 1 號"}}
	address.InvoiceChoices = invoiceChoices(ctx)
	pickup := couponTestView()
	pickup.Destination = shipping.DestinationPickupPoint
	pickup.PickupChains = pickupChains()
	pickup.InvoiceChoices = invoiceChoices(ctx)
	pickup.MapOffered = true

	// link renders the page refused for key and returns the document and the summary's link.
	link := func(t *testing.T, view View, key string) (*html.Node, *html.Node) {
		t.Helper()
		view.Errors = map[string]string{key: "x"}
		doc, err := html.Parse(strings.NewReader(renderToString(t, Page(Meta(ctx), &view))))
		if err != nil {
			t.Fatal(err)
		}
		return doc, findDescendant(nodeByID(doc, "checkout-refusal"), func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "a"
		})
	}

	for _, f := range refusedFields {
		t.Run(f.key, func(t *testing.T) {
			t.Parallel()
			view := address
			if strings.HasPrefix(f.key, "pickup_") {
				view = pickup
			}
			doc, a := link(t, view, f.key)
			if a == nil || attrValue(a, "href") != "#"+f.id {
				t.Fatalf("the summary links to %v, want #%s", a, f.id)
			}
			if nodeByID(doc, f.id) == nil {
				t.Errorf("a refused %s is linked to #%s, which the page does not render", f.key, f.id)
			}
			if got := nodeText(a); got != i18n.T(ctx, f.label) {
				t.Errorf("the link reads %q, want the field's name %q", got, i18n.T(ctx, f.label))
			}
		})
	}

	t.Run("store button", func(t *testing.T) {
		t.Parallel()
		doc, a := link(t, pickup, "pickup_store")
		button := nodeByID(doc, "pickup_store")
		if button == nil || button.Data != "button" {
			t.Fatalf("the page has %v at #pickup_store, want the store button", button)
		}
		if a == nil || attrValue(a, "href") != "#pickup_store" {
			t.Errorf("a refused store is linked to %v, want the store button", a)
		}
	})

	t.Run("store without map", func(t *testing.T) {
		t.Parallel()
		view := pickup
		view.MapOffered = false
		doc, a := link(t, view, "pickup_store")
		if a == nil || attrValue(a, "href") != "#pickup_chain" {
			t.Fatalf("without a store button the summary links to %v, want #pickup_chain", a)
		}
		if nodeByID(doc, "pickup_chain") == nil {
			t.Error("a refused store is linked to #pickup_chain, which the page does not render")
		}
	})
}

// TestPagePartialTakesNoFocus: an answer htmx swaps into the page showing asks for focus
// nowhere, whatever it refuses, because htmx would move focus from the control the shopper
// just changed to it. The refusal itself is as visible as on a whole page.
func TestPagePartialTakesNoFocus(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for name, tt := range map[string]struct {
		errs   map[string]string
		linked bool
	}{
		"code alone":       {errs: map[string]string{"coupon": "找不到這組折扣碼。"}},
		"field":            {errs: map[string]string{"phone": "請填寫聯絡電話。"}, linked: true},
		"code and a field": {errs: map[string]string{"coupon": "找不到這組折扣碼。", "phone": "請填寫聯絡電話。"}, linked: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			view := couponTestView()
			view.Errors = tt.errs
			view.Partial = true
			doc, err := html.Parse(strings.NewReader(renderToString(t, Page(Meta(ctx), &view))))
			if err != nil {
				t.Fatal(err)
			}
			focused := descendants(doc, func(n *html.Node) bool { return n.Type == html.ElementNode && hasAttr(n, "autofocus") })
			if len(focused) != 0 {
				t.Errorf("%d elements ask for focus in an answer htmx swaps in, want none", len(focused))
			}
			if got := nodeByID(doc, "checkout-refusal") != nil; got != tt.linked {
				t.Errorf("alert present = %v, want %v", got, tt.linked)
			}
		})
	}
}

// TestView_RefusedFields: RefusedFields lists the refused fields in the order the page
// shows them whatever order they were refused in, and leaves out the discount code.
func TestView_RefusedFields(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := &View{Errors: map[string]string{}, Destination: shipping.DestinationPickupPoint, MapOffered: true}
	for _, f := range slices.Backward(refusedFields) {
		view.Errors[f.key] = "x"
	}
	view.Errors["coupon"] = "x"

	got := view.RefusedFields(ctx)
	if len(got) != len(refusedFields) {
		t.Fatalf("RefusedFields lists %d fields, want %d", len(got), len(refusedFields))
	}
	for i, f := range refusedFields {
		if got[i].ID != f.id || got[i].Label != i18n.T(ctx, f.label) {
			t.Errorf("RefusedFields[%d] = %+v, want #%s %q", i, got[i], f.id, i18n.T(ctx, f.label))
		}
	}
	if (&View{}).RefusedFields(ctx) != nil {
		t.Error("an unrefused form lists refused fields")
	}
}

// TestView_RefusedFieldsCoverErrors: RefusedFields lists every field checkoutErrors can
// refuse, so a refusal is never counted by the page and missing from the summary.
func TestView_RefusedFieldsCoverErrors(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	noShipping := errors.New("no shipping method chosen")
	longNote := strings.Repeat("a", 501)
	for name, tt := range map[string]struct {
		addr *delivery.Details
		err  error
		inv  *Invoice
	}{
		"blank address":      {&delivery.Details{To: shipping.DestinationAddress, Note: longNote}, nil, &Invoice{}},
		"unoffered method":   {&delivery.Details{}, noShipping, &Invoice{}},
		"no chain":           {&delivery.Details{To: shipping.DestinationPickupPoint}, nil, &Invoice{}},
		"no store":           {&delivery.Details{To: shipping.DestinationPickupPoint, PickupChain: shipping.ChainSevenEleven}, nil, &Invoice{}},
		"store without id":   {&delivery.Details{To: shipping.DestinationPickupPoint, PickupChain: shipping.ChainSevenEleven, PickupStoreName: "門市"}, nil, &Invoice{}},
		"store without name": {&delivery.Details{To: shipping.DestinationPickupPoint, PickupChain: shipping.ChainSevenEleven, PickupStoreCode: "012345"}, nil, &Invoice{}},
		"mobile barcode":     {&delivery.Details{}, nil, &Invoice{Type: invoice.PreferenceMobile, MobileBarcode: "x"}},
		"donation code":      {&delivery.Details{}, nil, &Invoice{Type: invoice.PreferenceDonate, DonationCode: "x"}},
		"company":            {&delivery.Details{}, nil, &Invoice{Type: invoice.PreferenceCompany}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			errs := checkoutErrors(ctx, tt.addr, tt.err, tt.inv)
			if len(errs) == 0 {
				t.Fatal("checkoutErrors refused nothing")
			}
			view := &View{Errors: errs}
			if got := len(view.RefusedFields(ctx)); got != len(errs) {
				t.Errorf("RefusedFields lists %d of the %d refused fields %v", got, len(errs), slices.Sorted(maps.Keys(errs)))
			}
		})
	}
}

// TestView_RefusedFieldsCoverPage: RefusedFields lists every field the page can show a
// refusal under, except the discount code, so a field added to the form is added to
// the summary. The page's own source is the list of what it can refuse.
func TestView_RefusedFieldsCoverPage(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("page.templ")
	if err != nil {
		t.Fatal(err)
	}
	named := regexp.MustCompile(`(?:\.(?:Has)?Err|\.Invalid|@field)\("([a-z_]+)"`)
	listed := map[string]bool{"coupon": true}
	for _, f := range refusedFields {
		listed[f.key] = true
	}
	found := map[string]bool{}
	for _, m := range named.FindAllStringSubmatch(string(src), -1) {
		found[m[1]] = true
	}
	for _, key := range slices.Sorted(maps.Keys(found)) {
		if !listed[key] {
			t.Errorf("page.templ shows a refusal under %q, which the refusal summary does not list", key)
		}
	}
	if len(found) < 10 {
		t.Errorf("only %d refusable fields were found in page.templ: the pattern stopped matching", len(found))
	}
}
