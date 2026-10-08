package i18n

var (
	KeyAuditCustomerView = key("audit.customer.view", Message{ZhHant: "查看顧客", En: "View customer"})

	KeyAuditNewsletterSend = key("audit.newsletter.send", Message{ZhHant: "寄送電子報", En: "Send newsletter"})

	KeyAuditNewsletterCompose = key("audit.newsletter.compose", Message{ZhHant: "撰寫電子報", En: "Compose newsletter"})

	KeyAuditOrderShip = key("audit.order.ship", Message{ZhHant: "出貨", En: "Ship order"})

	KeyAuditOrderAdvance = key("audit.order.advance", Message{ZhHant: "訂單狀態", En: "Order status"})

	KeyAuditReturnDecide = key("audit.return.decide", Message{ZhHant: "退貨決定", En: "Decide return"})

	KeyAuditCreditGrant = key("audit.credit.grant", Message{ZhHant: "發放購物金", En: "Grant store credit"})

	KeyAuditStockAdjust = key("audit.stock.adjust", Message{ZhHant: "調整庫存", En: "Adjust stock"})

	KeyAuditStockReceive = key("audit.stock.receive", Message{ZhHant: "進貨", En: "Receive stock"})

	KeyAuditVariantReprice = key("audit.variant.reprice", Message{ZhHant: "調整售價", En: "Change price"})
	KeyAuditVariantArrival = key("audit.variant.arrival", Message{ZhHant: "設定預計到貨日", En: "Set expected arrival"})

	KeyAuditVariantRetire = key("audit.variant.retire", Message{ZhHant: "規格上下架", En: "Variant availability"})

	KeyAuditVariantCreate = key("audit.variant.create", Message{ZhHant: "新增規格", En: "Add variant"})

	KeyAuditProductCreate = key("audit.product.create", Message{ZhHant: "新增商品", En: "Add product"})

	KeyAuditProductUpdate = key("audit.product.update", Message{ZhHant: "修改商品", En: "Edit product"})

	KeyAuditProductStatus = key("audit.product.status", Message{ZhHant: "商品上下架", En: "Product status"})

	KeyAuditCouponCreate = key("audit.coupon.create", Message{ZhHant: "建立折扣碼", En: "Create coupon"})

	KeyAuditCouponToggle = key("audit.coupon.toggle", Message{ZhHant: "折扣碼啟用狀態", En: "Coupon active state"})

	KeyAuditCampaignCreate = key("audit.campaign.create", Message{ZhHant: "建立活動", En: "Create campaign"})

	KeyAuditCampaignImageSet   = key("audit.campaign.image.set", Message{ZhHant: "設定活動頁首圖片", En: "Set campaign header image"})
	KeyAuditCampaignImageClear = key("audit.campaign.image.clear", Message{ZhHant: "移除活動頁首圖片", En: "Remove campaign header image"})
	KeyAuditCampaignWindow     = key("audit.campaign.window", Message{ZhHant: "活動檔期", En: "Campaign dates"})
	KeyAuditCampaignTone       = key("audit.campaign.tone", Message{ZhHant: "活動色調", En: "Campaign tone"})

	KeyAuditCategoryImageSet   = key("audit.category.image.set", Message{ZhHant: "設定分類頁首圖片", En: "Set category header image"})
	KeyAuditCategoryImageClear = key("audit.category.image.clear", Message{ZhHant: "移除分類頁首圖片", En: "Remove category header image"})

	KeyAuditCampaignToggle = key("audit.campaign.toggle", Message{ZhHant: "活動啟用狀態", En: "Campaign active state"})

	KeyAuditCampaignFeature = key("audit.campaign.feature", Message{ZhHant: "活動加入商品", En: "Add product to campaign"})

	KeyAuditCampaignUnfeature = key("audit.campaign.unfeature", Message{
		ZhHant: "活動移除商品",
		En:     "Remove product from campaign",
	})

	KeyAuditOptionAdd = key("audit.option.add", Message{ZhHant: "新增規格項目", En: "Add option"})

	KeyAuditOptionValueAdd = key("audit.option.value.add", Message{ZhHant: "新增規格選項值", En: "Add option value"})

	KeyAuditSpecAdd = key("audit.spec.add", Message{ZhHant: "新增商品規格", En: "Add spec row"})

	KeyAuditSpecRemove = key("audit.spec.remove", Message{ZhHant: "移除商品規格", En: "Remove spec row"})

	KeyAuditImageAttach = key("audit.image.attach", Message{ZhHant: "新增商品圖片", En: "Attach product image"})

	KeyAuditImageMove   = key("audit.image.move", Message{ZhHant: "調整商品圖片順序", En: "Reorder product images"})
	KeyAuditImageDetach = key("audit.image.detach", Message{ZhHant: "移除商品圖片", En: "Detach product image"})

	KeyAuditImageOption = key("audit.image.option", Message{ZhHant: "設定商品圖片的選項", En: "Set product image option"})

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

	KeyAuditBannerCreate = key("audit.banner.create", Message{ZhHant: "新增促銷條", En: "Add promo strip"})

	KeyAuditBannerToggle = key("audit.banner.toggle", Message{ZhHant: "開關促銷條", En: "Promo strip active state"})

	KeyAuditHeroCreate = key("audit.hero.create", Message{ZhHant: "新增主視覺", En: "Add hero slide"})

	KeyAuditHeroToggle = key("audit.hero.toggle", Message{ZhHant: "主視覺啟用狀態", En: "Hero slide active state"})

	KeyAuditHeroPromote = key("audit.hero.promote", Message{ZhHant: "切換首頁主視覺", En: "Promote hero slide"})

	KeyAuditBrandCreate = key("audit.brand.create", Message{ZhHant: "新增品牌", En: "Add brand"})

	KeyAuditBrandRename = key("audit.brand.rename", Message{ZhHant: "更名品牌", En: "Rename brand"})

	KeyAuditBrandDelete = key("audit.brand.delete", Message{ZhHant: "刪除品牌", En: "Delete brand"})

	KeyAuditCategoryCreate = key("audit.category.create", Message{ZhHant: "新增分類", En: "Add category"})

	KeyAuditCategoryRename = key("audit.category.rename", Message{ZhHant: "更名分類", En: "Rename category"})

	KeyAuditCategoryDelete = key("audit.category.delete", Message{ZhHant: "刪除分類", En: "Delete category"})

	KeyAuditQuestionAnswer = key("audit.question.answer", Message{ZhHant: "回覆問題", En: "Answer question"})

	KeyAuditQuestionShow = key("audit.question.show", Message{ZhHant: "恢復顯示問題", En: "Show question"})

	KeyAuditQuestionHide = key("audit.question.hide", Message{ZhHant: "隱藏問題", En: "Hide question"})

	KeyAdminPageAudit = key("admin.page.audit", Message{ZhHant: "操作紀錄", En: "Activity log"})

	KeyAdminAuditLead = key("admin.audit.lead", Message{
		ZhHant: "誰在什麼時候做了什麼。這份紀錄只能新增，寫進去就改不了也刪不掉。",
		En: "Who did what, and when. This record is append-only: nothing written here " +
			"can be changed or removed.",
	})

	KeyAdminAuditEmpty = key("admin.audit.empty", Message{
		ZhHant: "還沒有任何紀錄。",
		En:     "Nothing recorded yet.",
	})

	KeyAdminActorCustomer = key("admin.actor.customer", Message{ZhHant: "顧客", En: "Customer"})

	KeyAdminActorSystem   = key("admin.actor.system", Message{ZhHant: "系統", En: "System"})
	KeyAdminActorProvider = key("admin.actor.provider", Message{ZhHant: "金流服務商", En: "Payment provider"})
	KeyAuditReturnInspect = key("audit.return.inspect", Message{ZhHant: "退貨驗收", En: "Inspect return"})

	KeyAuditReturnComplete = key("audit.return.complete", Message{ZhHant: "退貨結案", En: "Close return"})

	KeyAuditReturnRefundBeforeShipment = key("audit.return.refund_before_shipment", Message{ZhHant: "出貨前退款", En: "Refund before shipment"})

	KeyAuditOrderDelivery = key("audit.order.delivery", Message{ZhHant: "更正配送資料", En: "Correct delivery details"})

	KeyAuditPaymentReconciled = key("audit.payment.reconciled", Message{ZhHant: "款項對帳", En: "Reconcile payment"})

	KeyAuditInvoiceIssue = key("audit.invoice.issue", Message{ZhHant: "開立發票", En: "Issue invoice"})

	KeyAuditInvoiceVoid = key("audit.invoice.void", Message{ZhHant: "作廢發票", En: "Void invoice"})

	KeyAuditInvoiceAllowance = key("audit.invoice.allowance", Message{ZhHant: "開立折讓", En: "Issue allowance"})

	KeyAuditInvoiceAllowanceResend = key("audit.invoice.allowance.resend", Message{
		ZhHant: "授權重送折讓",
		En:     "Authorise an allowance resend",
	})

	KeyAuditInvoiceAllowanceInvalid = key("audit.invoice.allowance.invalid", Message{
		ZhHant: "折讓於加值中心已作廢",
		En:     "Allowance voided at the provider",
	})

	KeyAuditMessageHandle = key("audit.message.handle", Message{ZhHant: "標記已處理", En: "Mark handled"})

	KeyAuditMessageReopen = key("audit.message.reopen", Message{ZhHant: "重新開啟訊息", En: "Reopen message"})

	KeyAuditReviewHide = key("audit.review.hide", Message{ZhHant: "隱藏評價", En: "Hide review"})

	KeyAuditReviewShow = key("audit.review.show", Message{ZhHant: "取消隱藏評價", En: "Unhide review"})

	KeyAuditShippingPublish = key("audit.shipping.publish", Message{ZhHant: "發布運費版本", En: "Publish shipping version"})

	KeyAuditShippingSurcharge = key("audit.shipping.surcharge", Message{ZhHant: "設定分區加價", En: "Set zone surcharge"})

	KeyAuditTierCreate = key("audit.tier.create", Message{ZhHant: "新增會員等級", En: "Add membership tier"})

	KeyAuditTierDelete = key("audit.tier.delete", Message{ZhHant: "刪除會員等級", En: "Delete membership tier"})

	KeyAuditStaffGrant = key("audit.staff.grant", Message{ZhHant: "授予後台權限", En: "Grant staff access"})

	KeyAuditStaffRevoke = key("audit.staff.revoke", Message{ZhHant: "撤銷後台權限", En: "Revoke staff access"})

	KeyAuditStaffFactorRemove = key("audit.staff.factor.remove", Message{ZhHant: "移除第二因素", En: "Remove second factor"})
)

var (
	KeyAuditOrderNoteCreate  = key("audit.order.note.create", Message{ZhHant: "新增訂單內部備註", En: "Add internal order note"})
	KeyAuditOrderNoteReplace = key("audit.order.note.replace", Message{ZhHant: "替換訂單內部備註", En: "Replace internal order note"})
	KeyAuditOrderNoteClear   = key("audit.order.note.clear", Message{ZhHant: "清除訂單內部備註", En: "Clear internal order note"})
)

var KeyAuditAnswerHide = key("audit.answer.hide", Message{ZhHant: "撤下回覆", En: "Answer withdrawn"})

var (
	KeyAuditFieldRefrozen        = key("audit.field.refrozen", Message{ZhHant: "重新保留金額", En: "Amount held again"})
	KeyAuditFieldFee             = key("audit.field.fee", Message{ZhHant: "運費", En: "Delivery fee"})
	KeyAuditFieldFreeOver        = key("audit.field.freeover", Message{ZhHant: "免運門檻", En: "Free-delivery threshold"})
	KeyAuditFieldSurcharge       = key("audit.field.surcharge", Message{ZhHant: "分區加價", En: "Zone surcharge"})
	KeyAuditFieldDecision        = key("audit.field.decision", Message{ZhHant: "決定", En: "Decision"})
	KeyAuditFieldEntitlement     = key("audit.field.entitlement", Message{ZhHant: "退貨依據", En: "Basis for the return"})
	KeyAuditFieldPolicyWindow    = key("audit.field.policywindow", Message{ZhHant: "退貨期限", En: "Return window"})
	KeyAuditFieldCouponValue     = key("audit.field.couponvalue", Message{ZhHant: "折抵", En: "Discount"})
	KeyAuditEntitlementStatutory = key("audit.entitlement.statutory", Message{ZhHant: "七日猶豫期", En: "Statutory 7-day right to cancel"})
	KeyAuditEntitlementGoodwill  = key("audit.entitlement.goodwill", Message{ZhHant: "店家優惠", En: "The shop's voluntary offer"})
	KeyAuditEntitlementException = key("audit.entitlement.exception", Message{ZhHant: "人工例外", En: "Staff exception"})
	KeyAuditMoneyTag             = key("audit.money.tag", Message{ZhHant: "金額", En: "Money"})
)

var (
	KeyAuditFieldActive               = key("audit.field.active", Message{ZhHant: "啟用狀態", En: "Active state"})
	KeyAuditFieldAnswerLength         = key("audit.field.answer_length", Message{ZhHant: "回覆字數", En: "Answer length in characters"})
	KeyAuditFieldAssessmentVersion    = key("audit.field.assessment_version", Message{ZhHant: "退貨評估版本", En: "Return assessment version"})
	KeyAuditFieldCampaignTitleEn      = key("audit.field.campaign_title_en", Message{ZhHant: "活動標題（英文）", En: "Campaign title (English)"})
	KeyAuditFieldDeliveryMethodID     = key("audit.field.delivery_method_id", Message{ZhHant: "配送方式編號", En: "Delivery method ID"})
	KeyAuditFieldDeliveryVersionID    = key("audit.field.delivery_version_id", Message{ZhHant: "運費版本編號", En: "Delivery fee version ID"})
	KeyAuditFieldDeliveryZoneID       = key("audit.field.delivery_zone_id", Message{ZhHant: "配送區域編號", En: "Delivery zone ID"})
	KeyAuditFieldDestinationKind      = key("audit.field.destination_kind", Message{ZhHant: "收件方式", En: "Delivery destination type"})
	KeyAuditFieldDetails              = key("audit.field.details", Message{ZhHant: "記錄內容", En: "Recorded details"})
	KeyAuditFieldExpectedArrival      = key("audit.field.expected_arrival", Message{ZhHant: "預計到貨日", En: "Expected arrival date"})
	KeyAuditFieldHandled              = key("audit.field.handled", Message{ZhHant: "已處理狀態", En: "Handled state"})
	KeyAuditFieldHidden               = key("audit.field.hidden", Message{ZhHant: "隱藏狀態", En: "Hidden state"})
	KeyAuditFieldImageAlt             = key("audit.field.image_alt", Message{ZhHant: "圖片替代文字", En: "Image alternative text"})
	KeyAuditFieldImageDigest          = key("audit.field.image_digest", Message{ZhHant: "圖片識別碼", En: "Image fingerprint"})
	KeyAuditFieldImageMove            = key("audit.field.image_move", Message{ZhHant: "圖片移動方向", En: "Image move direction"})
	KeyAuditFieldImageOption          = key("audit.field.image_option", Message{ZhHant: "圖片對應選項", En: "Image option value"})
	KeyAuditFieldImageOrder           = key("audit.field.image_order", Message{ZhHant: "圖片排列順序", En: "Image order"})
	KeyAuditFieldInspectedLines       = key("audit.field.inspected_lines", Message{ZhHant: "驗收品項數", En: "Inspected item count"})
	KeyAuditFieldInvoiceOperation     = key("audit.field.invoice_operation", Message{ZhHant: "發票操作編號", En: "Invoice operation ID"})
	KeyAuditFieldMessageID            = key("audit.field.message_id", Message{ZhHant: "留言編號", En: "Message ID"})
	KeyAuditFieldNewsletterID         = key("audit.field.newsletter_id", Message{ZhHant: "電子報編號", En: "Newsletter ID"})
	KeyAuditFieldNewsletterRecipients = key("audit.field.newsletter_recipients", Message{ZhHant: "收信人數", En: "Recipient count"})
	KeyAuditFieldOptionValue          = key("audit.field.option_value", Message{ZhHant: "規格選項值", En: "Option value"})
	KeyAuditFieldParentCategory       = key("audit.field.parent_category", Message{ZhHant: "上層分類", En: "Parent category"})
	KeyAuditFieldPointsRate           = key("audit.field.points_rate", Message{ZhHant: "點數倍率（基點）", En: "Points rate (basis points)"})
	KeyAuditFieldPostalCodeCount      = key("audit.field.postal_code_count", Message{ZhHant: "郵遞區號數", En: "Postal-code count"})
	KeyAuditFieldPreviousRefund       = key("audit.field.previous_refund", Message{ZhHant: "前次退款編號", En: "Previous refund ID"})
	KeyAuditFieldProviderStatus       = key("audit.field.provider_status", Message{ZhHant: "加值中心狀態", En: "Provider status"})
	KeyAuditFieldQuestionID           = key("audit.field.question_id", Message{ZhHant: "提問編號", En: "Question ID"})
	KeyAuditFieldReceivedQuantity     = key("audit.field.received_quantity", Message{ZhHant: "進貨數量", En: "Received quantity"})
	KeyAuditFieldRecordID             = key("audit.field.record_id", Message{ZhHant: "記錄編號", En: "Record ID"})
	KeyAuditFieldRefundAttempt        = key("audit.field.refund_attempt", Message{ZhHant: "退款嘗試次數", En: "Refund attempt number"})
	KeyAuditFieldRefundEvidence       = key("audit.field.refund_evidence", Message{ZhHant: "退款證據", En: "Refund evidence"})
	KeyAuditFieldRefundRequest        = key("audit.field.refund_request", Message{ZhHant: "退款請求識別碼", En: "Refund request key"})
	KeyAuditFieldReplacementOperation = key("audit.field.replacement_operation", Message{ZhHant: "替代發票操作編號", En: "Replacement invoice operation ID"})
	KeyAuditFieldResendAuthorizations = key("audit.field.resend_authorizations", Message{ZhHant: "重送授權次數", En: "Resend authorization count"})
	KeyAuditFieldResolution           = key("audit.field.resolution", Message{ZhHant: "處理結果", En: "Resolution"})
	KeyAuditFieldRestockedLines       = key("audit.field.restocked_lines", Message{ZhHant: "回補庫存品項數", En: "Restocked item count"})
	KeyAuditFieldReturnRequest        = key("audit.field.return_request", Message{ZhHant: "退貨申請編號", En: "Return request ID"})
	KeyAuditFieldReviewID             = key("audit.field.review_id", Message{ZhHant: "評價編號", En: "Review ID"})
	KeyAuditFieldSKU                  = key("audit.field.sku", Message{ZhHant: "商品規格編號", En: "SKU"})
	KeyAuditFieldStockChange          = key("audit.field.stock_change", Message{ZhHant: "庫存調整數量", En: "Stock quantity change"})
	KeyAuditFieldUnknown              = key("audit.field.unknown", Message{ZhHant: "這筆記錄包含「%s」欄位。", En: "This record includes the field “%s”."})
)
