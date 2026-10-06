package admin

import (
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func TestStaffAuditActionsRenderAPhrase(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	for _, action := range []string{"staff.grant", "staff.revoke", "staff.factor.remove"} {
		if got := (AuditEntry{Action: action}).Label(ctx); got == action {
			t.Errorf("Label(%q) = %q; the staff action has no staff-facing phrase", action, got)
		}
	}
}

func TestProductUpdateAuditLabelIsLocalized(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		locale i18n.Locale
		want   string
	}{
		{name: "Traditional Chinese", locale: i18n.ZhHant, want: "修改商品"},
		{name: "English", locale: i18n.En, want: "Edit product"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			if got := (AuditEntry{Action: "product.update"}).Label(ctx); got != tt.want {
				t.Errorf("product.update label = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestASystemIssueIsNotReadAsAnErasedAccount: a system row has no user, which
// is also what an erased staff account leaves, and the two must read apart.
func TestASystemIssueIsNotReadAsAnErasedAccount(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		locale i18n.Locale
		entry  AuditEntry
		want   string
	}{
		{name: "system", locale: i18n.ZhHant, entry: AuditEntry{System: true}, want: "系統"},
		{name: "system in English", locale: i18n.En, entry: AuditEntry{System: true}, want: "System"},
		{name: "erased staff", locale: i18n.ZhHant, entry: AuditEntry{}, want: "已刪除的帳號"},
		{name: "staff", locale: i18n.ZhHant, entry: AuditEntry{Actor: "王店長"}, want: "王店長"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			if got := tt.entry.ActorText(ctx); got != tt.want {
				t.Errorf("ActorText = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTheAuditTrailShowsChangesAsRowsNotJSON(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	html := renderComponent(t, ctx, Audit(layouts.Page{}, AuditView{Rows: []AuditEntry{{
		Action: "order.advance", Entity: "orders", Actor: "staff", At: "2026-10-02 10:00",
		Changes: []AuditChange{{Field: "status", Before: "pending", After: "picking"}},
	}}}))
	for _, want := range []string{"<dt>Status</dt>", "<dd>Awaiting payment → Picking</dd>"} {
		if !strings.Contains(html, want) {
			t.Errorf("audit row is missing %s", want)
		}
	}
	if strings.Contains(html, `{"status"`) || strings.Contains(html, "{&#34;status") {
		t.Error("the audit row prints raw JSON")
	}
}

func TestAuditChangesReadAsWordsAndWholeDollars(t *testing.T) {
	t.Parallel()
	const customerID = "0b6c3a4e-1111-4222-8333-444455556666"
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	html := renderComponent(t, ctx, Audit(layouts.Page{}, AuditView{Rows: []AuditEntry{
		{
			Action: "credit.grant", Entity: "store_credit_entries", Actor: "staff", At: "2026-10-02 10:00",
			Changes: []AuditChange{{Field: "amount_cents", After: "9999900"}},
		},
		{
			Action: "order.ship", Entity: "orders", Actor: "staff", At: "2026-10-02 10:00",
			Changes: []AuditChange{{Field: "carrier", After: "black_cat"}},
		},
		{
			Action: "coupon.create", Entity: "coupons", Actor: "staff", At: "2026-10-02 10:00",
			Changes: []AuditChange{{Field: "kind", After: "amount"}, {Field: "value", After: "50"}},
		},
		{
			Action: "customer.view", Entity: "users", Actor: "staff", At: "2026-10-02 10:00", CustomerName: "王小明", CustomerID: customerID,
			Changes: []AuditChange{{Field: "user_id", After: customerID}},
		},
		{
			Action: "order.advance", Entity: "orders", Actor: "staff", At: "2026-10-02 10:00",
			Changes: []AuditChange{{Field: "odd_key", After: "odd"}},
		},
	}}))
	for _, want := range []string{
		"NT$99,999", "T-CAT (Black Cat)", "NT$50", "Fixed amount",
		`<a href="/admin/customers/` + customerID + `">王小明</a>`,
		"<dt>odd_key</dt>", "<dd>odd</dd>",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("audit page is missing %q", want)
		}
	}
	for _, unwanted := range []string{"_cents", "9999900", "black_cat"} {
		if strings.Contains(html, unwanted) {
			t.Errorf("audit page still prints %q", unwanted)
		}
	}
}

func TestAMoneyRowCarriesAVisibleTag(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.ZhHant)
	html := renderComponent(t, ctx, Audit(layouts.Page{}, AuditView{Rows: []AuditEntry{
		{Action: "credit.grant", Entity: "store_credit_entries", Actor: "staff", Changes: []AuditChange{{Field: "amount_cents", After: "100"}}},
	}}))
	if !strings.Contains(html, ">金額</span>") {
		t.Error("a money row has no visible money tag")
	}
}

func TestAuditEntryReadsEachRecordedKindAsWords(t *testing.T) {
	t.Parallel()
	const customerID = "0b6c3a4e-1111-4222-8333-444455556666"
	for _, tt := range []struct {
		name    string
		locale  i18n.Locale
		entry   AuditEntry
		want    string
		wantNot string
		// wantKey, when set, is the message want is read from, so the case
		// follows the wording rather than a copy of it.
		wantKey i18n.Key
	}{
		{
			name: "return decision", locale: i18n.En,
			entry: AuditEntry{Entity: "return_requests", Changes: []AuditChange{{Field: "decision", After: "approved"}}},
			want:  "<dd>Approved</dd>", wantNot: "<dd>approved</dd>",
		},
		{
			name: "statutory entitlement", locale: i18n.ZhHant,
			entry: AuditEntry{Entity: "return_requests", Changes: []AuditChange{{Field: "entitlement", After: "statutory"}}},
			want:  "<dd>七日猶豫期</dd>", wantNot: "statutory",
		},
		{
			name: "goodwill entitlement", locale: i18n.ZhHant,
			entry: AuditEntry{Entity: "return_requests", Changes: []AuditChange{{Field: "entitlement", After: "goodwill"}}},
			want:  "<dd>店家優惠</dd>", wantNot: "goodwill",
		},
		{
			name: "exception entitlement", locale: i18n.ZhHant,
			entry: AuditEntry{Entity: "return_requests", Changes: []AuditChange{{Field: "entitlement", After: "exception"}}},
			want:  "<dd>人工例外</dd>", wantNot: "exception",
		},
		{
			name: "policy window", locale: i18n.ZhHant,
			entry:   AuditEntry{Entity: "return_requests", Changes: []AuditChange{{Field: "policy_window", After: "goodwill"}}},
			wantKey: i18n.KeyAdminReturnWindowGoodwill, wantNot: "<dd>goodwill</dd>",
		},
		{
			name: "percent coupon", locale: i18n.En,
			entry: AuditEntry{Entity: "coupons", Changes: []AuditChange{{Field: "kind", After: "percent"}, {Field: "value", After: "20"}}},
			want:  "<dd>20%</dd>", wantNot: "NT$",
		},
		{
			name: "order status is a word", locale: i18n.En,
			entry: AuditEntry{Entity: "orders", Changes: []AuditChange{{Field: "status", After: "shipped"}}},
			want:  "<dd>Shipped</dd>", wantNot: "<dd>shipped</dd>",
		},
		{
			name: "product status stays as recorded", locale: i18n.En,
			entry: AuditEntry{Entity: "products", Changes: []AuditChange{{Field: "status", After: "delivered"}}},
			want:  "<dd>delivered</dd>", wantNot: "<dd>Delivered</dd>",
		},
		{
			name: "refund status stays as recorded", locale: i18n.En,
			entry: AuditEntry{Entity: "refunds", Changes: []AuditChange{{Field: "status", After: "delivered"}}},
			want:  "<dd>delivered</dd>", wantNot: "<dd>Delivered</dd>",
		},
		{
			name: "credit returned by a cancellation is whole dollars", locale: i18n.En,
			entry: AuditEntry{Entity: "orders", Changes: []AuditChange{{Field: "credit_returned_cents", After: "500000"}}},
			want:  "<dd>NT$5,000</dd>", wantNot: "credit_returned_cents",
		},
		{
			name: "an erased customer prints the recorded id", locale: i18n.En,
			entry: AuditEntry{Entity: "users", Changes: []AuditChange{{Field: "user_id", After: customerID}}},
			want:  "<dd>" + customerID + "</dd>", wantNot: "/admin/customers/",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tt.locale)
			want := tt.want
			if tt.wantKey != "" {
				want = i18n.T(ctx, tt.wantKey)
			}
			html := renderComponent(t, ctx, Audit(layouts.Page{}, AuditView{Rows: []AuditEntry{tt.entry}}))
			if !strings.Contains(html, want) {
				t.Errorf("audit row is missing %q", want)
			}
			if strings.Contains(html, tt.wantNot) {
				t.Errorf("audit row still carries %q", tt.wantNot)
			}
		})
	}
}

func TestOnlyARowThatRecordsAnAmountSaysMoney(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		entry AuditEntry
		want  bool
	}{
		{"refund before shipment", AuditEntry{Action: "return.refund_before_shipment", Changes: []AuditChange{{Field: "card_refund_cents", After: "100"}}}, true},
		{"amount coupon value", AuditEntry{Entity: "coupons", Action: "coupon.create", Changes: []AuditChange{{Field: "kind", After: "amount"}, {Field: "value", After: "50"}}}, true},
		{"percent coupon value", AuditEntry{Entity: "coupons", Action: "coupon.create", Changes: []AuditChange{{Field: "kind", After: "percent"}, {Field: "value", After: "20"}}}, false},
		{"stock quantity", AuditEntry{Action: "stock.adjust", Changes: []AuditChange{{Field: "delta", After: "3"}}}, false},
		{"declined return", AuditEntry{Action: "return.decide", Changes: []AuditChange{{Field: "decision", After: "rejected"}}}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.entry.Money(); got != tt.want {
				t.Errorf("Money() = %v, want %v", got, tt.want)
			}
			ctx := i18n.WithLocale(t.Context(), i18n.En)
			html := renderComponent(t, ctx, Audit(layouts.Page{}, AuditView{Rows: []AuditEntry{tt.entry}}))
			if got := strings.Contains(html, ">Money<"); got != tt.want {
				t.Errorf("rendered Money tag = %v, want %v", got, tt.want)
			}
		})
	}
}
