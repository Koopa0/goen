package admin

import (
	"html"
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
		{entity: "invoice_operations", field: "resend_authorizations", zh: "重送授權次數", en: "Resend authorisation count"},
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
				for _, want := range []string{"<dt>" + html.EscapeString(label) + "</dt>", "<dd>before-recorded → after-recorded</dd>"} {
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
		{name: "historical non-object", after: `["old",null]`, zh: "記錄內容。", en: "Recorded details.", text: `["old",null]`},
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
				for _, want := range []string{"<dt>" + html.EscapeString(label) + "</dt>", "<dd>" + html.EscapeString(tt.text) + "</dd>"} {
					if !strings.Contains(rendered, want) {
						t.Errorf("unknown field is missing %q", want)
					}
				}
			})
		}
	}
}
