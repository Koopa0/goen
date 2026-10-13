package pages

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestAClawbackShowsOnlyTheUnrecoveredShortfall(t *testing.T) {
	entry := PointsEntry{
		Kind: "clawback", Points: -40, RequestedPoints: 100, ShortfallPoints: 60,
	}

	for _, tc := range []struct {
		locale i18n.Locale
		what   string
		detail string
	}{
		{i18n.ZhHant, "退貨扣回", "點數不足，少扣 60 點"},
		{i18n.En, "Reversed for a return", "Not enough points: 60 points could not be reversed"},
	} {
		t.Run(tc.locale.Tag(), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), tc.locale)
			if got := entry.What(ctx); got != tc.what {
				t.Errorf("What() = %q, want %q", got, tc.what)
			}
			if got := entry.Detail(ctx); got != tc.detail {
				t.Errorf("Detail() = %q, want %q", got, tc.detail)
			}
		})
	}

	whollyConsumed := PointsEntry{
		Kind: "clawback", Points: 0, RequestedPoints: 75, ShortfallPoints: 75,
	}
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	if got := whollyConsumed.Detail(ctx); got != "Not enough points: 75 points could not be reversed" {
		t.Errorf("zero-point clawback detail = %q", got)
	}
}

func TestEveryPointsEntryKindHasACompletePresentation(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)

	for _, tt := range []struct {
		kind   PointsEntryKind
		points int64
		earned bool
		amount string
		what   string
	}{
		{kind: "award", points: 12, earned: true, amount: "+12", what: "Earned on a purchase"},
		{kind: "spend", points: -10, amount: "-10", what: "Redeemed for NT$1 store credit"},
		{kind: "clawback", points: -7, amount: "-7", what: "Reversed for a return"},
	} {
		t.Run(string(tt.kind), func(t *testing.T) {
			t.Parallel()
			entry := PointsEntry{Kind: tt.kind, Points: tt.points, CreditCents: 100}
			if got := entry.Earned(); got != tt.earned {
				t.Errorf("Earned() = %v, want %v", got, tt.earned)
			}
			if got := entry.Amount(); got != tt.amount {
				t.Errorf("Amount() = %q, want %q", got, tt.amount)
			}
			if got := entry.What(ctx); got != tt.what {
				t.Errorf("What() = %q, want %q", got, tt.what)
			}
			_ = entry.Detail(ctx)
		})
	}
}

func TestPointsRowsNameTheirOrderDatesAndCredit(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		locale i18n.Locale
		entry  PointsEntry
		want   map[string]string
	}{
		{name: "full reversal zh", locale: i18n.ZhHant, entry: PointsEntry{Kind: PointsClawedBack, Reason: "return", Points: -284, RequestedPoints: 284, Order: "GO-20261005-000003", At: "2026-10-05"}, want: map[string]string{"amount": "-284", "what": "退貨扣回，訂單 GO-20261005-000003", "metadata": "2026-10-05"}},
		{name: "full reversal en", locale: i18n.En, entry: PointsEntry{Kind: PointsClawedBack, Reason: "return", Points: -284, RequestedPoints: 284, Order: "GO-20261005-000003", At: "2026-10-05"}, want: map[string]string{"amount": "-284", "what": "Reversed for a return, order GO-20261005-000003", "metadata": "2026-10-05"}},
		{name: "short reversal zh", locale: i18n.ZhHant, entry: PointsEntry{Kind: PointsClawedBack, Reason: "return", Points: -40, RequestedPoints: 100, ShortfallPoints: 60, Order: "GO-20261005-000003", At: "2026-10-05"}, want: map[string]string{"amount": "-40", "what": "退貨扣回，訂單 GO-20261005-000003", "metadata": "點數不足，少扣 60 點 · 2026-10-05"}},
		{name: "short reversal en", locale: i18n.En, entry: PointsEntry{Kind: PointsClawedBack, Reason: "return", Points: -40, RequestedPoints: 100, ShortfallPoints: 60, Order: "GO-20261005-000003", At: "2026-10-05"}, want: map[string]string{"amount": "-40", "what": "Reversed for a return, order GO-20261005-000003", "metadata": "Not enough points: 60 points could not be reversed · 2026-10-05"}},
		{name: "zero reversal zh", locale: i18n.ZhHant, entry: PointsEntry{Kind: PointsClawedBack, Reason: "return", Points: 0, RequestedPoints: 75, ShortfallPoints: 75, Order: "GO-20261005-000003", At: "2026-10-05"}, want: map[string]string{"amount": "0", "what": "退貨扣回，訂單 GO-20261005-000003", "metadata": "點數不足，少扣 75 點 · 2026-10-05"}},
		{name: "zero reversal en", locale: i18n.En, entry: PointsEntry{Kind: PointsClawedBack, Reason: "return", Points: 0, RequestedPoints: 75, ShortfallPoints: 75, Order: "GO-20261005-000003", At: "2026-10-05"}, want: map[string]string{"amount": "0", "what": "Reversed for a return, order GO-20261005-000003", "metadata": "Not enough points: 75 points could not be reversed · 2026-10-05"}},
		{name: "cancelled reversal zh", locale: i18n.ZhHant, entry: PointsEntry{Kind: PointsClawedBack, Reason: "cancelled", Points: -284, RequestedPoints: 284, Order: "GO-20261005-000003", At: "2026-10-05"}, want: map[string]string{"amount": "-284", "what": "訂單取消扣回，訂單 GO-20261005-000003", "metadata": "2026-10-05"}},
		{name: "cancelled reversal en", locale: i18n.En, entry: PointsEntry{Kind: PointsClawedBack, Reason: "cancelled", Points: -284, RequestedPoints: 284, Order: "GO-20261005-000003", At: "2026-10-05"}, want: map[string]string{"amount": "-284", "what": "Reversed for a cancelled order, order GO-20261005-000003", "metadata": "2026-10-05"}},
		{name: "earned zh", locale: i18n.ZhHant, entry: PointsEntry{Kind: PointsAwarded, Points: 284, Order: "GO-20261005-000003", At: "2026-10-05", ExpiresOn: "2027-10-05"}, want: map[string]string{"amount": "+284", "what": "訂單 GO-20261005-000003", "metadata": "2026-10-05 獲得 · 2027-10-05 到期"}},
		{name: "earned en", locale: i18n.En, entry: PointsEntry{Kind: PointsAwarded, Points: 284, Order: "GO-20261005-000003", At: "2026-10-05", ExpiresOn: "2027-10-05"}, want: map[string]string{"amount": "+284", "what": "Order GO-20261005-000003", "metadata": "earned 2026-10-05 · expires 2027-10-05"}},
		{name: "expired zh", locale: i18n.ZhHant, entry: PointsEntry{Kind: PointsAwarded, Points: 284, At: "2025-10-05", ExpiresOn: "2026-10-05", Expired: true}, want: map[string]string{"amount": "+284", "what": "購物回饋", "metadata": "2025-10-05 獲得 · 已於 2026-10-05 到期"}},
		{name: "expired en", locale: i18n.En, entry: PointsEntry{Kind: PointsAwarded, Points: 284, At: "2025-10-05", ExpiresOn: "2026-10-05", Expired: true}, want: map[string]string{"amount": "+284", "what": "Earned on a purchase", "metadata": "earned 2025-10-05 · expired 2026-10-05"}},
		{name: "redeemed zh", locale: i18n.ZhHant, entry: PointsEntry{Kind: PointsSpent, Points: -100, CreditCents: 1000, At: "2026-10-05"}, want: map[string]string{"amount": "-100", "what": "兌換 NT$10 購物金", "metadata": "2026-10-05"}},
		{name: "redeemed en", locale: i18n.En, entry: PointsEntry{Kind: PointsSpent, Points: -100, CreditCents: 1000, At: "2026-10-05"}, want: map[string]string{"amount": "-100", "what": "Redeemed for NT$10 store credit", "metadata": "2026-10-05"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc := pointsDocument(t, tt.locale, &PointsView{Entries: []PointsEntry{tt.entry}})
			row := findDescendant(doc, func(n *html.Node) bool { return hasClass(n, "goen-points__item") })
			if row == nil {
				t.Fatal("Points() has no ledger row")
			}
			got := map[string]string{}
			for label, class := range map[string]string{"amount": "goen-points__amount", "what": "goen-points__what", "metadata": "goen-points__meta"} {
				got[label] = pointsText(findDescendant(row, func(n *html.Node) bool { return hasClass(n, class) }))
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Points() ledger mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestPointsRuleNamesTheNumericMinimumAndStep(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale i18n.Locale
		want   string
	}{
		{i18n.ZhHant, "最少 100 點，每次以 10 點為單位兌換；換不完的點數會留著。"},
		{i18n.En, "At least 100 points, in whole multiples of 10 points. Whatever is left over stays on your account."},
	} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			doc := pointsDocument(t, tt.locale, &PointsView{Redeemable: 200, Minimum: 100, PerCredit: 10})
			rule := findDescendant(doc, func(n *html.Node) bool { return attrValue(n, "id") == "points-rule" })
			field := findDescendant(doc, func(n *html.Node) bool { return attrValue(n, "id") == "points" })
			got := map[string]string{"rule": pointsText(rule), "described by": attrValue(field, "aria-describedby")}
			want := map[string]string{"rule": tt.want, "described by": "points-rule"}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("Points() field guidance mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func pointsDocument(t *testing.T, locale i18n.Locale, view *PointsView) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(renderIn(t, locale, Points(layouts.Page{}, *view))))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func pointsText(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var collect func(*html.Node)
	collect = func(node *html.Node) {
		if node.Type == html.TextNode {
			b.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			collect(child)
		}
	}
	collect(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

func TestAnUnknownPointsEntryKindIsAProgrammingError(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	entry := PointsEntry{Kind: "future_kind"}

	for name, present := range map[string]func(){
		"Earned": func() { _ = entry.Earned() },
		"Amount": func() { _ = entry.Amount() },
		"What":   func() { _ = entry.What(ctx) },
		"Detail": func() { _ = entry.Detail(ctx) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			defer func() {
				if recover() == nil {
					t.Error("unknown closed ledger kind did not panic")
				}
			}()
			present()
		})
	}
}

func TestPointsLeadUsesTheCurrentExactMemberMultiplier(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale i18n.Locale
		rate   int32
		tier   string
		want   string
	}{
		{i18n.ZhHant, 10000, "", "每消費 NT$100 得 1 點，再乘以你目前的點數倍率（1 倍）。10 點 = NT$1，可以兌換成購物金在結帳時折抵。"},
		{i18n.En, 10000, "", "One point per NT$100 spent, multiplied by your current 1× points rate. 10 points = NT$1, redeemable as store credit at checkout."},
		{i18n.ZhHant, 11000, "銀卡會員", "每消費 NT$100 得 1 點，再乘以你目前的點數倍率（銀卡會員 1.1 倍）。10 點 = NT$1，可以兌換成購物金在結帳時折抵。"},
		{i18n.En, 11000, "Silver", "One point per NT$100 spent, multiplied by your current Silver 1.1× points rate. 10 points = NT$1, redeemable as store credit at checkout."},
		{i18n.En, 12345, "Precise", "One point per NT$100 spent, multiplied by your current Precise 1.2345× points rate. 10 points = NT$1, redeemable as store credit at checkout."},
	} {
		t.Run(tt.locale.Tag()+" "+tt.want, func(t *testing.T) {
			t.Parallel()
			doc := pointsDocument(t, tt.locale, &PointsView{PerCredit: 10, MultiplierBP: tt.rate, TierName: tt.tier})
			lead := findDescendant(doc, func(n *html.Node) bool { return hasClass(n, "goen-pagehead__sub") })
			if got := pointsText(lead); got != tt.want {
				t.Errorf("Points() lead = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPointsFormRefusalPreservesTheAmountAndOperation(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale i18n.Locale
		reason string
	}{
		{i18n.ZhHant, "至少要兌換 100 點，而且要是 10 的倍數。"},
		{i18n.En, "Redeem at least 100 points, in whole multiples of 10."},
	} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			draft := "101"
			view := PointsView{Redeemable: 200, Minimum: 100, PerCredit: 10, DraftPoints: &draft, FieldError: tt.reason, OperationID: "240cc90f-2f42-485c-9cf5-52d21ba4bf75"}
			for _, available := range []int64{200, 0} {
				view.Redeemable = available
				doc := pointsDocument(t, tt.locale, &view)
				field := findDescendant(doc, func(n *html.Node) bool { return attrValue(n, "id") == "points" })
				operation := findDescendant(doc, func(n *html.Node) bool { return attrValue(n, "name") == "operation_id" })
				reason := findDescendant(doc, func(n *html.Node) bool { return attrValue(n, "id") == "points-error" })
				got := map[string]string{"amount": attrValue(field, "value"), "operation": attrValue(operation, "value"), "invalid": attrValue(field, "aria-invalid"), "described by": attrValue(field, "aria-describedby"), "reason": pointsText(reason)}
				want := map[string]string{"amount": "101", "operation": view.OperationID, "invalid": "true", "described by": "points-rule points-error", "reason": tt.reason}
				if diff := cmp.Diff(want, got); diff != "" {
					t.Errorf("Points() refused form mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}
