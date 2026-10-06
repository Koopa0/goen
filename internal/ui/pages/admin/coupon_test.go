package admin

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/coupon"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestCouponStateReflectsRemainingUses(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		row  Coupon
		zh   string
		en   string
		live bool
	}{
		{name: "cap reached", row: Coupon{Active: true, Current: true, MaxRedeem: 1, Redeemed: 1}, zh: "已用完", en: "Used up"},
		{name: "over cap", row: Coupon{Active: true, Current: true, MaxRedeem: 1, Redeemed: 2}, zh: "已用完", en: "Used up"},
		{name: "one use remains", row: Coupon{Active: true, Current: true, MaxRedeem: 2, Redeemed: 1}, zh: "使用中", en: "Live", live: true},
		{name: "no cap", row: Coupon{Active: true, Current: true, Redeemed: 100}, zh: "使用中", en: "Live", live: true},
		{name: "cancelled use released", row: Coupon{Active: true, Current: true, MaxRedeem: 1}, zh: "使用中", en: "Live", live: true},
		{name: "off with cap reached", row: Coupon{Current: true, MaxRedeem: 1, Redeemed: 1}, zh: "已停用", en: "Switched off"},
		{name: "outside window with cap reached", row: Coupon{Active: true, MaxRedeem: 1, Redeemed: 1}, zh: "不在期間內", en: "Outside its window"},
	} {
		for _, locale := range i18n.Locales() {
			t.Run(tt.name+"/"+locale.Tag(), func(t *testing.T) {
				t.Parallel()
				want := tt.en
				if locale == i18n.ZhHant {
					want = tt.zh
				}
				ctx := i18n.WithLocale(t.Context(), locale)
				if got := tt.row.State(ctx); got != want {
					t.Errorf("Coupon.State = %q, want %q", got, want)
				}
				if got := tt.row.Live(); got != tt.live {
					t.Errorf("Coupon.Live = %t, want %t", got, tt.live)
				}
			})
		}
	}
}

func TestCouponTableSeparatesConditionsUsesAndExpiry(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		locale     i18n.Locale
		headers    []string
		conditions string
		used       string
		expiry     string
		state      string
	}{
		{locale: i18n.ZhHant, headers: []string{"條件", "已使用", "到期"}, conditions: "限量 1 · 每位會員 1 次", used: "已使用 1 次", expiry: "至 2027-01-02 11:04", state: "已用完"},
		{locale: i18n.En, headers: []string{"Conditions", "Used", "Expiry"}, conditions: "1 in total · 1 per member", used: "1 used", expiry: "Until 2027-01-02 11:04", state: "Used up"},
	} {
		t.Run(tt.locale.Tag(), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			body := renderComponent(t, ctx, Coupons(layouts.Page{}, CouponsView{Rows: []Coupon{{
				Code: "LIMIT", Kind: coupon.Amount, AmountCents: 10000,
				MaxRedeem: 1, PerCustomer: 1, Redeemed: 1,
				Active: true, Current: true, EndsAt: "2027-01-02 11:04",
			}}}))
			doc, err := html.Parse(strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			headers := couponTableCells(doc, "thead", "th")
			cells := couponTableCells(doc, "tbody", "td")
			if len(headers) != 7 || len(cells) != 7 {
				t.Fatalf("coupon table has %d headers and %d cells, want seven of each", len(headers), len(cells))
			}
			gotHeaders := make([]string, 0, 3)
			for _, header := range headers[2:5] {
				gotHeaders = append(gotHeaders, couponCellText(header))
				if couponAttribute(header, "scope") != "col" {
					t.Error("coupon header lost its column scope")
				}
			}
			if diff := cmp.Diff(tt.headers, gotHeaders); diff != "" {
				t.Errorf("coupon column headers (-want +got):\n%s", diff)
			}
			wantCells := []string{tt.conditions, tt.used, tt.expiry, tt.state}
			gotCells := make([]string, 0, 4)
			for _, cell := range cells[2:6] {
				gotCells = append(gotCells, couponCellText(cell))
			}
			if diff := cmp.Diff(wantCells, gotCells); diff != "" {
				t.Errorf("coupon column values (-want +got):\n%s", diff)
			}
			badgeFound := false
			for node := range cells[5].Descendants() {
				if node.Data == "span" && couponCellText(node) == tt.state {
					badgeFound = true
					if class := couponAttribute(node, "class"); class != "goen-badge" {
						t.Errorf("used-up badge class = %q, want neutral goen-badge", class)
					}
				}
			}
			if !badgeFound {
				t.Error("used-up coupon has no neutral text badge")
			}
		})
	}
}

func couponTableCells(doc *html.Node, section, cell string) []*html.Node {
	var cells []*html.Node
	for node := range doc.Descendants() {
		if node.Type != html.ElementNode || node.Data != section {
			continue
		}
		for child := range node.Descendants() {
			if child.Type == html.ElementNode && child.Data == cell {
				cells = append(cells, child)
			}
		}
	}
	return cells
}

func couponAttribute(node *html.Node, name string) string {
	for _, attr := range node.Attr {
		if attr.Key == name {
			return attr.Val
		}
	}
	return ""
}

func couponCellText(node *html.Node) string {
	var parts []string
	for child := range node.Descendants() {
		if child.Type == html.TextNode {
			parts = append(parts, child.Data)
		}
	}
	return strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
}
