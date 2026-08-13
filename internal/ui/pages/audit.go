package pages

// AuditEntry is one recorded back-office action.
type AuditEntry struct {
	Action string
	Entity string
	Actor  string
	At     string
	// RequestID ties this row to the log lines from the same request, which is
	// the difference between "somebody published this" and knowing what else
	// that request did.
	RequestID string
	Detail    string
}

// Label is what the action is called on the page.
//
// The switch has no default that guesses: an action goen records and does not
// name here would render as its raw key, which is visible and greppable rather
// than silently mislabelled as something else.
func (e AuditEntry) Label() string {
	if label, ok := actionLabels[e.Action]; ok {
		return label
	}
	// The raw key, which is visible and greppable — an action goen records and
	// forgets to name here should look wrong on the page rather than quietly
	// borrow another action's label.
	return e.Action
}

// actionLabels is the chrome name for each recorded action.
//
// A map rather than a switch: sixteen cases returning a constant is a lookup
// wearing control flow, and gocyclo was right to say so.
//
//nolint:gosec // G101 fires on any package-level string map; these are UI labels
var actionLabels = map[string]string{
	"customer.view":          "查看顧客",
	"newsletter.send":        "寄送電子報",
	"order.ship":             "出貨",
	"order.advance":          "訂單狀態",
	"return.decide":          "退貨決定",
	"credit.grant":           "發放商店額度",
	"stock.adjust":           "調整庫存",
	"stock.receive":          "進貨",
	"variant.reprice":        "調整售價",
	"variant.retire":         "規格上下架",
	"variant.create":         "新增規格",
	"product.create":         "新增商品",
	"product.status":         "商品上下架",
	"coupon.create":          "建立折扣碼",
	"coupon.toggle":          "折扣碼啟用狀態",
	"campaign.create":        "建立活動",
	"campaign.toggle":        "活動啟用狀態",
	"campaign.feature":       "活動加入商品",
	"campaign.unfeature":     "活動移除商品",
	"option.add":             "新增規格項目",
	"option.value.add":       "新增規格選項值",
	"spec.add":               "新增商品規格",
	"spec.remove":            "移除商品規格",
	"image.attach":           "新增商品圖片",
	"image.detach":           "移除商品圖片",
	"shipping.method.create": "新增配送方式",
	"shipping.method.toggle": "開關配送方式",
	"shipping.zone.create":   "新增配送區域",
	"shipping.zone.prefixes": "設定區域郵遞區號",
	"shipping.zone.delete":   "刪除配送區域",
	"faq.create":             "新增常見問題",
	"faq.update":             "修改常見問題",
	"faq.delete":             "刪除常見問題",
	"banner.create":          "新增促銷條",
	"banner.toggle":          "開關促銷條",
	"hero.create":            "新增主視覺",
	"hero.toggle":            "主視覺啟用狀態",
	"hero.promote":           "切換首頁主視覺",
	"brand.create":           "新增品牌",
	"brand.rename":           "更名品牌",
	"brand.delete":           "刪除品牌",
	"category.create":        "新增分類",
	"category.rename":        "更名分類",
	"category.delete":        "刪除分類",
	"question.answer":        "回覆問題",
	"question.hide":          "隱藏問題",
}

// Money reports whether this action moved money or stock, which is what a
// reader scanning the trail is looking for first.
func (e AuditEntry) Money() bool {
	switch e.Action {
	case "credit.grant", "return.decide", "stock.adjust", "variant.reprice":
		return true
	default:
		return false
	}
}

// ShortRequestID is enough of the id to match a log line by eye.
func (e AuditEntry) ShortRequestID() string {
	if len(e.RequestID) <= 8 {
		return e.RequestID
	}
	return e.RequestID[:8]
}

// AuditView is the trail.
type AuditView struct {
	Rows []AuditEntry
}

// Empty reports whether nothing has been recorded.
func (v AuditView) Empty() bool { return len(v.Rows) == 0 }

// AdminImage is one image attached to a product, as the back office shows it.
type AdminImage struct {
	Key    string
	Alt    string
	Width  int32
	Height int32
}

// URL is where it is served. An uploaded image is a digest; a seeded one is an
// embedded filename, and assets.ProductImageURL knows the difference.
func (i AdminImage) URL() string { return "/media/" + i.Key }
