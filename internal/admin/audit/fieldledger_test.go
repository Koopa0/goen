package audit

import (
	"go/ast"
	"go/parser"
	"go/token"
	stdhtml "html"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
)

// auditLedger is every (table, key) pair goen has ever written into
// audit_events.before/after, with the label the audit page shows for it.
// audit_events is append-only and erase_user does not reach it, so a pair
// stays here after the code stops writing it (users.email: staff rows hold
// only role since #1897, older rows still hold the address). Never delete a
// row; add one, with its label, in the PR that first writes the pair.
//
// What holds it: a Go writer can record only a declared Field, and
// TestFieldKeysInLedger needs every Field's key in some row here; SQL
// writers are read pair by pair (TestAuditSQLProducerFieldsAreInTheLedger).
// Nothing checks the table a Go writer records a Field under, so a desk that
// records an existing Field on a new table adds that row by hand.
//
// products.domestic_party_name/_phone/_address are the product's legally
// required labelling party (an importer or maker named on the package), not a
// customer or staff member: they are business data goen does not erase.
var auditLedger = []struct{ entity, field, zh, en string }{
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
	{entity: "faq_entries", field: "category_id", zh: "分類", en: "Category"},
	{entity: "faq_categories", field: "name", zh: "名稱", en: "Name"},
	{entity: "faq_categories", field: "name_en", zh: "名稱（英文）", en: "Name (English)"},
	{entity: "faq_categories", field: "id", zh: "記錄編號", en: "Record ID"},
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
	{entity: "order_shipments", field: "carrier", zh: "物流商", en: "Carrier"},
	{entity: "order_shipments", field: "tracking", zh: "查詢編號", en: "Tracking number"},
	{entity: "order_shipments", field: "delivered_at", zh: "送達時間", en: "Delivered at"},
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
	{entity: "product_option_values", field: "value", zh: "選項值", en: "Option value"},
	{entity: "product_images", field: "product", zh: "商品", en: "Product"},
	{entity: "product_images", field: "digest", zh: "圖片識別碼", en: "Image fingerprint"},
	{entity: "product_images", field: "alt", zh: "圖片替代文字", en: "Image alternative text"},
	{entity: "product_images", field: "option_value", zh: "圖片對應選項", en: "Image option value"},
	{entity: "product_images", field: "move", zh: "圖片移動方向", en: "Image move direction"},
	{entity: "product_images", field: "order", zh: "圖片排列順序", en: "Image order"},
	{entity: "product_variants", field: "product", zh: "商品", en: "Product"},
	{entity: "product_variants", field: "sku", zh: "款式 SKU", en: "SKU"},
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
	{entity: "return_requests", field: "reason", zh: "原因", en: "Reason"},
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
	{entity: "categories", field: "comparable", zh: "商品比較", en: "Product comparison"},
	{entity: "categories", field: "parent", zh: "上層分類", en: "Parent category"},
	{entity: "categories", field: "category", zh: "分類", en: "Category"},
	{entity: "categories", field: "digest", zh: "圖片識別碼", en: "Image fingerprint"},
	{entity: "categories", field: "alt", zh: "圖片替代文字", en: "Image alternative text"},
	{entity: "categories", field: "alt_en", zh: "替代文字（英文）", en: "Alt text (English)"},
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

func TestAuditRecordedFieldLabels(t *testing.T) {
	t.Parallel()
	for _, field := range auditLedger {
		for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
			t.Run(field.entity+"/"+field.field+"/"+string(locale), func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				label := field.zh
				if locale == i18n.En {
					label = field.en
				}
				entry := Entry{Entity: field.entity, Changes: []Change{{Field: field.field, Before: "before-recorded", After: "after-recorded"}}}
				rendered := renderComponent(t, ctx, Audit(frame.Page{}, View{Rows: []Entry{entry}}))
				for _, want := range []string{"<dt>" + stdhtml.EscapeString(label) + "</dt>", "<dd>before-recorded → after-recorded</dd>"} {
					if !strings.Contains(rendered, want) {
						t.Errorf("recorded %s.%s is missing %q", field.entity, field.field, want)
					}
				}
			})
		}
	}
}

// TestAuditFieldsInLedger keeps the labels and the ledger covering
// each other: a label with no ledger row means a stored key that may still sit
// in old rows has lost its record, or that a row was deleted while its label
// stayed. A bare label key is matched by any ledger row with that field; a
// "table.key" label by that exact row.
func TestAuditFieldsInLedger(t *testing.T) {
	t.Parallel()
	pairs := make(map[string]bool, len(auditLedger))
	fields := make(map[string]bool, len(auditLedger))
	for _, row := range auditLedger {
		pairs[row.entity+"."+row.field] = true
		fields[row.field] = true
	}
	for key := range auditFields {
		if entity, field, qualified := strings.Cut(key, "."); qualified {
			if !pairs[key] {
				t.Errorf("audit label %q (table %s, key %s) has no row in auditLedger: audit_events keeps old rows forever, so the ledger must keep the pair", key, entity, field)
			}
			continue
		}
		if !fields[key] {
			t.Errorf("audit label %q has no row in auditLedger: audit_events keeps old rows forever, so the ledger must keep the key", key)
		}
	}
	// 166 rows on 6ba69813; the ledger only grows.
	if len(auditLedger) < 166 {
		t.Errorf("auditLedger has %d rows, want at least 166: a row was deleted", len(auditLedger))
	}
}

// TestFieldKeysInLedger holds the keys a Go writer can record to the
// ledger, which gives each a label on /admin/audit. A Field misspelt where it
// is declared would store a new key under an old name; it fails here.
func TestFieldKeysInLedger(t *testing.T) {
	t.Parallel()
	fields := make(map[string]bool, len(auditLedger))
	for _, row := range auditLedger {
		fields[row.field] = true
	}
	keys := declaredFields(t)
	// 94 on 6ba69813; a parser that stops matching must not pass over nothing.
	if len(keys) < 94 {
		t.Fatalf("found %d Fields, want at least 94: the parser is not reading their declarations", len(keys))
	}
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		if seen[key] {
			t.Errorf("two Fields store %q: one key, one Field", key)
		}
		seen[key] = true
		if !fields[key] {
			t.Errorf("Field %q has no row in auditLedger: add one for each table that records it, with its label", key)
		}
	}
}

// declaredFields is the key of every Field this package's source makes.
// Field{"key"} is the only way to make one with a key.
func declaredFields(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if !ok {
				return true
			}
			if name, isName := literal.Type.(*ast.Ident); !isName || name.Name != "Field" {
				return true
			}
			var key *ast.BasicLit
			if len(literal.Elts) == 1 {
				key, _ = literal.Elts[0].(*ast.BasicLit)
			}
			if key == nil || key.Kind != token.STRING {
				t.Fatalf("%s: a Field is made from something other than one string literal", path)
			}
			text, err := strconv.Unquote(key.Value)
			if err != nil {
				t.Fatal(err)
			}
			keys = append(keys, text)
			return true
		})
	}
	return keys
}
