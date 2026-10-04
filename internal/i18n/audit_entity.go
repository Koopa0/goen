package i18n

// The record kinds /admin/audit names in a row's subject line.
var (
	KeyAuditEntityOrders = key("audit.entity.orders", Message{ZhHant: "訂單", En: "Order"})

	KeyAuditEntityOrderPrivateData = key("audit.entity.order_private_data", Message{ZhHant: "訂單收件資料", En: "Order delivery details"})

	KeyAuditEntityPayments = key("audit.entity.payments", Message{ZhHant: "付款", En: "Payment"})

	KeyAuditEntityPaymentWebhookEvents = key("audit.entity.payment_webhook_events", Message{ZhHant: "付款通知", En: "Payment notice"})

	KeyAuditEntityRefunds = key("audit.entity.refunds", Message{ZhHant: "退款", En: "Refund"})

	KeyAuditEntityReturnRequests = key("audit.entity.return_requests", Message{ZhHant: "退貨", En: "Return"})

	KeyAuditEntityStoreCreditEntries = key("audit.entity.store_credit_entries", Message{ZhHant: "購物金", En: "Store credit"})

	KeyAuditEntityUsers = key("audit.entity.users", Message{ZhHant: "帳號", En: "Account"})

	KeyAuditEntityBrands = key("audit.entity.brands", Message{ZhHant: "品牌", En: "Brand"})

	KeyAuditEntityCategories = key("audit.entity.categories", Message{ZhHant: "分類", En: "Category"})

	KeyAuditEntityContactMessages = key("audit.entity.contact_messages", Message{ZhHant: "聯絡訊息", En: "Contact message"})

	KeyAuditEntityCoupons = key("audit.entity.coupons", Message{ZhHant: "優惠碼", En: "Coupon"})

	KeyAuditEntityFaqEntries = key("audit.entity.faq_entries", Message{ZhHant: "常見問題", En: "FAQ entry"})

	KeyAuditEntityHeroSlides = key("audit.entity.hero_slides", Message{ZhHant: "首頁輪播", En: "Home slide"})

	KeyAuditEntityMembershipTiers = key("audit.entity.membership_tiers", Message{ZhHant: "會員等級", En: "Membership tier"})

	KeyAuditEntityProductAnswers = key("audit.entity.product_answers", Message{ZhHant: "商品回答", En: "Product answer"})

	KeyAuditEntityProductImages = key("audit.entity.product_images", Message{ZhHant: "商品圖片", En: "Product image"})

	KeyAuditEntityProductOptionValues = key("audit.entity.product_option_values", Message{ZhHant: "選項值", En: "Option value"})

	KeyAuditEntityProductOptions = key("audit.entity.product_options", Message{ZhHant: "商品選項", En: "Product option"})

	KeyAuditEntityProductQuestions = key("audit.entity.product_questions", Message{ZhHant: "商品提問", En: "Product question"})

	KeyAuditEntityProductReviews = key("audit.entity.product_reviews", Message{ZhHant: "商品評價", En: "Product review"})

	KeyAuditEntityProductSpecs = key("audit.entity.product_specs", Message{ZhHant: "商品規格表", En: "Product specification"})

	KeyAuditEntityProductVariants = key("audit.entity.product_variants", Message{ZhHant: "規格", En: "Variant"})

	KeyAuditEntityProducts = key("audit.entity.products", Message{ZhHant: "商品", En: "Product"})

	KeyAuditEntityPromoBanners = key("audit.entity.promo_banners", Message{ZhHant: "促銷橫幅", En: "Promo banner"})

	KeyAuditEntitySaleCampaignProducts = key("audit.entity.sale_campaign_products", Message{ZhHant: "檔期商品", En: "Campaign product"})

	KeyAuditEntitySaleCampaigns = key("audit.entity.sale_campaigns", Message{ZhHant: "檔期", En: "Sale campaign"})

	KeyAuditEntityShippingMethodVersions = key("audit.entity.shipping_method_versions", Message{ZhHant: "運送方式版本", En: "Shipping method version"})

	KeyAuditEntityShippingMethods = key("audit.entity.shipping_methods", Message{ZhHant: "運送方式", En: "Shipping method"})

	KeyAuditEntityShippingVersionZones = key("audit.entity.shipping_version_zones", Message{ZhHant: "運費區域設定", En: "Shipping zone setting"})

	KeyAuditEntityShippingZonePrefixes = key("audit.entity.shipping_zone_prefixes", Message{ZhHant: "區域郵遞區號", En: "Zone postcode prefix"})

	KeyAuditEntityShippingZones = key("audit.entity.shipping_zones", Message{ZhHant: "運費區域", En: "Shipping zone"})
)
