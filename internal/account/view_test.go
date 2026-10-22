package account

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/frame"
)

// The tests in this file are the account page's halves of tests that render
// several pages: each half lives with the page it renders.

// TestAccountOrderHistoryLinks holds that the account overview sends readers to
// /orders/{number}, not a second detail template.
func TestAccountOrderHistoryLinks(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	view := &View{
		Orders: []AccountOrder{{
			Number: "GO-260101-000012", Status: order.FulfillmentDelivered,
			PlacedAt: shoptime.Date{Year: 2026, Month: time.January, Day: 1}, TotalCents: 106000, LineCount: 1,
		}},
	}
	html := renderToString(t, Account(Meta(ctx), view))
	if !strings.Contains(html, `href="/orders/GO-260101-000012"`) {
		t.Error("the order history does not link to the canonical order page")
	}
	if strings.Contains(html, "/account/orders/") {
		t.Error("the order history still links to the retired account order page")
	}
}

// TestAccountOrder_StatusTextRefunded: an order returned in full is badged
// refunded in the account.
func TestAccountOrder_StatusTextRefunded(t *testing.T) {
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	row := AccountOrder{Status: order.FulfillmentDelivered, Committed: true, Returned: true}
	if got := row.StatusText(ctx); got != "已退款" {
		t.Errorf("AccountOrder{delivered, Returned}.StatusText = %q, want 已退款", got)
	}
}

// TestAccountOrderPayAction holds the 付款 action: only for an order that owes,
// only where payment is possible.
func TestAccountOrderPayAction(t *testing.T) {
	t.Parallel()
	view := func(enabled bool) *View {
		return &View{
			PaymentsEnabled: enabled,
			Orders: []AccountOrder{
				{Number: "GO-260101-000011", Status: order.FulfillmentPending, OwedCents: 100},
				{Number: "GO-260101-000010", Status: order.FulfillmentPending, Committed: true, OwedCents: 100},
			},
		}
	}
	on := renderToString(t, Account(Meta(t.Context()), view(true)))
	if !strings.Contains(on, `href="/orders/GO-260101-000011/pay"`) {
		t.Error("an unpaid order has no 付款 action")
	}
	if strings.Contains(on, `href="/orders/GO-260101-000010/pay"`) {
		t.Error("a paid order is offered payment")
	}
	if off := renderToString(t, Account(Meta(t.Context()), view(false))); strings.Contains(off, "/orders/GO-260101-000011/pay") {
		t.Error("an order is offered payment where the shop takes none")
	}
}

// The history badge's half of internal/order/buyer's
// TestOrderPaidNotAwaitingPayment, which keeps the order
// page's notice.
func TestAccountOrderPaidIsNotAwaitingPayment(t *testing.T) {
	paid := AccountOrder{
		Number: "GO-260101-000010", Status: "pending", PlacedAt: shoptime.Date{Year: 2026, Month: time.January, Day: 1},
		TotalCents: 106000, LineCount: 1, Committed: true, OwedCents: 106000,
	}
	unpaid := AccountOrder{
		Number: "GO-260101-000011", Status: "pending", PlacedAt: shoptime.Date{Year: 2026, Month: time.January, Day: 1},
		TotalCents: 106000, LineCount: 1, Committed: false, OwedCents: 106000,
	}
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	if got := paid.StatusText(ctx); got != "付款完成" {
		t.Errorf("a captured order is badged %q in the history, want 付款完成", got)
	}
	if got := unpaid.StatusText(ctx); got != "待付款" {
		t.Errorf("an unpaid order is badged %q, want 待付款", got)
	}
}

// backOfficeEntrances is every door into /admin the storefront renders, by the
// markup each one produces: the header's drawer below 1024, and the account
// page at every width. A test naming one of them would go green while the other
// disappeared.
//
// The wide bar's action row is deliberately not one of them, and header.templ
// carries the measurement: at 1024 the English header has no room left.
//
// A copy of the map in internal/ui/frame/backoffice_test.go, whose header
// test reads it.
var backOfficeEntrances = map[string]string{
	"the header's drawer":    `<a class="ui-navitem" href="/admin">`,
	"the account page's nav": `<a class="goen-account__navitem" href="/admin">`,
}

// TestAccountBackOfficeStaffOnly holds both halves of the entrance. The back
// office had no link from anywhere on the site and the only way in was typing
// the URL; the fix must not become the opposite defect, which is a customer
// being offered a door that answers 404 — RequireStaff does not disclose that
// /admin exists, so following it is a dead end that also says the shop has
// something to hide.
func TestAccountBackOfficeStaffOnly(t *testing.T) {
	t.Parallel()

	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(locale.Tag(), func(t *testing.T) {
			t.Parallel()
			base := i18n.WithLocale(t.Context(), locale)
			staff := renderAccountPage(t, frame.WithStaff(base, true))

			for where, markup := range backOfficeEntrances {
				if !strings.Contains(staff, markup) {
					t.Errorf("a staff member is offered no back office in %s: no %s in the page",
						where, markup)
				}
			}

			// The label comes off the catalogue, so a staff member reading in
			// English does not meet the one Chinese item on an English page.
			label := i18n.T(base, i18n.KeyBackOffice)
			if !strings.Contains(staff, ">"+label+"<") {
				t.Errorf("the back-office entrance does not render %q in %s", label, locale.Tag())
			}
			if !strings.Contains(staff, i18n.T(base, i18n.KeyBackOfficeHint)) {
				t.Errorf("the account page's back-office entrance renders no hint in %s", locale.Tag())
			}

			// Both shapes of "not staff": the middleware never ran, and it ran
			// and said no. Only the second survives a refactor that starts
			// setting the flag unconditionally.
			for name, ctx := range map[string]context.Context{
				"no middleware": base,
				"not staff":     frame.WithStaff(base, false),
			} {
				customer := renderAccountPage(t, ctx)
				// Without this the assertion below passes on an empty render,
				// which is the false green a page that failed to build gives.
				if !strings.Contains(customer, `href="/account/warranty"`) {
					t.Fatalf("%s: the account page did not render its own nav; the "+
						"assertion below would pass on anything", name)
				}
				if strings.Contains(customer, "/admin") {
					t.Errorf("%s: a customer's page links into the back office", name)
				}
			}
		})
	}
}

func renderAccountPage(t *testing.T, ctx context.Context) string {
	t.Helper()
	return renderComponent(t, ctx, Account(
		Meta(ctx),
		&View{Email: "somebody@example.com"},
	))
}

// The address book's row of internal/checkout's
// TestPageAutofill, with the rows of its tables
// that name the address book's controls and copies of the helpers it calls.
func TestAccountAddressFieldsNameTheirAutofill(t *testing.T) {
	t.Parallel()

	want := map[string]string{
		"addr-name":     "name",
		"addr-phone":    "tel",
		"addr-postal":   "postal-code",
		"addr-city":     "address-level1",
		"addr-district": "address-level2",
		"addr-street":   "street-address",
	}
	offBecause := map[string]string{
		"addr-label": "the customer's own nickname for the row",
	}

	controls := renderedAddressBookControls(t)
	if len(controls) < len(want)+len(offBecause) {
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
	if got := controls["addr-postal"]["inputmode"]; got != "numeric" {
		t.Errorf("addr-postal inputmode = %q, want numeric", got)
	}
}

// renderedAddressBookControls is renderedAutofillControls for the one form here.
func renderedAddressBookControls(t *testing.T) map[string]map[string]string {
	t.Helper()

	controls := map[string]map[string]string{}
	html := renderToString(t, Account(Meta(t.Context()), &View{}))
	form := formWithAction(t, html, "/account/addresses")
	for _, tag := range formControlPattern.FindAllString(form, -1) {
		attributes := quotedAttributes(tag)
		if !isTextEntryControl(attributes) {
			continue
		}
		id := attributes["id"]
		if id == "" {
			t.Errorf("address book form renders a text-entry control with no id: %s", tag)
			continue
		}
		controls[id] = attributes
	}
	return controls
}

var (
	formControlPattern     = regexp.MustCompile(`<(?:input|select|textarea)\b[^>]*>`)
	quotedAttributePattern = regexp.MustCompile(`\b([[:alnum:]_-]+)="([^"]*)"`)
)

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
