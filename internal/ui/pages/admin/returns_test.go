package admin

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/returns"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestReturnInspectionCountsRestockedUnitsInBothLanguages(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
		for _, count := range []int32{0, 1, 2, 1000} {
			t.Run(fmt.Sprintf("%s/%d", locale, count), func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				rendered := renderComponent(t, ctx, Returns(layouts.Page{}, ReturnsView{
					Rows: []Return{{ID: "inspected", OrderNumber: "GO-INSPECTED", Status: returns.StatusApproved, Window: "within",
						Lines: []ReturnLine{{OrderLineID: "line", Quantity: count, Inspected: true, Restocked: count}}}},
				}))
				want := fmt.Sprintf("Inspection finished — %d units went back into stock.", count)
				if count == 1 {
					want = "Inspection finished — 1 unit went back into stock."
				}
				if locale == i18n.ZhHant {
					want = fmt.Sprintf("驗貨已完成，共 %d 件回到庫存。", count)
				}
				if !strings.Contains(rendered, want) {
					t.Errorf("inspection notice does not contain %q", want)
				}
			})
		}
	}
}

func TestSellerFactsCountUnitsInBothLanguages(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
		for _, count := range []int64{0, 1, 2, 1000} {
			want := fmt.Sprintf("%d units sold", count)
			if count == 1 {
				want = "1 unit sold"
			}
			if locale == i18n.ZhHant {
				want = fmt.Sprintf("售出 %d 件", count)
			}
			got := (Seller{Units: count}).Facts(i18n.WithLocale(t.Context(), locale))
			if !strings.Contains(got, want) {
				t.Errorf("Facts(%s, %d) = %q, want %q", locale, count, got, want)
			}
		}
	}
}

func TestPartialReturnInspectionsLabelOnlyTheirOwnControls(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(string(locale), func(t *testing.T) {
			t.Parallel()
			rendered := renderComponent(t, i18n.WithLocale(t.Context(), locale), Returns(layouts.Page{}, ReturnsView{
				Rows: []Return{
					{ID: "return-a", OrderNumber: "GO-SHARED", Status: "approved", Decided: true, Window: "within", Units: 1,
						Lines: []ReturnLine{{OrderLineID: "shared-line", Quantity: 1, Restockable: true}}},
					{ID: "return-b", OrderNumber: "GO-SHARED", Status: "approved", Decided: true, Window: "within", Units: 1,
						Lines: []ReturnLine{{OrderLineID: "shared-line", Quantity: 1, Restockable: true}}},
				},
			}))
			document, err := html.Parse(strings.NewReader(rendered))
			if err != nil {
				t.Fatalf("parse returns page: %v", err)
			}
			attribute := func(n *html.Node, name string) string {
				for _, a := range n.Attr {
					if a.Key == name {
						return a.Val
					}
				}
				return ""
			}
			ids := make(map[string]int)
			forms := make(map[string]*html.Node)
			var visit func(*html.Node)
			visit = func(n *html.Node) {
				if id := attribute(n, "id"); id != "" {
					ids[id]++
				}
				if n.Type == html.ElementNode && n.Data == "form" {
					forms[attribute(n, "action")] = n
				}
				for child := n.FirstChild; child != nil; child = child.NextSibling {
					visit(child)
				}
			}
			visit(document)
			for id, count := range ids {
				if count != 1 {
					t.Errorf("document id %q appears %d times, want once", id, count)
				}
			}
			for _, returnID := range []string{"return-a", "return-b"} {
				action := "/admin/returns/" + returnID + "/inspect#inspect-" + returnID
				form := forms[action]
				if form == nil {
					t.Fatalf("inspection form %q is missing", action)
				}
				controls := make(map[string]string)
				labels := make(map[string]int)
				var inspect func(*html.Node)
				inspect = func(n *html.Node) {
					if n.Type == html.ElementNode {
						switch n.Data {
						case "input":
							controls[attribute(n, "id")] = attribute(n, "name")
						case "label":
							labels[attribute(n, "for")]++
						}
					}
					for child := n.FirstChild; child != nil; child = child.NextSibling {
						inspect(child)
					}
				}
				inspect(form)
				for _, field := range []struct{ prefix, name string }{
					{"recv", "received_shared-line"}, {"stock", "restocked_shared-line"}, {"note", "note_shared-line"},
				} {
					id := field.prefix + "-" + returnID + "-shared-line"
					if got := controls[id]; got != field.name {
						t.Errorf("%s control %q name = %q, want %q", action, id, got, field.name)
					}
					if got := labels[id]; got != 1 {
						t.Errorf("%s labels targeting %q = %d, want 1", action, id, got)
					}
					if got := ids[id]; got != 1 {
						t.Errorf("document id %q appears %d times, want once", id, got)
					}
				}
			}
		})
	}
}

func TestPayoutChannelNamesTheFrozenSources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		card   int64
		credit int64
		locale i18n.Locale
		want   string
		not    string
	}{
		{
			name: "card-only Traditional Chinese",
			card: 140000, locale: i18n.ZhHant,
			want: "卡款 NT$1,400 走 Stripe", not: "購物金",
		},
		{
			name: "card-only English",
			card: 140000, locale: i18n.En,
			want: "Card NT$1,400 refunds through Stripe", not: "store credit",
		},
		{
			name:   "credit-only Traditional Chinese",
			credit: 200000, locale: i18n.ZhHant,
			want: "購物金 NT$2,000 退回餘額", not: "Stripe",
		},
		{
			name:   "credit-only English",
			credit: 200000, locale: i18n.En,
			want: "Store credit NT$2,000 returns to the balance", not: "Stripe",
		},
		{
			name: "split Traditional Chinese",
			card: 140000, credit: 60000, locale: i18n.ZhHant,
			want: "卡款 NT$1,400 走 Stripe，購物金 NT$600 退回餘額",
		},
		{
			name: "split English",
			card: 140000, credit: 60000, locale: i18n.En,
			want: "Card NT$1,400 through Stripe, store credit NT$600 back to the balance",
		},
		{
			name:   "open request has no frozen channel",
			locale: i18n.ZhHant,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			row := Return{CardRefundCents: tt.card, CreditRefundCents: tt.credit}
			got := row.PayoutChannel(ctx)
			if got != tt.want {
				t.Errorf("PayoutChannel() = %q, want %q", got, tt.want)
			}
			if tt.not != "" && strings.Contains(got, tt.not) {
				t.Errorf("PayoutChannel() = %q, must not mention %q", got, tt.not)
			}
		})
	}
}

func TestTheReturnQueueHTMLNamesTheRefundChannels(t *testing.T) {
	t.Parallel()

	render := func(t *testing.T, locale i18n.Locale, row Return) string {
		t.Helper()
		var b strings.Builder
		if err := Returns(layouts.Page{Title: "退貨"}, ReturnsView{
			Rows: []Return{row},
		}).Render(i18n.WithLocale(t.Context(), locale), &b); err != nil {
			t.Fatalf("render: %v", err)
		}
		return b.String()
	}

	t.Run("credit-only English names the ledger, not Stripe", func(t *testing.T) {
		t.Parallel()
		page := render(t, i18n.En, Return{
			ID: "credit-row", OrderNumber: "GO-CREDIT", Status: "approved",
			StatusText: "Approved", Window: "within", Decided: true,
			CreditRefundCents: 200000,
		})
		want := i18n.T(i18n.WithLocale(t.Context(), i18n.En), i18n.KeyAdminRetPayoutCredit)
		want = strings.ReplaceAll(want, "%s", "NT$2,000")
		if !strings.Contains(page, want) {
			t.Errorf("credit-only HTML lacks %q", want)
		}
		if strings.Contains(page, "refunds through Stripe") {
			t.Error("a credit-only return is described as a Stripe refund")
		}
	})

	t.Run("split Traditional Chinese names both sources", func(t *testing.T) {
		t.Parallel()
		page := render(t, i18n.ZhHant, Return{
			ID: "split-row", OrderNumber: "GO-SPLIT", Status: "approved",
			StatusText: "已同意", Window: "within", Decided: true,
			CardRefundCents: 140000, CreditRefundCents: 60000,
		})
		want := "卡款 NT$1,400 走 Stripe，購物金 NT$600 退回餘額"
		if !strings.Contains(page, want) {
			t.Errorf("split HTML lacks %q", want)
		}
	})

	t.Run("an open request does not invent a channel", func(t *testing.T) {
		t.Parallel()
		page := render(t, i18n.ZhHant, Return{
			ID: "open-row", OrderNumber: "GO-OPEN", Status: "requested",
			StatusText: "待處理", Window: "within",
		})
		for _, frozen := range []string{
			"卡款 NT$", "購物金 NT$",
			i18n.T(i18n.WithLocale(t.Context(), i18n.ZhHant), i18n.KeyAdminRetPayoutCard),
			i18n.T(i18n.WithLocale(t.Context(), i18n.ZhHant), i18n.KeyAdminRetPayoutCredit),
		} {
			if strings.Contains(page, frozen) {
				t.Errorf("an open request shows a frozen payout channel %q", frozen)
			}
		}
		lead := i18n.T(i18n.WithLocale(t.Context(), i18n.ZhHant), i18n.KeyAdminRetLead)
		if !strings.Contains(page, lead) {
			t.Error("the page lead is absent")
		}
	})
}

func TestEveryReturnPolicyWindowHasALabel(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		for _, window := range []returns.PolicyWindow{returns.WindowStatutory, returns.WindowGoodwill, returns.WindowLate, returns.WindowUndelivered, returns.WindowMixed} {
			label := ReturnLineWindowText(ctx, window)
			if label == "" || label == string(window) {
				t.Errorf("WindowText(%q) in %s = %q, want a catalogue label", window, locale, label)
			}
		}
	}
}

func TestAnApprovedReturnWithMoneyOutstandingOffersToSendItAgain(t *testing.T) {
	base := func(id string) Return {
		return Return{
			ID: id, OrderNumber: "GO-TEST", Status: "approved", StatusText: "已同意",
			Reason: "不合用", Window: "within",
		}
	}
	render := func(t *testing.T, row Return) string {
		t.Helper()
		return renderToString(t, Returns(layouts.Page{Title: "退貨"}, ReturnsView{
			Rows: []Return{row},
		}))
	}

	t.Run("each policy window names itself without claiming a missing fact", func(t *testing.T) {
		ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
		tests := []struct {
			window string
			want   []string
			hide   []string
		}{
			{
				window: "within",
				want:   []string{i18n.T(ctx, i18n.KeyAdminReturnWindowWithin), i18n.T(ctx, i18n.KeyAdminRetMustAccept)},
				hide:   []string{i18n.T(ctx, i18n.KeyAdminRetGoodwillHint), i18n.T(ctx, i18n.KeyAdminRetLateHint)},
			},
			{
				window: "goodwill",
				want:   []string{i18n.T(ctx, i18n.KeyAdminReturnWindowGoodwill), i18n.T(ctx, i18n.KeyAdminRetGoodwillHint)},
				// Days 8–14 are a conditional offer; the late-window exception
				// sentence must not appear on the hint.
				hide: []string{i18n.T(ctx, i18n.KeyAdminRetMustAccept), i18n.T(ctx, i18n.KeyAdminRetLateHint)},
			},
			{
				window: "after",
				want:   []string{i18n.T(ctx, i18n.KeyAdminReturnWindowAfter), i18n.T(ctx, i18n.KeyAdminRetLateHint)},
				hide:   []string{i18n.T(ctx, i18n.KeyAdminRetMustAccept), i18n.T(ctx, i18n.KeyAdminRetGoodwillHint)},
			},
			{
				window: "undelivered",
				want:   []string{i18n.T(ctx, i18n.KeyAdminReturnWindowUndelivered)},
				hide:   []string{i18n.T(ctx, i18n.KeyAdminRetMustAccept), i18n.T(ctx, i18n.KeyAdminRetLateHint)},
			},
			{
				window: "mixed",
				want:   []string{i18n.T(ctx, i18n.KeyAdminReturnWindowMixed), i18n.T(ctx, i18n.KeyAdminRetGoodwillHint)},
				hide:   []string{i18n.T(ctx, i18n.KeyAdminRetMustAccept), i18n.T(ctx, i18n.KeyAdminRetLateHint)},
			},
		}
		for _, tt := range tests {
			t.Run(tt.window, func(t *testing.T) {
				row := base("window-" + tt.window)
				row.Status = "requested"
				row.Decided = false
				row.Window = returns.PolicyWindow(tt.window)
				page := render(t, row)
				for _, want := range tt.want {
					if !strings.Contains(page, want) {
						t.Errorf("window %s HTML lacks %q", tt.window, want)
					}
				}
				for _, hide := range tt.hide {
					if strings.Contains(page, hide) {
						t.Errorf("window %s HTML still claims %q", tt.window, hide)
					}
				}
			})
		}
	})

	t.Run("an open statutory request offers approval without a reject escape", func(t *testing.T) {
		row := base("open-row")
		row.Status = "requested"
		row.Decided = false
		page := render(t, row)
		for _, want := range []string{
			`action="/admin/returns/open-row/decide"`, `value="approved"`,
			`name="assessment_version"`, `name="resolution"`, `maxlength="300"`,
		} {
			if !strings.Contains(page, want) {
				t.Errorf("open request HTML lacks %s", want)
			}
		}
		for _, hide := range []string{
			`name="rejection_ground"`, `value="missing_reason"`, `value="ineligible"`,
			`value="rejected"`,
		} {
			if strings.Contains(page, hide) {
				t.Errorf("statutory form still offers %s", hide)
			}
		}
	})

	t.Run("a goodwill request offers assessment and the three verbs", func(t *testing.T) {
		row := base("goodwill-row")
		row.Status = "requested"
		row.Decided = false
		row.Window = "goodwill"
		row.Lines = []ReturnLine{{
			OrderLineID: "line-1", Name: "測試", Quantity: 1, SKU: "SKU-1",
		}}
		page := render(t, row)
		for _, want := range []string{
			`action="/admin/returns/goodwill-row/assess"`,
			`name="unused_line-1"`, `name="packaging_line-1"`, `name="accessories_line-1"`,
			`value="unknown"`, `value="met"`, `value="unmet"`,
			`name="basis"`, `value="approved"`, `value="rejected"`, `value="exception"`,
		} {
			if !strings.Contains(page, want) {
				t.Errorf("goodwill HTML lacks %s", want)
			}
		}
	})

	t.Run("an outstanding payout offers only the retry", func(t *testing.T) {
		row := base("retry-row")
		row.Decided = true
		row.PayoutOutstanding = true
		page := render(t, row)
		for _, want := range []string{
			`method="post"`, `action="/admin/returns/retry-row/decide"`,
			`name="decision"`, `value="approved"`, i18n.T(i18n.WithLocale(t.Context(), i18n.ZhHant), i18n.KeyAdminRetRetryPayout),
		} {
			if !strings.Contains(page, want) {
				t.Errorf("retry HTML lacks %s", want)
			}
		}
		if strings.Contains(page, `value="rejected"`) {
			t.Error("a payout retry offers to retake the rejection decision")
		}
		if strings.Contains(page, `name="resolution"`) {
			t.Error("a payout retry asks for a new decision note which cannot change the approved claim")
		}
	})

	t.Run("a refused exception keeps the typed reason and marks the field", func(t *testing.T) {
		row := base("except-row")
		row.Status = "requested"
		row.Decided = false
		row.Window = "after"
		row.Resolution = "   "
		page := renderToString(t, Returns(layouts.Page{Title: "退貨"}, ReturnsView{
			Rows:   []Return{row},
			Errors: map[string]string{"except-row.resolution": "need a reason"},
		}))
		if !strings.Contains(page, `value="   "`) {
			t.Error("422 dropped the typed whitespace reason")
		}
		if !strings.Contains(page, `id="res-except-row"`) || !strings.Contains(page, `aria-invalid="true"`) {
			t.Error("422 did not mark the resolution field")
		}
		if !strings.Contains(page, "need a reason") {
			t.Error("422 hid the field-specific refusal")
		}
	})

	t.Run("a settled payout has no decision action", func(t *testing.T) {
		row := base("settled-row")
		row.Decided = true
		page := render(t, row)
		if strings.Contains(page, `/admin/returns/settled-row/decide`) {
			t.Error("a settled return still offers a decision or payout action")
		}
	})

	t.Run("an inconsistent payout is explained without an unsafe button", func(t *testing.T) {
		row := base("stranded-row")
		row.Decided = true
		row.PayoutOutstanding = true
		row.PayoutBlocked = true
		page := render(t, row)
		stranded := i18n.T(i18n.WithLocale(t.Context(), i18n.ZhHant), i18n.KeyAdminRetPayoutStranded)
		if !strings.Contains(page, stranded) {
			t.Error("the inconsistent-payout explanation is absent")
		}
		if strings.Contains(page, `/admin/returns/stranded-row/decide`) {
			t.Error("an inconsistent payout offers a retry that cannot be proven safe")
		}
	})
}

// TestEachReturnDecisionPostsItsOwnAnswer locks what the three buttons submit.
// They are one form with one field, and the answer is carried by the button
// pressed, so a re-skin that drops a name or a value would send every decision
// as the same one.
func TestEachReturnDecisionPostsItsOwnAnswer(t *testing.T) {
	t.Parallel()
	page := renderToString(t, Returns(layouts.Page{Title: "退貨"}, ReturnsView{
		Rows: []Return{{
			ID: "open-row", OrderNumber: "GO-OPEN", Status: "requested",
			StatusText: "待處理", Window: "goodwill", Reason: "不合用",
		}},
	}))

	for _, want := range []string{
		`name="decision" value="approved"`,
		`name="decision" value="exception"`,
		`name="decision" value="rejected"`,
		`name="assessment_version"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the decision form no longer carries %s", want)
		}
	}
}

func TestEveryReturnRowLinksItsOrderAndOnlyShippedGoodsCanBeShort(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	line := ReturnLine{Name: "耳機", Quantity: 1, Inspected: true, Received: 0}
	view := ReturnsView{Rows: []Return{
		{ID: "shipped", OrderNumber: "GO-260930-000012", Window: "goodwill", Lines: []ReturnLine{line}},
		{ID: "unshipped", OrderNumber: "GO-260930-000011", Window: "goodwill", BeforeShipment: true, Lines: []ReturnLine{line}},
	}}
	page := renderComponent(t, ctx, Returns(layouts.Page{}, view))

	for _, number := range []string{"GO-260930-000012", "GO-260930-000011"} {
		if !strings.Contains(page, `<a href="/admin/orders/`+number+`">`+number+`</a>`) {
			t.Errorf("the row for %s does not link to its order", number)
		}
	}
	if got := strings.Count(page, i18n.T(ctx, i18n.KeyAdminRetShortfall)); got != 1 {
		t.Errorf("%q appears %d times, want once: shipped goods can be short, a refund before shipment cannot", i18n.T(ctx, i18n.KeyAdminRetShortfall), got)
	}
}
