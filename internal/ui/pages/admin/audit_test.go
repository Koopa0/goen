package admin

import (
	"go/ast"
	"go/parser"
	"go/token"
	stdhtml "html"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode"

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
		"<dt>This record includes the field “odd_key”.</dt>", "<dd>odd</dd>",
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

func TestAuditRecordedFieldLabels(t *testing.T) {
	t.Parallel()
	// These literal pairs follow recorded producer payloads, independently of
	// the label registry, including keys whose meaning depends on the entity.
	fields := []struct {
		entity string
		field  string
		zh     string
		en     string
	}{
		{entity: "sale_campaigns", field: "slug", zh: "網址代稱", en: "Slug"},
		{entity: "sale_campaigns", field: "starts_at", zh: "開始時間", en: "Starts"},
		{entity: "sale_campaigns", field: "ends_at", zh: "結束時間", en: "Ends"},
		{entity: "sale_campaigns", field: "campaign", zh: "活動", en: "Campaign"},
		{entity: "sale_campaigns", field: "tone", zh: "色調", en: "Tone"},
		{entity: "sale_campaigns", field: "digest", zh: "圖片識別碼", en: "Image fingerprint"},
		{entity: "sale_campaigns", field: "alt", zh: "圖片替代文字", en: "Image alternative text"},
		{entity: "sale_campaigns", field: "title", zh: "活動標題", en: "Campaign title"},
		{entity: "sale_campaigns", field: "title_en", zh: "活動標題（英文）", en: "Campaign title (English)"},
		{entity: "sale_campaigns", field: "days", zh: "活動天數", en: "Days it runs"},
		{entity: "sale_campaigns", field: "active", zh: "啟用狀態", en: "Active state"},
		{entity: "sale_campaign_products", field: "campaign", zh: "活動", en: "Campaign"},
		{entity: "sale_campaign_products", field: "product", zh: "商品", en: "Product"},
		{entity: "promo_banners", field: "message", zh: "訊息內容", en: "Message"},
		{entity: "promo_banners", field: "cta", zh: "按鈕連結", en: "Button link"},
		{entity: "promo_banners", field: "active", zh: "啟用狀態", en: "Active state"},
		{entity: "faq_entries", field: "category", zh: "分類", en: "Category"},
		{entity: "faq_entries", field: "question", zh: "問題", en: "Question"},
		{entity: "faq_entries", field: "id", zh: "記錄編號", en: "Record ID"},
		{entity: "hero_slides", field: "headline", zh: "標題", en: "Headline"},
		{entity: "hero_slides", field: "cta", zh: "按鈕連結", en: "Button link"},
		{entity: "hero_slides", field: "active", zh: "啟用狀態", en: "Active state"},
		{entity: "coupons", field: "code", zh: "代碼", en: "Code"},
		{entity: "coupons", field: "kind", zh: "類型", en: "Type"},
		{entity: "coupons", field: "value", zh: "折抵", en: "Discount"},
		{entity: "coupons", field: "active", zh: "啟用狀態", en: "Active state"},
		{entity: "users", field: "user_id", zh: "顧客", en: "Customer"},
		{entity: "users", field: "email", zh: "電子郵件", en: "Email"},
		{entity: "users", field: "role", zh: "角色", en: "Role"},
		{entity: "contact_messages", field: "message_id", zh: "留言編號", en: "Message ID"},
		{entity: "contact_messages", field: "handled", zh: "已處理狀態", en: "Handled state"},
		{entity: "product_questions", field: "hidden", zh: "隱藏狀態", en: "Hidden state"},
		{entity: "product_answers", field: "length", zh: "回覆字數", en: "Answer length in characters"},
		{entity: "product_answers", field: "question_id", zh: "提問編號", en: "Question ID"},
		{entity: "product_answers", field: "hidden", zh: "隱藏狀態", en: "Hidden state"},
		{entity: "product_reviews", field: "review_id", zh: "評價編號", en: "Review ID"},
		{entity: "product_reviews", field: "hidden", zh: "隱藏狀態", en: "Hidden state"},
		{entity: "payment_webhook_events", field: "event", zh: "事件編號", en: "Event"},
		{entity: "payment_webhook_events", field: "resolution", zh: "處理結果", en: "Resolution"},
		{entity: "payments", field: "provider_ref", zh: "金流端編號", en: "Provider reference"},
		{entity: "payments", field: "resolution", zh: "處理結果", en: "Resolution"},
		{entity: "store_credit_entries", field: "amount_cents", zh: "金額", en: "Amount"},
		{entity: "store_credit_entries", field: "reason", zh: "原因", en: "Reason"},
		{entity: "membership_tiers", field: "code", zh: "代碼", en: "Code"},
		{entity: "membership_tiers", field: "name", zh: "名稱", en: "Name"},
		{entity: "membership_tiers", field: "name_en", zh: "名稱（英文）", en: "Name (English)"},
		{entity: "membership_tiers", field: "min_spend_cents", zh: "門檻", en: "Threshold"},
		{entity: "membership_tiers", field: "multiplier_bp", zh: "點數倍率（基點）", en: "Points rate (basis points)"},
		{entity: "membership_tiers", field: "id", zh: "記錄編號", en: "Record ID"},
		{entity: "orders", field: "number", zh: "訂單編號", en: "Order number"},
		{entity: "orders", field: "status", zh: "狀態", en: "Status"},
		{entity: "orders", field: "carrier", zh: "物流商", en: "Carrier"},
		{entity: "orders", field: "tracking", zh: "查詢編號", en: "Tracking number"},
		{entity: "orders", field: "return_request_id", zh: "退貨申請編號", en: "Return request ID"},
		{entity: "orders", field: "note", zh: "內部備註", en: "Internal note"},
		{entity: "orders", field: "credit_returned_cents", zh: "退回購物金", en: "Refunded to store credit"},
		{entity: "order_private_data", field: "order_number", zh: "訂單編號", en: "Order number"},
		{entity: "order_private_data", field: "destination", zh: "收件資訊", en: "Delivery details"},
		{entity: "products", field: "name", zh: "名稱", en: "Name"},
		{entity: "products", field: "slug", zh: "網址代稱", en: "Slug"},
		{entity: "products", field: "brand_id", zh: "品牌", en: "Brand"},
		{entity: "products", field: "category_id", zh: "分類", en: "Category"},
		{entity: "products", field: "warranty_months", zh: "保固月數", en: "Warranty term (months)"},
		{entity: "products", field: "status", zh: "狀態", en: "Status"},
		{entity: "products", field: "origin", zh: "產地", en: "Origin"},
		{entity: "products", field: "origin_en", zh: "產地（英文）", en: "Origin (English)"},
		{entity: "products", field: "domestic_party_name", zh: "國內負責廠商名稱", en: "Domestic responsible party name"},
		{entity: "products", field: "domestic_party_phone", zh: "國內負責廠商電話", en: "Domestic responsible party phone"},
		{entity: "products", field: "domestic_party_address", zh: "國內負責廠商地址", en: "Domestic responsible party address"},
		{entity: "products", field: "net_quantity", zh: "淨含量數值", en: "Net quantity"},
		{entity: "products", field: "net_unit", zh: "淨含量單位", en: "Net unit"},
		{entity: "products", field: "min_age_months", zh: "最低適用月齡", en: "Minimum age in months"},
		{entity: "products", field: "tax_type", zh: "課稅別", en: "Tax type"},
		{entity: "products", field: "invoice_unit", zh: "發票單位", en: "Invoice unit"},
		{entity: "product_specs", field: "slug", zh: "網址代稱", en: "Slug"},
		{entity: "product_specs", field: "label", zh: "項目", en: "Label"},
		{entity: "product_options", field: "slug", zh: "網址代稱", en: "Slug"},
		{entity: "product_options", field: "name", zh: "名稱", en: "Name"},
		{entity: "product_option_values", field: "slug", zh: "網址代稱", en: "Slug"},
		{entity: "product_option_values", field: "value", zh: "規格選項值", en: "Option value"},
		{entity: "product_images", field: "product", zh: "商品", en: "Product"},
		{entity: "product_images", field: "digest", zh: "圖片識別碼", en: "Image fingerprint"},
		{entity: "product_images", field: "alt", zh: "圖片替代文字", en: "Image alternative text"},
		{entity: "product_images", field: "option_value", zh: "圖片對應選項", en: "Image option value"},
		{entity: "product_images", field: "move", zh: "圖片移動方向", en: "Image move direction"},
		{entity: "product_images", field: "order", zh: "圖片排列順序", en: "Image order"},
		{entity: "product_variants", field: "product", zh: "商品", en: "Product"},
		{entity: "product_variants", field: "sku", zh: "商品規格編號", en: "SKU"},
		{entity: "product_variants", field: "price_cents", zh: "售價", en: "Selling price"},
		{entity: "product_variants", field: "stock", zh: "庫存", en: "Stock"},
		{entity: "product_variants", field: "delta", zh: "庫存調整數量", en: "Stock quantity change"},
		{entity: "product_variants", field: "received", zh: "進貨數量", en: "Received quantity"},
		{entity: "product_variants", field: "preorder_release_on", zh: "預計到貨日", en: "Expected arrival date"},
		{entity: "product_variants", field: "active", zh: "啟用狀態", en: "Active state"},
		{entity: "product_variants", field: "compare_at_cents", zh: "原價", en: "Compare-at price"},
		{entity: "return_requests", field: "decision", zh: "決定", en: "Decision"},
		{entity: "return_requests", field: "resolution", zh: "處理說明", en: "Resolution note"},
		{entity: "return_requests", field: "policy_window", zh: "退貨期限", en: "Return window"},
		{entity: "return_requests", field: "entitlement", zh: "退貨依據", en: "Basis for the return"},
		{entity: "return_requests", field: "assessment_version", zh: "退貨評估版本", en: "Return assessment version"},
		{entity: "return_requests", field: "lines", zh: "驗收品項數", en: "Inspected item count"},
		{entity: "return_requests", field: "restocked", zh: "回補庫存品項數", en: "Restocked item count"},
		{entity: "return_requests", field: "order_number", zh: "訂單編號", en: "Order number"},
		{entity: "return_requests", field: "card_refund_cents", zh: "退回信用卡", en: "Refunded to card"},
		{entity: "return_requests", field: "credit_refund_cents", zh: "退回購物金", en: "Refunded to store credit"},
		{entity: "shipping_method_versions", field: "method_id", zh: "配送方式編號", en: "Delivery method ID"},
		{entity: "shipping_method_versions", field: "name", zh: "名稱", en: "Name"},
		{entity: "shipping_method_versions", field: "carrier", zh: "物流商", en: "Carrier"},
		{entity: "shipping_method_versions", field: "name_en", zh: "名稱（英文）", en: "Name (English)"},
		{entity: "shipping_method_versions", field: "carrier_en", zh: "物流商（英文）", en: "Carrier (English)"},
		{entity: "shipping_method_versions", field: "fee_cents", zh: "運費", en: "Delivery fee"},
		{entity: "shipping_method_versions", field: "free_over_cents", zh: "免運門檻", en: "Free-delivery threshold"},
		{entity: "shipping_version_zones", field: "version_id", zh: "運費版本編號", en: "Delivery fee version ID"},
		{entity: "shipping_version_zones", field: "zone_id", zh: "配送區域編號", en: "Delivery zone ID"},
		{entity: "shipping_version_zones", field: "surcharge_cents", zh: "分區加價", en: "Zone surcharge"},
		{entity: "shipping_methods", field: "code", zh: "代碼", en: "Code"},
		{entity: "shipping_methods", field: "destination_kind", zh: "收件方式", en: "Delivery destination type"},
		{entity: "shipping_methods", field: "name", zh: "名稱", en: "Name"},
		{entity: "shipping_methods", field: "fee_cents", zh: "運費", en: "Delivery fee"},
		{entity: "shipping_methods", field: "active", zh: "啟用狀態", en: "Active state"},
		{entity: "shipping_zones", field: "code", zh: "代碼", en: "Code"},
		{entity: "shipping_zones", field: "name", zh: "名稱", en: "Name"},
		{entity: "shipping_zones", field: "prefixes", zh: "郵遞區號數", en: "Postal-code count"},
		{entity: "shipping_zone_prefixes", field: "prefixes", zh: "郵遞區號數", en: "Postal-code count"},
		{entity: "brands", field: "slug", zh: "網址代稱", en: "Slug"},
		{entity: "brands", field: "name", zh: "名稱", en: "Name"},
		{entity: "categories", field: "slug", zh: "網址代稱", en: "Slug"},
		{entity: "categories", field: "name", zh: "名稱", en: "Name"},
		{entity: "categories", field: "name_en", zh: "名稱（英文）", en: "Name (English)"},
		{entity: "categories", field: "icon_key", zh: "圖示", en: "Icon"},
		{entity: "categories", field: "tone", zh: "色調", en: "Tone"},
		{entity: "categories", field: "comparable", zh: "開放商品比較", en: "Offer comparison"},
		{entity: "categories", field: "parent", zh: "上層分類", en: "Parent category"},
		{entity: "categories", field: "category", zh: "分類", en: "Category"},
		{entity: "categories", field: "digest", zh: "圖片識別碼", en: "Image fingerprint"},
		{entity: "categories", field: "alt", zh: "圖片替代文字", en: "Image alternative text"},
		{entity: "invoice_operations", field: "resend_authorizations", zh: "重送授權次數", en: "Resend authorization count"},
		{entity: "invoice_documents", field: "order", zh: "訂單編號", en: "Order number"},
		{entity: "invoice_documents", field: "invoice", zh: "統一發票", en: "Tax invoice"},
		{entity: "invoice_documents", field: "allowance", zh: "折讓", en: "Credit note"},
		{entity: "invoice_documents", field: "operation", zh: "發票操作編號", en: "Invoice operation ID"},
		{entity: "invoice_documents", field: "status", zh: "狀態", en: "Status"},
		{entity: "invoice_documents", field: "amount_cents", zh: "金額", en: "Amount"},
		{entity: "invoice_documents", field: "provider_status", zh: "加值中心狀態", en: "Provider status"},
		{entity: "invoice_documents", field: "replacement_operation", zh: "替代發票操作編號", en: "Replacement invoice operation ID"},
		{entity: "invoice_documents", field: "refrozen_amount_cents", zh: "重新保留金額", en: "Amount held again"},
		{entity: "invoice_documents", field: "reason", zh: "原因", en: "Reason"},
		{entity: "refunds", field: "status", zh: "狀態", en: "Status"},
		{entity: "refunds", field: "request_key", zh: "退款請求識別碼", en: "Refund request key"},
		{entity: "refunds", field: "attempt_no", zh: "退款嘗試次數", en: "Refund attempt number"},
		{entity: "refunds", field: "previous_refund_id", zh: "前次退款編號", en: "Previous refund ID"},
		{entity: "refunds", field: "amount_cents", zh: "金額", en: "Amount"},
		{entity: "refunds", field: "provider_ref", zh: "金流端編號", en: "Provider reference"},
		{entity: "refunds", field: "evidence", zh: "退款證據", en: "Refund evidence"},
		{entity: "newsletter_issues", field: "issue_id", zh: "電子報編號", en: "Newsletter ID"},
		{entity: "newsletter_issues", field: "subject", zh: "主旨", en: "Subject"},
		{entity: "newsletter_issues", field: "recipients", zh: "收信人數", en: "Recipient count"},
	}
	for _, field := range fields {
		for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
			t.Run(field.entity+"/"+field.field+"/"+string(locale), func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				label := field.zh
				if locale == i18n.En {
					label = field.en
				}
				entry := AuditEntry{Entity: field.entity, Changes: []AuditChange{{Field: field.field, Before: "before-recorded", After: "after-recorded"}}}
				rendered := renderComponent(t, ctx, Audit(layouts.Page{}, AuditView{Rows: []AuditEntry{entry}}))
				for _, want := range []string{"<dt>" + stdhtml.EscapeString(label) + "</dt>", "<dd>before-recorded → after-recorded</dd>"} {
					if !strings.Contains(rendered, want) {
						t.Errorf("recorded %s.%s is missing %q", field.entity, field.field, want)
					}
				}
			})
		}
	}
}

func TestAuditUnknownFieldsRetainDetails(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		field  string
		before string
		after  string
		zh     string
		en     string
		text   string
	}{
		{name: "future field", field: "future_key", before: "old", after: "new", zh: "這筆記錄包含「future_key」欄位。", en: "This record includes the field “future_key”.", text: "old → new"},
		{name: "hostile field and value", field: "<script>alert(1)</script>", before: "<b>old</b>", after: "<img src=x onerror=alert(1)>", zh: "這筆記錄包含「<script>alert(1)</script>」欄位。", en: "This record includes the field “<script>alert(1)</script>”.", text: "<b>old</b> → <img src=x onerror=alert(1)>"},
		{name: "historical non-object", after: `["old",null]`, zh: "記錄內容", en: "Recorded details", text: `["old",null]`},
		{name: "null value", field: "future_null", after: "—", zh: "這筆記錄包含「future_null」欄位。", en: "This record includes the field “future_null”.", text: "—"},
		{name: "removed value", field: "future_removed", before: "original", zh: "這筆記錄包含「future_removed」欄位。", en: "This record includes the field “future_removed”.", text: "original"},
	} {
		for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
			t.Run(tt.name+"/"+string(locale), func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				label := tt.zh
				if locale == i18n.En {
					label = tt.en
				}
				entry := AuditEntry{Entity: "future_table", Changes: []AuditChange{{Field: tt.field, Before: tt.before, After: tt.after}}}
				changes := entry.ReadableChanges(ctx)
				if len(changes) != 1 {
					t.Fatalf("got %d changes, want one", len(changes))
				}
				if changes[0].Label != label || changes[0].Text != tt.text || changes[0].Href != "" {
					t.Errorf("unknown field = %#v, want label %q and text %q without a link", changes[0], label, tt.text)
				}
				rendered := renderComponent(t, ctx, Audit(layouts.Page{}, AuditView{Rows: []AuditEntry{entry}}))
				for _, want := range []string{"<dt>" + stdhtml.EscapeString(label) + "</dt>", "<dd>" + stdhtml.EscapeString(tt.text) + "</dd>"} {
					if !strings.Contains(rendered, want) {
						t.Errorf("unknown field is missing %q", want)
					}
				}
			})
		}
	}
}

func TestAuditProducerFieldsHaveLabels(t *testing.T) {
	t.Parallel()
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(root, "go.mod")); statErr == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Fatal("cannot find the repository's go.mod")
		}
		root = parent
	}
	fields := make(map[string]map[string]string)
	add := func(entity string, keys []string, source string) {
		if fields[entity] == nil {
			fields[entity] = make(map[string]string)
		}
		for _, key := range keys {
			fields[entity][key] = source
		}
	}
	wrappers := auditSQLProducerFields(t, root, add)
	files := make(map[string][]*ast.File)
	paths := make(map[*ast.File]string)
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if parseErr != nil {
			return parseErr
		}
		if !ast.IsGenerated(file) {
			files[filepath.Dir(path)] = append(files[filepath.Dir(path)], file)
			paths[file] = path
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for dir, packageFiles := range files {
		for _, file := range packageFiles {
			imports := make(map[string]string)
			for _, imp := range file.Imports {
				path, unquoteErr := strconv.Unquote(imp.Path.Value)
				if unquoteErr != nil {
					t.Fatal(unquoteErr)
				}
				name := filepath.Base(path)
				if imp.Name != nil {
					name = imp.Name.Name
				}
				imports[name] = path
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				selectedEntity, tableVariable := "", ""
				var resolve func(ast.Expr, bool, *ast.FuncDecl, map[string]bool) []string
				resolve = func(expr ast.Expr, payload bool, scope *ast.FuncDecl, visiting map[string]bool) []string {
					var result []string
					switch value := expr.(type) {
					case *ast.BasicLit:
						if !payload && value.Kind == token.STRING {
							text, unquoteErr := strconv.Unquote(value.Value)
							if unquoteErr != nil {
								t.Fatal(unquoteErr)
							}
							return []string{text}
						}
					case *ast.Ident:
						if value.Name == "nil" {
							return nil
						}
						if visiting[value.Name] {
							t.Fatalf("%s: cyclic audit payload %s", paths[file], value.Name)
						}
						next := make(map[string]bool)
						for name, seen := range visiting {
							next[name] = seen
						}
						next[value.Name] = true
						found := false
						ast.Inspect(scope.Body, func(node ast.Node) bool {
							if branch, isBranch := node.(*ast.IfStmt); payload && isBranch && tableVariable != "" {
								if entity := auditBranchTable(t, branch, tableVariable); entity != "" && entity != selectedEntity {
									return false
								}
							}
							assignment, isAssignment := node.(*ast.AssignStmt)
							if !isAssignment {
								return true
							}
							for index, lhs := range assignment.Lhs {
								if id, isID := lhs.(*ast.Ident); isID && id.Name == value.Name && index < len(assignment.Rhs) {
									found = true
									result = append(result, resolve(assignment.Rhs[index], payload, scope, next)...)
								}
								if slot, isSlot := lhs.(*ast.IndexExpr); payload && isSlot {
									if id, isID := slot.X.(*ast.Ident); isID && id.Name == value.Name {
										result = append(result, resolve(slot.Index, false, scope, next)...)
									}
								}
							}
							return true
						})
						if !found && payload && file.Name.Name == "staff" && scope.Name.Name == "write" && value.Name == "after" {
							return auditStaffCallbackFields(t, packageFiles, scope, func(expr ast.Expr, callback *ast.FuncDecl) []string {
								return resolve(expr, true, callback, make(map[string]bool))
							})
						}
						if !found && payload {
							for _, param := range scope.Type.Params.List {
								for _, name := range param.Names {
									if name.Name == value.Name {
										return auditStructJSONFields(t, root, dir, param.Type, imports, files)
									}
								}
							}
						}
						if found {
							return result
						}
					case *ast.CompositeLit:
						if _, isMap := value.Type.(*ast.MapType); payload && isMap {
							for _, element := range value.Elts {
								pair, isPair := element.(*ast.KeyValueExpr)
								if !isPair {
									t.Fatalf("%s: unsupported audit map element", paths[file])
								}
								result = append(result, resolve(pair.Key, false, scope, visiting)...)
							}
							return result
						}
						if payload {
							return auditStructJSONFields(t, root, dir, value.Type, imports, files)
						}
					case *ast.CallExpr:
						if name, isName := value.Fun.(*ast.SelectorExpr); isName && name.Sel.Name == "Marshal" && len(value.Args) == 1 {
							if pkg, isPackage := name.X.(*ast.Ident); isPackage && imports[pkg.Name] == "encoding/json" {
								return resolve(value.Args[0], payload, scope, visiting)
							}
						}
						if payload && auditGoEmptyMapCall(value) {
							return nil
						}
						name, isName := value.Fun.(*ast.Ident)
						if !isName {
							break
						}
						helper := auditGoFunction(packageFiles, name.Name)
						if helper == nil {
							break
						}
						for _, returned := range auditGoFunctionReturns(helper) {
							result = append(result, resolve(returned, payload, helper, make(map[string]bool))...)
						}
						return result
					}
					t.Fatalf("%s:%d: unsupported audit expression %T; extend producer discovery", paths[file], fset.Position(expr.Pos()).Line, expr)
					return nil
				}
				ast.Inspect(fn.Body, func(node ast.Node) bool {
					literal, isLiteral := node.(*ast.CompositeLit)
					if !isLiteral {
						return true
					}
					typ, isType := literal.Type.(*ast.SelectorExpr)
					if !isType {
						return true
					}
					pkg, isPackage := typ.X.(*ast.Ident)
					if !isPackage {
						return true
					}
					members := make(map[string]ast.Expr)
					for _, element := range literal.Elts {
						if pair, isPair := element.(*ast.KeyValueExpr); isPair {
							if key, isKey := pair.Key.(*ast.Ident); isKey {
								members[key.Name] = pair.Value
							}
						}
					}
					var entities []string
					switch {
					case imports[pkg.Name] == "github.com/koopa0/goen/internal/admin/audit" && typ.Sel.Name == "Event":
						selectedEntity, tableVariable = "", ""
						entities = resolve(members["Table"], false, fn, make(map[string]bool))
						if variable, isVariable := members["Table"].(*ast.Ident); isVariable {
							tableVariable = variable.Name
						}
					case imports[pkg.Name] == "github.com/koopa0/goen/internal/db" && strings.HasSuffix(typ.Sel.Name, "Params"):
						entity, isWrapper := wrappers[strings.TrimSuffix(typ.Sel.Name, "Params")]
						if !isWrapper {
							return true
						}
						if entity != "" {
							entities = []string{entity}
							break
						}
						if file.Name.Name == "audit" {
							return true
						}
						entities = resolve(members["EntityTable"], false, fn, make(map[string]bool))
					default:
						return true
					}
					for _, member := range []string{"Before", "After"} {
						if expr := members[member]; expr != nil {
							for _, entity := range entities {
								selectedEntity = entity
								keys := resolve(expr, true, fn, make(map[string]bool))
								add(entity, keys, paths[file]+":"+strconv.Itoa(fset.Position(expr.Pos()).Line))
							}
						}
					}
					return true
				})
			}
		}
	}
	if len(fields) == 0 {
		t.Fatal("producer discovery found no audit fields")
	}
	unique := make(map[string]bool)
	pairs := 0
	for entity, keys := range fields {
		for key, source := range keys {
			unique[key] = true
			pairs++
			if _, ok := (AuditEntry{Entity: entity}).field(key); !ok {
				t.Errorf("recorded producer field %s.%s from %s has no audit label", entity, key, source)
			}
		}
	}
	t.Logf("derived %d entity/key pairs, %d keys across %d entities", pairs, len(unique), len(fields))
}

func auditStructJSONFields(t *testing.T, root, dir string, expr ast.Expr, imports map[string]string, files map[string][]*ast.File) []string {
	t.Helper()
	name := ""
	switch typ := expr.(type) {
	case *ast.Ident:
		name = typ.Name
	case *ast.SelectorExpr:
		pkg, ok := typ.X.(*ast.Ident)
		if !ok {
			t.Fatal("unsupported audit struct package")
		}
		path := strings.TrimPrefix(imports[pkg.Name], "github.com/koopa0/goen/")
		dir = filepath.Join(root, path)
		name = typ.Sel.Name
	default:
		t.Fatalf("unsupported audit struct type %T", expr)
	}
	for _, file := range files[dir] {
		for _, decl := range file.Decls {
			general, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range general.Specs {
				typ, isType := spec.(*ast.TypeSpec)
				if !isType || typ.Name.Name != name {
					continue
				}
				structure, isStruct := typ.Type.(*ast.StructType)
				if !isStruct {
					t.Fatalf("audit payload %s is not a struct", name)
				}
				var keys []string
				for _, field := range structure.Fields.List {
					if len(field.Names) == 0 {
						t.Fatal("embedded audit payload fields need explicit discovery")
					}
					for _, fieldName := range field.Names {
						if !ast.IsExported(fieldName.Name) {
							continue
						}
						key := fieldName.Name
						if field.Tag != nil {
							tag, err := strconv.Unquote(field.Tag.Value)
							if err != nil {
								t.Fatal(err)
							}
							if jsonName := strings.Split(reflect.StructTag(tag).Get("json"), ",")[0]; jsonName != "" {
								key = jsonName
							}
						}
						if key != "-" {
							keys = append(keys, key)
						}
					}
				}
				return keys
			}
		}
	}
	t.Fatalf("cannot resolve audited struct %s", name)
	return nil
}

func auditGoEmptyMapCall(call *ast.CallExpr) bool {
	name, isName := call.Fun.(*ast.Ident)
	if !isName || name.Name != "make" || len(call.Args) == 0 {
		return false
	}
	_, isMap := call.Args[0].(*ast.MapType)
	return isMap
}

func auditGoFunction(files []*ast.File, name string) *ast.FuncDecl {
	for _, file := range files {
		for _, declaration := range file.Decls {
			helper, isHelper := declaration.(*ast.FuncDecl)
			if isHelper && helper.Name.Name == name && helper.Body != nil {
				return helper
			}
		}
	}
	return nil
}

func auditGoFunctionReturns(helper *ast.FuncDecl) []ast.Expr {
	var result []ast.Expr
	ast.Inspect(helper.Body, func(node ast.Node) bool {
		if returned, isReturn := node.(*ast.ReturnStmt); isReturn && len(returned.Results) > 0 {
			result = append(result, returned.Results[0])
		}
		return true
	})
	return result
}

func auditSQLProducerFields(t *testing.T, root string, add func(string, []string, string)) map[string]string {
	t.Helper()
	wrappers := make(map[string]string)
	readPayload := func(tokens []string) []string {
		if len(tokens) == 1 && strings.EqualFold(tokens[0], "null") {
			return nil
		}
		if len(tokens) > 1 && strings.EqualFold(tokens[0], "jsonb_build_object") {
			args, _ := auditSQLArguments(t, tokens, 1)
			if len(args)%2 != 0 {
				t.Fatal("odd audit JSON object arguments")
			}
			var keys []string
			for index := 0; index < len(args); index += 2 {
				if len(args[index]) != 1 || !strings.HasPrefix(args[index][0], "'") {
					t.Fatal("dynamic SQL audit key needs explicit discovery")
				}
				keys = append(keys, strings.ReplaceAll(strings.Trim(args[index][0], "'"), "''", "'"))
			}
			return keys
		}
		t.Fatalf("unsupported SQL audit payload %v; extend producer discovery", tokens)
		return nil
	}
	readCall := func(name string, args [][]string, invoiceEntity, path string) {
		t.Helper()
		if auditSQLQueryParameters(path, args) {
			return
		}
		switch name {
		case "record_audit_event":
			if (len(args) != 6 && len(args) != 7) || len(args[2]) != 1 || !strings.HasPrefix(args[2][0], "'") {
				t.Fatalf("unsupported audit SQL call in %s", path)
			}
			entity := strings.Trim(args[2][0], "'")
			add(entity, readPayload(args[4]), path)
			add(entity, readPayload(args[5]), path)
		case "record_invoice_operation_audit":
			if len(args) != 3 {
				t.Fatalf("unsupported invoice audit SQL call in %s", path)
			}
			if invoiceEntity == "" {
				t.Fatalf("%s: cannot resolve invoice audit wrapper entity", path)
			}
			add(invoiceEntity, readPayload(args[2]), path)
		}
	}
	readInsert := func(function string, signature [][]string, tokens []string, index int, path string) {
		t.Helper()
		columns, values := auditSQLInsertValues(t, tokens, index)
		entity := auditSQLInsertEntity(columns, values)
		if entity == "" {
			if !auditSQLGenericForwarder(function, signature, columns, values) {
				t.Fatalf("%s: unsupported dynamic audit INSERT entity in %s", path, function)
			}
			return
		}
		invoiceForwarder := auditSQLInvoiceForwarder(function, signature, columns, values)
		for column, names := range columns {
			if names[0] != "before" && names[0] != "after" {
				continue
			}
			if names[0] == "after" && invoiceForwarder {
				continue // Only this exact wrapper forwards caller-supplied JSON.
			}
			add(entity, readPayload(values[column]), path)
		}
	}
	sourceFS := os.DirFS(root)
	paths, err := filepath.Glob(filepath.Join(root, "migrations", "*.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{"internal", "seed", "scripts"} {
		if err := filepath.WalkDir(filepath.Join(root, directory), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() && strings.HasSuffix(path, ".sql") {
				paths = append(paths, path)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range paths {
		relative, pathErr := filepath.Rel(root, path)
		if pathErr != nil {
			t.Fatal(pathErr)
		}
		data, readErr := fs.ReadFile(sourceFS, filepath.ToSlash(relative))
		if readErr != nil {
			t.Fatal(readErr)
		}
		tokens := auditSQLTokens(t, string(data))
		// Infer the forwarding wrapper's table from its actual INSERT.
		invoiceEntity := ""
		function := ""
		var signature [][]string
		for index, word := range tokens {
			if strings.EqualFold(word, "create") && index+2 < len(tokens) && strings.EqualFold(tokens[index+1], "function") {
				function, signature = auditSQLFunctionDeclaration(t, tokens, index)
			}
			if word == "$audit_body_end$" {
				function, signature = "", nil
			}
			if function != "record_invoice_operation_audit" || !strings.EqualFold(word, "insert") || index+3 >= len(tokens) || !strings.EqualFold(tokens[index+2], "audit_events") {
				continue
			}
			columns, end := auditSQLArguments(t, tokens, index+3)
			if end+1 >= len(tokens) || !strings.EqualFold(tokens[end], "values") {
				t.Fatal("invoice audit wrapper has no explicit VALUES row")
			}
			values, _ := auditSQLArguments(t, tokens, end+1)
			if len(columns) != len(values) {
				t.Fatal("invoice audit wrapper columns and values differ")
			}
			if !auditSQLInvoiceForwarder(function, signature, columns, values) {
				t.Fatal("unfamiliar invoice audit forwarding function")
			}
			for column, names := range columns {
				if names[0] != "entity_table" {
					continue
				}
				if len(values[column]) != 1 || !strings.HasPrefix(values[column][0], "'") {
					t.Fatal("invoice audit wrapper has a dynamic entity")
				}
				invoiceEntity = strings.Trim(values[column][0], "'")
			}
		}
		query := ""
		offset := 0
		for _, line := range strings.Split(string(data), "\n") {
			lineStart := offset
			offset += len(line) + 1
			if strings.HasPrefix(line, "-- name: ") {
				query = strings.Fields(line)[2]
			}
			if strings.Contains(line, "SELECT record_audit_event(") && query != "" {
				start := lineStart
				args, _ := auditSQLArguments(t, auditSQLTokens(t, string(data)[start:]), 2)
				wrappers[query] = strings.Trim(args[2][0], "'")
				if !strings.HasPrefix(args[2][0], "'") {
					wrappers[query] = ""
				}
			}
		}
		function, signature = "", nil
		for index, word := range tokens {
			name := strings.ToLower(word)
			if name == "create" && index+2 < len(tokens) && strings.EqualFold(tokens[index+1], "function") {
				function, signature = auditSQLFunctionDeclaration(t, tokens, index)
			}
			if word == "$audit_body_end$" {
				function, signature = "", nil
			}
			if (name == "record_audit_event" || name == "record_invoice_operation_audit") && index+1 < len(tokens) && tokens[index+1] == "(" && (index == 0 || !strings.EqualFold(tokens[index-1], "function")) {
				args, _ := auditSQLArguments(t, tokens, index+1)
				readCall(name, args, invoiceEntity, path)
			}
			if name == "insert" && index+3 < len(tokens) && strings.EqualFold(tokens[index+1], "into") && strings.EqualFold(tokens[index+2], "audit_events") {
				readInsert(function, signature, tokens, index, path)
			}
		}
	}
	return wrappers
}

func auditSQLQueryParameters(path string, args [][]string) bool {
	if !strings.Contains(path, string(filepath.Separator)+"internal"+string(filepath.Separator)) {
		return false
	}
	// Parameter-only query wrappers are resolved at their Go callers.
	if len(args) == 7 && len(args[4]) > 0 && len(args[5]) > 0 && args[4][0] == "@" && args[5][0] == "@" {
		return true
	}
	return len(args) == 7 && len(args[5]) > 0 && args[5][0] == "@" && strings.EqualFold(args[4][0], "null")
}

func auditSQLInsertValues(t *testing.T, tokens []string, index int) (columns, values [][]string) {
	t.Helper()
	columns, end := auditSQLArguments(t, tokens, index+3)
	if end+1 >= len(tokens) || !strings.EqualFold(tokens[end], "values") {
		t.Fatal("audit INSERT is not an explicit VALUES row")
	}
	values, _ = auditSQLArguments(t, tokens, end+1)
	if len(columns) != len(values) {
		t.Fatal("audit INSERT columns and values differ")
	}
	return columns, values
}

func auditSQLInsertEntity(columns, values [][]string) string {
	entity := ""
	for column, names := range columns {
		if names[0] == "entity_table" && len(values[column]) == 1 && strings.HasPrefix(values[column][0], "'") {
			entity = strings.Trim(values[column][0], "'")
		}
	}
	return entity
}

func auditSQLArguments(t *testing.T, tokens []string, start int) (args [][]string, next int) {
	t.Helper()
	if start >= len(tokens) || tokens[start] != "(" {
		t.Fatalf("expected SQL argument list at token %d in %v", start, tokens[max(0, start-3):min(len(tokens), start+3)])
	}
	var current []string
	depth := 1
	for index := start + 1; index < len(tokens); index++ {
		word := tokens[index]
		switch word {
		case "(", "[":
			depth++
		case ")", "]":
			depth--
		}
		if depth == 0 {
			return append(args, current), index + 1
		}
		if word == "," && depth == 1 {
			args = append(args, current)
			current = nil
		} else {
			current = append(current, word)
		}
	}
	t.Fatal("unterminated SQL argument list")
	return nil, 0
}

func auditSQLTokens(t *testing.T, source string) []string {
	t.Helper()
	var tokens []string
	for index := 0; index < len(source); {
		if strings.HasPrefix(source[index:], "--") {
			end := strings.IndexByte(source[index:], '\n')
			if end < 0 {
				break
			}
			index += end + 1
			continue
		}
		if strings.HasPrefix(source[index:], "/*") {
			end := strings.Index(source[index+2:], "*/")
			if end < 0 {
				t.Fatal("unterminated SQL comment")
			}
			index += end + 4
			continue
		}
		character := source[index]
		if character == '$' {
			quoted, next := auditSQLDollarTokens(t, source, index, tokens)
			if next != index {
				tokens = append(tokens, quoted...)
				index = next
				continue
			}
		}
		switch {
		case character == '\'':
			start := index
			index++
			for index < len(source) {
				if source[index] == '\'' {
					index++
					if index < len(source) && source[index] == '\'' {
						index++
						continue
					}
					break
				}
				index++
			}
			tokens = append(tokens, source[start:index])
		case unicode.IsSpace(rune(character)):
			index++
		case character == '_' || unicode.IsLetter(rune(character)):
			start := index
			for index < len(source) && (source[index] == '_' || unicode.IsLetter(rune(source[index])) || unicode.IsDigit(rune(source[index]))) {
				index++
			}
			tokens = append(tokens, source[start:index])
		default:
			tokens = append(tokens, string(character))
			index++
		}
	}
	return tokens
}

func auditSQLDollarTokens(t *testing.T, source string, start int, preceding []string) (tokens []string, next int) {
	t.Helper()
	end := start + 1
	for end < len(source) && (source[end] == '_' || unicode.IsLetter(rune(source[end])) || unicode.IsDigit(rune(source[end]))) {
		end++
	}
	if end >= len(source) || source[end] != '$' {
		return nil, start
	}
	marker := source[start : end+1]
	closeAt := strings.Index(source[end+1:], marker)
	if closeAt < 0 {
		t.Fatal("unterminated SQL dollar quote")
	}
	bodyEnd := end + 1 + closeAt
	next = bodyEnd + len(marker)
	if len(preceding) == 0 || (!strings.EqualFold(preceding[len(preceding)-1], "as") && !strings.EqualFold(preceding[len(preceding)-1], "do")) {
		return []string{source[start:next]}, next
	}
	tokens = append(tokens, "$audit_body_begin$")
	tokens = append(tokens, auditSQLTokens(t, source[end+1:bodyEnd])...)
	tokens = append(tokens, "$audit_body_end$")
	return tokens, next
}

func auditStaffCallbackFields(t *testing.T, packageFiles []*ast.File, scope *ast.FuncDecl, resolve func(ast.Expr, *ast.FuncDecl) []string) []string {
	t.Helper()
	var result []string

	for _, source := range packageFiles {
		ast.Inspect(source, func(node ast.Node) bool {
			call, isCall := node.(*ast.CallExpr)
			if !isCall {
				return true
			}
			method, isMethod := call.Fun.(*ast.SelectorExpr)
			if !isMethod || method.Sel.Name != "write" {
				return true
			}
			if len(call.Args) != 3 {
				t.Fatal("unsupported staff audit wrapper arguments")
			}
			callback, isCallback := call.Args[2].(*ast.FuncLit)
			if !isCallback {
				t.Fatal("staff audit wrapper needs callback source discovery")
			}
			callbackScope := &ast.FuncDecl{Name: scope.Name, Type: callback.Type, Body: callback.Body}
			ast.Inspect(callback.Body, func(child ast.Node) bool {
				if returned, isReturn := child.(*ast.ReturnStmt); isReturn && len(returned.Results) == 3 {
					result = append(result, resolve(returned.Results[1], callbackScope)...)
				}
				return true
			})
			return true
		})
	}
	if len(result) == 0 {
		t.Fatal("staff audit wrapper has no discoverable callback payloads")
	}
	return result
}

func auditBranchTable(t *testing.T, branch *ast.IfStmt, name string) string {
	t.Helper()
	entity := ""
	ast.Inspect(branch.Body, func(node ast.Node) bool {
		assignment, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for index, lhs := range assignment.Lhs {
			id, isID := lhs.(*ast.Ident)
			if !isID || id.Name != name || index >= len(assignment.Rhs) {
				continue
			}
			text, isText := assignment.Rhs[index].(*ast.BasicLit)
			if !isText || text.Kind != token.STRING {
				t.Fatal("conditional audit table needs explicit source discovery")
			}
			var err error
			entity, err = strconv.Unquote(text.Value)
			if err != nil {
				t.Fatal(err)
			}
		}
		return true
	})
	return entity
}

func auditSQLSignatureMatches(signature [][]string, namesAndTypes []string) bool {
	if len(signature)*2 != len(namesAndTypes) {
		return false
	}
	for index, parameter := range signature {
		if len(parameter) < 2 || parameter[0] != namesAndTypes[index*2] || parameter[1] != namesAndTypes[index*2+1] {
			return false
		}
	}
	return true
}

func auditSQLColumnsMatch(columns, values [][]string, expected map[string]string) bool {
	if len(columns) != len(values) {
		return false
	}
	for name, value := range expected {
		found := false
		for index, column := range columns {
			if len(column) == 1 && column[0] == name && len(values[index]) == 1 && values[index][0] == value {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func auditSQLGenericForwarder(function string, signature, columns, values [][]string) bool {
	return function == "record_audit_event" &&
		auditSQLSignatureMatches(signature, []string{"p_actor", "uuid", "p_action", "text", "p_entity_table", "text", "p_entity_id", "uuid", "p_before", "jsonb", "p_after", "jsonb", "p_request_id", "text"}) &&
		auditSQLColumnsMatch(columns, values, map[string]string{"entity_table": "p_entity_table", "before": "p_before", "after": "p_after"})
}

func auditSQLInvoiceForwarder(function string, signature, columns, values [][]string) bool {
	return function == "record_invoice_operation_audit" &&
		auditSQLSignatureMatches(signature, []string{"p_operation_id", "uuid", "p_entity_id", "uuid", "p_after", "jsonb"}) &&
		auditSQLColumnsMatch(columns, values, map[string]string{"entity_id": "p_entity_id", "before": "NULL", "after": "p_after"})
}

func TestAuditSQLForwardingRequiresItsExactFunction(t *testing.T) {
	t.Parallel()
	columns := [][]string{{"entity_id"}, {"entity_table"}, {"before"}, {"after"}}
	generic := [][]string{{"p_actor", "uuid"}, {"p_action", "text"}, {"p_entity_table", "text"}, {"p_entity_id", "uuid"}, {"p_before", "jsonb"}, {"p_after", "jsonb"}, {"p_request_id", "text"}}
	invoice := [][]string{{"p_operation_id", "uuid"}, {"p_entity_id", "uuid"}, {"p_after", "jsonb"}}
	genericValues := [][]string{{"p_entity_id"}, {"p_entity_table"}, {"p_before"}, {"p_after"}}
	invoiceValues := [][]string{{"p_entity_id"}, {"'invoice_documents'"}, {"NULL"}, {"p_after"}}
	if !auditSQLGenericForwarder("record_audit_event", generic, columns, genericValues) {
		t.Error("the exact generic forwarding function was not recognized")
	}
	if !auditSQLInvoiceForwarder("record_invoice_operation_audit", invoice, columns, invoiceValues) {
		t.Error("the exact invoice forwarding function was not recognized")
	}
	for _, function := range []string{"", "new_writer", "record_invoice_operation_audit"} {
		if auditSQLGenericForwarder(function, generic, columns, genericValues) {
			t.Errorf("unrecognized %q may silently omit a dynamic entity", function)
		}
	}
	for _, function := range []string{"", "new_writer", "record_audit_event"} {
		if auditSQLInvoiceForwarder(function, invoice, columns, invoiceValues) {
			t.Errorf("unrecognized %q may silently omit p_after", function)
		}
	}
	if auditSQLGenericForwarder("record_audit_event", invoice, columns, genericValues) || auditSQLInvoiceForwarder("record_invoice_operation_audit", generic, columns, invoiceValues) {
		t.Error("a forwarding name with a different signature was accepted")
	}
	wrongValues := [][]string{{"p_entity_id"}, {"p_entity_table"}, {"p_before"}, {"new_payload"}}
	if auditSQLGenericForwarder("record_audit_event", generic, columns, wrongValues) {
		t.Error("a forwarding function with a different payload was accepted")
	}
}

func auditSQLFunctionDeclaration(t *testing.T, tokens []string, start int) (name string, signature [][]string) {
	t.Helper()
	var declaration strings.Builder
	declaration.WriteString(tokens[start+2])
	index := start + 3
	for index+1 < len(tokens) && tokens[index] == "." {
		declaration.WriteByte('.')
		declaration.WriteString(tokens[index+1])
		index += 2
	}
	signature, _ = auditSQLArguments(t, tokens, index)
	return declaration.String(), signature
}
