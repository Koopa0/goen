package orders

import (
	"fmt"
	"html"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/order/delivery"
	"github.com/koopa0/goen/internal/shipping"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/ui/frame"
)

// TestOrdersEmptyCopy: the empty queue's copy matches the queue context.
func TestOrdersEmptyCopy(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	render := func(v View) string {
		return renderToString(t, Orders(OrdersMeta(ctx), v))
	}

	t.Run("a search miss names the term and not the status filter", func(t *testing.T) {
		t.Parallel()
		body := render(View{Term: "GO-MISSING", Searched: true})
		want := i18n.T(ctx, i18n.KeyAdminQueueNoneFound)
		if !strings.Contains(body, fmt.Sprintf(want, "GO-MISSING")) {
			t.Fatalf("search miss HTML lacks %q", fmt.Sprintf(want, "GO-MISSING"))
		}
		if strings.Contains(body, i18n.T(ctx, i18n.KeyAdminQueueEmpty)) {
			t.Error("a search miss still claims the status filter is empty")
		}
	})

	t.Run("an empty all-orders queue says there are none yet", func(t *testing.T) {
		t.Parallel()
		body := render(View{})
		want := i18n.T(ctx, i18n.KeyAdminQueueNoneYet)
		if !strings.Contains(body, want) {
			t.Fatalf("empty shop HTML lacks %q", want)
		}
		if strings.Contains(body, i18n.T(ctx, i18n.KeyAdminQueueEmpty)) {
			t.Error("an empty shop borrows the status-tab empty copy")
		}
	})

	t.Run("an empty status tab keeps the state-specific copy", func(t *testing.T) {
		t.Parallel()
		body := render(View{Status: "picking"})
		want := i18n.T(ctx, i18n.KeyAdminQueueEmpty)
		if !strings.Contains(body, want) {
			t.Fatalf("empty status tab HTML lacks %q", want)
		}
	})
}

// TestSearchForm_Clear: the queue being searched offers the way back to the whole queue, and the page of an order
// that is not there keeps the number asked for and offers none.
func TestSearchForm_Clear(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	clearLink := `>` + i18n.T(ctx, i18n.KeyAdminQueueClear) + `</a>`
	searched := renderToString(t, Orders(OrdersMeta(ctx), View{Term: "GO-ONE", Searched: true}))
	if !strings.Contains(searched, clearLink) || !strings.Contains(searched, `value="GO-ONE"`) {
		t.Errorf("a searched queue lacks its Clear link or the term: %s", searched)
	}
	if plain := renderToString(t, Orders(OrdersMeta(ctx), View{})); strings.Contains(plain, clearLink) {
		t.Error("a queue nobody searched offers to clear the search")
	}
	missing := renderToString(t, orderNotFound(frame.Page{}, frame.MissingRecordView{Section: "orders", BackLabel: i18n.KeyAdminBackOrders}, "GO-ONE"))
	if strings.Contains(missing, clearLink) || !strings.Contains(missing, `id="order-search"`) || !strings.Contains(missing, `value="GO-ONE"`) {
		t.Errorf("the missing order page must keep the number in the search and offer no Clear: %s", missing)
	}
}

// TestOrderPickupCorrection: a pickup order corrects without a store. It locks
// the shape checkout already writes: the chain is the destination, and the
// store behind it is filled in by the carrier's picker. An order placed before
// one exists carries the chain alone, so the correction form must let a staff
// member save it that way — delivery.Details.Validate refuses one of the two
// and accepts neither.
func TestOrderPickupCorrection(t *testing.T) {
	t.Parallel()
	body := renderToString(t, Order(frame.Page{Title: "GO-PICKUP"}, &OrderView{
		Number:            "GO-PICKUP",
		Correctable:       true,
		PickupDestination: true,
		PickupChains:      shipping.Chains(),
		Address:           (&delivery.Details{PickupChain: shipping.ChainFamilyMart}).Line(),
		Delivery:          Delivery{PickupChain: shipping.ChainFamilyMart},
	}))

	for _, id := range []string{"d-store-code", "d-store-name"} {
		tag := tagWithID(t, body, id)
		if strings.Contains(tag, "required") {
			t.Errorf("the correction form still demands #%s: %s", id, tag)
		}
	}
	if tag := tagWithID(t, body, "d-chain"); !strings.Contains(tag, "required") {
		t.Errorf("the chain stopped being required: %s", tag)
	}

	chain := (&delivery.Details{PickupChain: shipping.ChainFamilyMart}).Line()
	if !strings.Contains(body, chain) {
		t.Errorf("the order detail does not name the chain %q", chain)
	}
	if strings.Contains(body, chain+" ") || strings.Contains(body, chain+"(") {
		t.Errorf("the order detail invents a store beside the chain %q", chain)
	}
}

// tagWithID returns the opening tag carrying id, so a test can ask what
// attributes it holds without parsing the whole document.
func tagWithID(t *testing.T, body, id string) string {
	t.Helper()
	at := strings.Index(body, `id="`+id+`"`)
	if at < 0 {
		t.Fatalf("no element carries id %q", id)
	}
	start := strings.LastIndex(body[:at], "<")
	end := strings.Index(body[at:], ">")
	if start < 0 || end < 0 {
		t.Fatalf("element with id %q is not a tag", id)
	}
	return body[start : at+end+1]
}

// TestOrderTimelineActors: a staff member by name, an erased one as such and
// never as the system, and everyone else by kind; under the list, how long mail
// stays on it.
func TestOrderTimelineActors(t *testing.T) {
	t.Parallel()
	entries := []TimelineEntry{
		{Label: i18n.KeyStatusPlaced, ActorKind: ActorCustomer},
		{Label: i18n.KeyAdminTimelineProvider, Note: "checkout.session.completed", ActorKind: ActorProvider},
		{Label: i18n.KeyStatusPicking, ActorKind: ActorStaff, Actor: "王店長"},
		{Label: i18n.KeyAuditInvoiceAllowance, Status: i18n.KeyAdminTimelineInvoiceAwaitingBuyer, ActorKind: ActorStaff},
		{Label: i18n.KeyAuditInvoiceVoid, Status: i18n.KeyAdminTimelineInvoicePending, ActorKind: ActorSystem},
	}
	for _, tc := range []struct {
		locale  i18n.Locale
		by      []string
		caption string
	}{
		{i18n.ZhHant, []string{"顧客", "金流服務商", "王店長", "已刪除的帳號", "系統"}, "郵件紀錄只保留 30 天，更早的郵件不會列在這裡。"},
		{i18n.En, []string{"Customer", "Payment provider", "王店長", "Erased account", "System"}, "Mail is kept for 30 days; older mail is not listed here."},
	} {
		ctx := i18n.WithLocale(t.Context(), tc.locale)
		body := renderComponent(t, ctx, Order(frame.Page{}, &OrderView{
			Number: "GO-261004-000001", Timeline: entries, MailKept: 30 * 24 * time.Hour,
		}))
		items := strings.Split(body, `class="goen-admin__event"`)[1:]
		if len(items) != len(entries) {
			t.Fatalf("%s: Order renders %d timeline entries, want %d", tc.locale, len(items), len(entries))
		}
		for i, e := range entries {
			item, _, _ := strings.Cut(items[i], "</li>")
			for _, want := range []string{i18n.T(ctx, e.Label), " · " + tc.by[i], e.Note} {
				if !strings.Contains(item, want) {
					t.Errorf("%s: timeline entry %d (%s) lacks %q: %s", tc.locale, i, e.Label, want, item)
				}
			}
			if e.Status != "" && !strings.Contains(item, i18n.T(ctx, e.Status)) {
				t.Errorf("%s: timeline entry %d (%s) lacks its status %q", tc.locale, i, e.Label, i18n.T(ctx, e.Status))
			}
		}
		if !strings.Contains(body, tc.caption) {
			t.Errorf("%s: the timeline does not say %q", tc.locale, tc.caption)
		}
	}
}

// TestOrderView_CanIssueInvoiceCreditPaid: the issue button follows the money.
// A positive sale paid wholly with store credit still owes an invoice before
// picking.
func TestOrderView_CanIssueInvoiceCreditPaid(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		view OrderView
		want bool
	}{
		{name: "committed", view: OrderView{Status: order.FulfillmentPicking, Committed: true, TotalCents: 100000}, want: true},
		{name: "credit paid in full, pending", view: OrderView{Status: order.FulfillmentPending, TotalCents: 100000, CreditCents: 100000}, want: true},
		{name: "zero gross, pending", view: OrderView{Status: order.FulfillmentPending, TotalCents: 0}, want: false},
		{name: "zero gross, committed", view: OrderView{Status: order.FulfillmentPicking, Committed: true, TotalCents: 0}, want: false},
		{name: "shipping remains after goods discount", view: OrderView{Status: order.FulfillmentPicking, Committed: true, TotalCents: 8000}, want: true},
		{name: "still owing", view: OrderView{Status: order.FulfillmentPending, Unpaid: true}, want: false},
		{name: "cancelled", view: OrderView{Status: order.FulfillmentCancelled}, want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.view.InvoicingEnabled = true
			if got := tt.view.CanIssueInvoice(); got != tt.want {
				t.Errorf("CanIssueInvoice = %t, want %t", got, tt.want)
			}
		})
	}
}

// TestOrderUnrecognizedTimelineEntry: the order page shows an unrecognized
// timeline entry. An entry the build cannot label still draws, with its raw
// source, kind and status and its time, once, and the rest of the page is
// there.
func TestOrderUnrecognizedTimelineEntry(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		body := renderOrder(t, locale, &OrderView{
			Number: "GO-261005-000001",
			Timeline: []TimelineEntry{{
				At: "2026-10-05 10:20", Label: i18n.KeyAdminTimelineUnrecognized,
				Unrecognized: "invoice / issue / voided", ActorKind: ActorSystem,
			}},
		})
		for _, want := range []string{
			i18n.T(ctx, i18n.KeyAdminTimelineUnrecognized), "invoice / issue / voided", "2026-10-05 10:20",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: the order page does not carry %q", locale, want)
			}
		}
		if got := strings.Count(body, "invoice / issue / voided"); got != 1 {
			t.Errorf("%s: the order page carries the unrecognized details %d times, want 1", locale, got)
		}
	}
}

// TestOrderTimelineOperationTimes: the timeline shows when an operation was
// created and when it completed. A mail or invoice operation sits at its
// creation but is labelled with where it stands now, so both moments are shown:
// created at one time, and now in a state it reached at another.
func TestOrderTimelineOperationTimes(t *testing.T) {
	t.Parallel()
	entries := []TimelineEntry{
		{At: "2026-10-05 10:00", DoneAt: "2026-10-05 10:20", Label: i18n.KeyAdminTimelineMailPaid,
			Status: i18n.KeyAdminTimelineMailSent, ActorKind: ActorSystem},
		{At: "2026-10-05 10:05", Label: i18n.KeyAdminTimelineMailShipped,
			Status: i18n.KeyAdminTimelineMailQueued, ActorKind: ActorSystem},
		{At: "2026-10-05 10:30", Label: i18n.KeyStatusPicking, ActorKind: ActorStaff, Actor: "王店長"},
	}
	for _, tc := range []struct {
		locale i18n.Locale
		want   [][]string
		absent []string
	}{
		{i18n.ZhHant, [][]string{
			{"2026-10-05 10:00 建立", "目前：已寄出（2026-10-05 10:20）"},
			{"2026-10-05 10:05 建立", "目前：尚未寄出"},
			{"2026-10-05 10:30 · 王店長"},
		}, []string{"目前：尚未寄出（"}},
		{i18n.En, [][]string{
			{"Created 2026-10-05 10:00", "Now: Sent (2026-10-05 10:20)"},
			{"Created 2026-10-05 10:05", "Now: Not sent yet"},
			{"2026-10-05 10:30 · 王店長"},
		}, []string{"Now: Not sent yet ("}},
	} {
		body := renderOrder(t, tc.locale, &OrderView{Number: "GO-261005-000002", Timeline: entries})
		items := strings.Split(body, `class="goen-admin__event"`)[1:]
		if len(items) != len(entries) {
			t.Fatalf("%s: renders %d timeline entries, want %d", tc.locale, len(items), len(entries))
		}
		for i, wants := range tc.want {
			item, _, _ := strings.Cut(items[i], "</li>")
			for _, want := range wants {
				if !strings.Contains(item, want) {
					t.Errorf("%s: timeline entry %d lacks %q: %s", tc.locale, i, want, item)
				}
			}
		}
		for _, bad := range tc.absent {
			if strings.Contains(body, bad) {
				t.Errorf("%s: a state with no completion time shows one: %q", tc.locale, bad)
			}
		}
	}
}

// TestOrderDeliveryErrorsBesideControls: delivery correction errors belong to
// their controls.
func TestOrderDeliveryErrorsBesideControls(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
		for _, tc := range []struct {
			name   string
			pickup bool
			chain  shipping.Chain
			errors map[string]i18n.Key
		}{
			{name: "phone", errors: map[string]i18n.Key{"phone": i18n.KeyPhoneMalformed}},
			{name: "pickup pair", pickup: true, chain: shipping.ChainFamilyMart, errors: map[string]i18n.Key{"pickup_store_name": i18n.KeyAddressIncomplete}},
			{name: "all address fields", errors: map[string]i18n.Key{
				"recipient": i18n.KeyNameRequired, "phone": i18n.KeyPhoneMalformed, "email": i18n.KeyCheckoutEmailMalformed,
				"postal_code": i18n.KeyPostalCodeMalformed, "city": i18n.KeyCityRequired,
				"district": i18n.KeyDistrictRequired, "street": i18n.KeyStreetRequired,
			}},
			{name: "all pickup fields", pickup: true, chain: "unknown", errors: map[string]i18n.Key{
				"recipient": i18n.KeyNameRequired, "phone": i18n.KeyPhoneMalformed, "email": i18n.KeyCheckoutEmailMalformed,
				"pickup_chain": i18n.KeyPickupChainRequired, "pickup_store_code": i18n.KeyStoreCodeMalformed,
				"pickup_store_name": i18n.KeyStoreNameTooLong,
			}},
			{name: "missing chain", pickup: true, errors: map[string]i18n.Key{"pickup_chain": i18n.KeyPickupChainRequired}},
		} {
			t.Run(string(locale)+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				messages := make(map[string]string, len(tc.errors))
				for field, key := range tc.errors {
					messages[field] = i18n.T(ctx, key)
				}
				v := OrderView{
					Number: "GO-DRAFT", Correctable: true, PickupDestination: tc.pickup,
					PickupChains: shipping.Chains(), DeliveryErrors: messages,
					Recipient: "Saved recipient", Phone: "0912345678", Email: "saved@example.com", Address: "Saved destination",
					Delivery: Delivery{Recipient: " Proposed recipient ", Phone: "123", Email: "proposed@example.com",
						PostalCode: "110", City: "New city", District: "New district", Street: " Proposed <street> ",
						PickupChain: tc.chain, PickupStoreCode: "a123", PickupStoreName: ""},
				}
				body := renderComponent(t, ctx, Order(frame.Page{}, &v))
				controls := map[string]struct{ id, value string }{
					"recipient": {"d-recipient", v.Delivery.Recipient}, "phone": {"d-phone", v.Delivery.Phone}, "email": {"d-email", v.Delivery.Email},
				}
				if tc.pickup {
					controls["pickup_store_code"] = struct{ id, value string }{"d-store-code", v.Delivery.PickupStoreCode}
					controls["pickup_store_name"] = struct{ id, value string }{"d-store-name", v.Delivery.PickupStoreName}
					chainTag := tagWithID(t, body, "d-chain")
					if tc.errors["pickup_chain"] != "" && (!strings.Contains(chainTag, `aria-invalid="true"`) || !strings.Contains(chainTag, `aria-describedby="d-chain-error"`)) {
						t.Errorf("refused chain has no own error association: %s", chainTag)
					}
					selected := `<option value="` + html.EscapeString(string(tc.chain)) + `" selected`
					if !strings.Contains(body, selected) {
						t.Errorf("submitted chain %q has no selected option", tc.chain)
					}
				} else {
					for field, control := range map[string]struct{ id, value string }{
						"postal_code": {"d-postal", v.Delivery.PostalCode}, "city": {"d-city", v.Delivery.City},
						"district": {"d-district", v.Delivery.District}, "street": {"d-street", v.Delivery.Street},
					} {
						controls[field] = control
					}
				}
				for field, control := range controls {
					tag := tagWithID(t, body, control.id)
					if !strings.Contains(tag, `value="`+html.EscapeString(control.value)+`"`) {
						t.Errorf("%s lost its submitted value %q: %s", field, control.value, tag)
					}
					if message := messages[field]; message != "" {
						if !strings.Contains(tag, `aria-invalid="true"`) || !strings.Contains(tag, `aria-describedby="`+control.id+`-error"`) {
							t.Errorf("%s has no own error association: %s", field, tag)
						}
						want := `<p id="` + control.id + `-error" class="ui-error-text" role="alert">` + html.EscapeString(message) + `</p>`
						if !strings.Contains(body, want) {
							t.Errorf("%s lacks its error %q", field, want)
						}
					} else if strings.Contains(tag, "aria-invalid") || strings.Contains(tag, "aria-describedby") {
						t.Errorf("valid %s is marked as refused: %s", field, tag)
					}
				}
				for _, saved := range []string{v.Recipient, v.Phone, v.Email, v.Address} {
					if !strings.Contains(body, `<dd class="ui-dl__desc">`+html.EscapeString(saved)+`</dd>`) {
						t.Errorf("saved summary lost %q", saved)
					}
				}
			})
		}
	}
}

// TestOrderTimelineRefusedCodes: the timeline labels refused invoice codes in
// both languages.
func TestOrderTimelineRefusedCodes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		label  i18n.Key
		status i18n.Key
		note   string
		zh     string
		en     string
	}{
		{"issue", i18n.KeyAuditInvoiceIssue, i18n.KeyAdminTimelineInvoiceRejected, "2000006", "綠界代碼 2000006", "ECPay code 2000006"},
		{"allowance", i18n.KeyAuditInvoiceAllowance, i18n.KeyAdminTimelineInvoiceRejected, "2000040", "綠界代碼 2000040", "ECPay code 2000040"},
		{"void", i18n.KeyAuditInvoiceVoid, i18n.KeyAdminTimelineInvoiceRejected, "", "", ""},
		{"local refusal", i18n.KeyAuditInvoiceIssue, i18n.KeyAdminTimelineInvoiceRejected, "", "", ""},
		{"staff note", i18n.KeyStatusPlaced, "", "Keep this note", "Keep this note", "Keep this note"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
				body := renderOrder(t, locale, &OrderView{Number: "GO-261012-000001", Timeline: []TimelineEntry{{
					Label: tc.label, Status: tc.status, Note: tc.note,
				}}})
				want := tc.en
				if locale == i18n.ZhHant {
					want = tc.zh
				}
				if want == "" {
					if strings.Contains(body, "ECPay code") || strings.Contains(body, "綠界代碼") {
						t.Errorf("%s %s HTML invents a provider code", tc.name, locale)
					}
					continue
				}
				noteAt := strings.Index(body, want)
				if noteAt < 0 {
					t.Fatalf("%s %s HTML lacks %q", tc.name, locale, want)
				}
				if tc.status != "" {
					ctx := i18n.WithLocale(t.Context(), locale)
					statusAt := strings.Index(body, i18n.T(ctx, tc.status))
					if statusAt < 0 || statusAt >= noteAt {
						t.Errorf("%s %s HTML does not show refusal before its code", tc.name, locale)
					}
				}
			}
		})
	}
}
