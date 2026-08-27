package pages

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

var (
	formControlPattern     = regexp.MustCompile(`<(?:input|select|textarea)\b[^>]*>`)
	quotedAttributePattern = regexp.MustCompile(`\b([[:alnum:]_-]+)="([^"]*)"`)
)

// TestEveryCheckoutFieldTellsTheBrowserWhatItIs derives the controls from the
// rendered forms, then requires an explicit autofill decision for each one.
// This deliberately lives in Go rather than scripts/check-layout.mjs: the
// browser sweep only renders accepted GETs and one claim must not have two
// independently drifting homes.
func TestEveryCheckoutFieldTellsTheBrowserWhatItIs(t *testing.T) {
	t.Parallel()

	want := map[string]string{
		"email":         "email",
		"name":          "name",
		"phone":         "tel",
		"postal_code":   "postal-code",
		"city":          "address-level1",
		"district":      "address-level2",
		"street":        "street-address",
		"addr-name":     "name",
		"addr-phone":    "tel",
		"addr-postal":   "postal-code",
		"addr-city":     "address-level1",
		"addr-district": "address-level2",
		"addr-street":   "street-address",
	}
	offBecause := map[string]string{
		"coupon":            "a promotion code is not the customer's own data; a browser offering the last one is offering somebody else's",
		"invoice_carrier":   "a mobile carrier barcode is not an autofill category",
		"invoice_tax_id":    "no WHATWG token names a Taiwan tax ID",
		"pickup_brand":      "no token names a convenience-store chain, and a wrong chain sends a parcel to the wrong counter",
		"pickup_store_code": "a store, not the customer's address",
		"pickup_store_name": "a store, not the customer's address",
		"note":              "a free-text delivery note; filling a stored address here would be wrong",
		"addr-label":        "the customer's own nickname for the row",
	}

	controls := renderedAutofillControls(t)
	if len(controls) < 10 {
		t.Fatalf("only %d text-entry controls were examined; the rendered-form corpus stopped matching", len(controls))
	}

	var missing []string
	for id, attributes := range controls {
		if token, ok := attributes["autocomplete"]; !ok || strings.TrimSpace(token) == "" {
			missing = append(missing, id)
		}
		if _, named := want[id]; !named {
			if _, explained := offBecause[id]; !explained {
				t.Errorf("%s renders in a checkout or address form but has no named autofill decision", id)
			}
		}
	}
	slices.Sort(missing)
	if len(missing) != 0 {
		t.Errorf("controls with no autocomplete decision: %s", strings.Join(missing, ", "))
	}

	assertAutocompleteTokens(t, controls, want)
	assertAutocompleteOff(t, controls, offBecause)
	assertCheckoutKeyboards(t, controls)
}

// renderedAutofillControls renders every conditional text-entry branch. Radio,
// checkbox, hidden, and submit controls describe state or actions, not data the
// browser can autofill, so they are excluded by kind rather than by id.
func renderedAutofillControls(t *testing.T) map[string]map[string]string {
	t.Helper()

	ctx := t.Context()
	renders := []struct {
		name      string
		component templ.Component
		action    string
		checkout  bool
	}{
		{name: "address", component: Checkout(CheckoutMeta(ctx), &CheckoutView{}), action: "/checkout", checkout: true},
		{name: "pickup", component: Checkout(CheckoutMeta(ctx), &CheckoutView{Destination: "pickup_point"}), action: "/checkout", checkout: true},
		{name: "mobile carrier", component: Checkout(CheckoutMeta(ctx), &CheckoutView{Invoice: CheckoutInvoice{Type: "mobile_carrier"}}), action: "/checkout", checkout: true},
		{name: "company invoice", component: Checkout(CheckoutMeta(ctx), &CheckoutView{Invoice: CheckoutInvoice{Type: "company"}}), action: "/checkout", checkout: true},
		{name: "address book", component: Account(AccountMeta(ctx), &AccountView{}), action: "/account/addresses"},
	}

	controls := map[string]map[string]string{}
	for _, render := range renders {
		html := renderToString(t, render.component)
		form := formWithAction(t, html, render.action)
		for _, tag := range formControlPattern.FindAllString(form, -1) {
			attributes := quotedAttributes(tag)
			if !isTextEntryControl(attributes) {
				continue
			}
			id := attributes["id"]
			if id == "" {
				t.Errorf("%s form renders a text-entry control with no id: %s", render.name, tag)
				continue
			}
			if render.checkout {
				if hint, ok := attributes["enterkeyhint"]; ok {
					t.Errorf("%s carries enterkeyhint=%q; TestEnterInTheCheckoutPlacesTheOrder proves Enter charges, so next/search would lie", id, hint)
				}
			}
			if previous, ok := controls[id]; ok {
				assertStableAutofillAttributes(t, id, previous, attributes)
				continue
			}
			controls[id] = attributes
		}
	}
	return controls
}

func formWithAction(t *testing.T, html, action string) string {
	t.Helper()
	_, after, ok := strings.Cut(html, `action="`+action+`"`)
	if !ok {
		t.Fatalf("rendered page has no form action %q", action)
	}
	form, _, ok := strings.Cut(after, "</form>")
	if !ok {
		t.Fatalf("form action %q has no closing tag", action)
	}
	return form
}

func quotedAttributes(tag string) map[string]string {
	attributes := map[string]string{}
	for _, match := range quotedAttributePattern.FindAllStringSubmatch(tag, -1) {
		attributes[match[1]] = match[2]
	}
	return attributes
}

func isTextEntryControl(attributes map[string]string) bool {
	switch attributes["type"] {
	case "radio", "checkbox", "hidden", "submit", "button", "reset":
		return false
	default:
		return true
	}
}

func assertStableAutofillAttributes(t *testing.T, id string, first, next map[string]string) {
	t.Helper()
	for _, attribute := range []string{"autocomplete", "inputmode", "autocapitalize", "spellcheck"} {
		if first[attribute] != next[attribute] {
			t.Errorf("%s renders %s inconsistently across checkout branches: %q and %q", id, attribute, first[attribute], next[attribute])
		}
	}
}

func assertAutocompleteTokens(t *testing.T, controls map[string]map[string]string, want map[string]string) {
	t.Helper()
	for id, token := range want {
		attributes, seen := controls[id]
		if !seen {
			t.Errorf("token table names %s, which the rendered forms do not contain", id)
			continue
		}
		if got, present := attributes["autocomplete"]; present && got != token {
			t.Errorf("%s autocomplete = %q, want %q", id, got, token)
		}
	}
}

func assertAutocompleteOff(t *testing.T, controls map[string]map[string]string, offBecause map[string]string) {
	t.Helper()
	for id, reason := range offBecause {
		attributes, seen := controls[id]
		if !seen {
			t.Errorf("autocomplete-off table names %s (%s), which the rendered forms do not contain", id, reason)
			continue
		}
		if got, present := attributes["autocomplete"]; present && got != "off" {
			t.Errorf("%s autocomplete = %q, want off: %s", id, got, reason)
		}
	}
}

func assertCheckoutKeyboards(t *testing.T, controls map[string]map[string]string) {
	t.Helper()
	for _, id := range []string{"postal_code", "addr-postal"} {
		if got := controls[id]["inputmode"]; got != "numeric" {
			t.Errorf("%s inputmode = %q, want numeric", id, got)
		}
	}
	if got := controls["phone"]["inputmode"]; got != "" {
		t.Errorf("phone inputmode = %q, want none so +886 remains typeable", got)
	}
	store := controls["pickup_store_code"]
	if got := store["inputmode"]; got != "" {
		t.Errorf("pickup_store_code inputmode = %q, want none: mistake #28 would make 149 Hi-Life letter-leading codes unreachable", got)
	}
	// Only check these after the field has an explicit off decision, so the
	// natural RED reports the root omission once. Later mutations of either
	// keyboard hint still fail by identity.
	if store["autocomplete"] == "off" {
		if got := store["autocapitalize"]; got != "characters" {
			t.Errorf("pickup_store_code autocapitalize = %q, want characters", got)
		}
		if got := store["spellcheck"]; got != "false" {
			t.Errorf("pickup_store_code spellcheck = %q, want false", got)
		}
	}
}
