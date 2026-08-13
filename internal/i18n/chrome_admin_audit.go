package i18n

// The audit trail's action names.
//
// One key per Action constant `internal/admin` records. They are their own file
// because they are a VOCABULARY rather than a page: the trail is read by
// scanning a column of them, so they have to be short, parallel in shape, and
// consistent about the verb — "Grant store credit", never "Store credit granted"
// beside "Granting credit".
//
// The English says what the staff member DID, in the imperative-noun form a log
// column wants. Where the Chinese is a state rather than an act — 折扣碼啟用狀態
// is "the coupon's active state", written that way because one control both
// enables and disables — the English keeps that reading rather than picking one
// direction and being wrong half the time.

var (
	KeyAuditCustomerView   = key("audit.customer.view", Message{ZhHant: "查看顧客", En: "View customer"})
	KeyAuditNewsletterSend = key("audit.newsletter.send", Message{ZhHant: "寄送電子報", En: "Send newsletter"})
	KeyAuditOrderShip      = key("audit.order.ship", Message{ZhHant: "出貨", En: "Ship order"})
	KeyAuditOrderAdvance   = key("audit.order.advance", Message{ZhHant: "訂單狀態", En: "Order status"})
	KeyAuditReturnDecide   = key("audit.return.decide", Message{ZhHant: "退貨決定", En: "Decide return"})
	KeyAuditCreditGrant    = key("audit.credit.grant", Message{ZhHant: "發放商店額度", En: "Grant store credit"})
	KeyAuditStockAdjust    = key("audit.stock.adjust", Message{ZhHant: "調整庫存", En: "Adjust stock"})
	KeyAuditStockReceive   = key("audit.stock.receive", Message{ZhHant: "進貨", En: "Receive stock"})

	// 規格 is a VARIANT here, not a spec sheet. The two share a word in Chinese
	// on this very page — 新增規格 is a variant and 新增商品規格 is a spec row —
	// and English has to keep them apart, or the trail says the same thing about
	// two different acts.
	KeyAuditVariantReprice = key("audit.variant.reprice", Message{ZhHant: "調整售價", En: "Change price"})
	KeyAuditVariantRetire  = key("audit.variant.retire", Message{ZhHant: "規格上下架", En: "Variant availability"})
	KeyAuditVariantCreate  = key("audit.variant.create", Message{ZhHant: "新增規格", En: "Add variant"})
	KeyAuditProductCreate  = key("audit.product.create", Message{ZhHant: "新增商品", En: "Add product"})
	KeyAuditProductStatus  = key("audit.product.status", Message{ZhHant: "商品上下架", En: "Product status"})

	KeyAuditCouponCreate = key("audit.coupon.create", Message{ZhHant: "建立折扣碼", En: "Create coupon"})
	KeyAuditCouponToggle = key("audit.coupon.toggle", Message{ZhHant: "折扣碼啟用狀態", En: "Coupon active state"})

	KeyAuditCampaignCreate    = key("audit.campaign.create", Message{ZhHant: "建立活動", En: "Create campaign"})
	KeyAuditCampaignToggle    = key("audit.campaign.toggle", Message{ZhHant: "活動啟用狀態", En: "Campaign active state"})
	KeyAuditCampaignFeature   = key("audit.campaign.feature", Message{ZhHant: "活動加入商品", En: "Add product to campaign"})
	KeyAuditCampaignUnfeature = key("audit.campaign.unfeature", Message{
		ZhHant: "活動移除商品",
		En:     "Remove product from campaign",
	})

	// An OPTION is the axis (顏色) and an option VALUE is one of its choices
	// (星霧藍). The distinction is the picker's whole design — the URL carries
	// the canonical value — so the trail names both halves rather than calling
	// each "option".
	KeyAuditOptionAdd      = key("audit.option.add", Message{ZhHant: "新增規格項目", En: "Add option"})
	KeyAuditOptionValueAdd = key("audit.option.value.add", Message{ZhHant: "新增規格選項值", En: "Add option value"})

	KeyAuditSpecAdd    = key("audit.spec.add", Message{ZhHant: "新增商品規格", En: "Add spec row"})
	KeyAuditSpecRemove = key("audit.spec.remove", Message{ZhHant: "移除商品規格", En: "Remove spec row"})

	// Attach/detach rather than add/delete: an image is content-addressed and
	// shared, so removing it from a product does not remove the object.
	KeyAuditImageAttach = key("audit.image.attach", Message{ZhHant: "新增商品圖片", En: "Attach product image"})
	KeyAuditImageDetach = key("audit.image.detach", Message{ZhHant: "移除商品圖片", En: "Detach product image"})

	KeyAuditShippingMethodCreate = key("audit.shipping.method.create", Message{
		ZhHant: "新增配送方式",
		En:     "Add delivery method",
	})
	KeyAuditShippingMethodToggle = key("audit.shipping.method.toggle", Message{
		ZhHant: "開關配送方式",
		En:     "Delivery method active state",
	})
	KeyAuditShippingZoneCreate = key("audit.shipping.zone.create", Message{
		ZhHant: "新增配送區域",
		En:     "Add delivery zone",
	})
	KeyAuditShippingZonePrefixes = key("audit.shipping.zone.prefixes", Message{
		ZhHant: "設定區域郵遞區號",
		En:     "Set zone postal codes",
	})
	KeyAuditShippingZoneDelete = key("audit.shipping.zone.delete", Message{
		ZhHant: "刪除配送區域",
		En:     "Delete delivery zone",
	})

	KeyAuditFAQCreate = key("audit.faq.create", Message{ZhHant: "新增常見問題", En: "Add FAQ entry"})
	KeyAuditFAQUpdate = key("audit.faq.update", Message{ZhHant: "修改常見問題", En: "Edit FAQ entry"})
	KeyAuditFAQDelete = key("audit.faq.delete", Message{ZhHant: "刪除常見問題", En: "Delete FAQ entry"})

	// 促銷條 is the strip above the header, 主視覺 the hero below it. "Banner"
	// for both would make the trail unable to say which one somebody changed.
	KeyAuditBannerCreate = key("audit.banner.create", Message{ZhHant: "新增促銷條", En: "Add promo strip"})
	KeyAuditBannerToggle = key("audit.banner.toggle", Message{ZhHant: "開關促銷條", En: "Promo strip active state"})
	KeyAuditHeroCreate   = key("audit.hero.create", Message{ZhHant: "新增主視覺", En: "Add hero slide"})
	KeyAuditHeroToggle   = key("audit.hero.toggle", Message{ZhHant: "主視覺啟用狀態", En: "Hero slide active state"})
	// Promote, not "set": the hero is a QUEUE and promoting one moves it to the
	// front of the others rather than replacing them.
	KeyAuditHeroPromote = key("audit.hero.promote", Message{ZhHant: "切換首頁主視覺", En: "Promote hero slide"})

	// Rename and not "edit": a slug is never renamed, only the display name, so
	// the trail names the half that can actually change.
	KeyAuditBrandCreate    = key("audit.brand.create", Message{ZhHant: "新增品牌", En: "Add brand"})
	KeyAuditBrandRename    = key("audit.brand.rename", Message{ZhHant: "更名品牌", En: "Rename brand"})
	KeyAuditBrandDelete    = key("audit.brand.delete", Message{ZhHant: "刪除品牌", En: "Delete brand"})
	KeyAuditCategoryCreate = key("audit.category.create", Message{ZhHant: "新增分類", En: "Add category"})
	KeyAuditCategoryRename = key("audit.category.rename", Message{ZhHant: "更名分類", En: "Rename category"})
	KeyAuditCategoryDelete = key("audit.category.delete", Message{ZhHant: "刪除分類", En: "Delete category"})

	KeyAuditQuestionAnswer = key("audit.question.answer", Message{ZhHant: "回覆問題", En: "Answer question"})
	KeyAuditQuestionHide   = key("audit.question.hide", Message{ZhHant: "隱藏問題", En: "Hide question"})
)
