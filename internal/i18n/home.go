package i18n

var (
	// %s is the root categories as one list: a list written here is one the
	// shop's departments stop agreeing with.
	KeyHomeDescription = key("home.description", Message{
		ZhHant: "goen 販售%s。",
		En:     "goen sells %s.",
	})

	KeyCannotLoadHome = key("error.cannotload.home", Message{
		ZhHant: "商店首頁暫時無法顯示,請稍後再試。",
		En:     "We cannot show the home page right now. Please try again shortly.",
	})

	KeySectionCategories = key("home.categories", Message{ZhHant: "依館別選購", En: "Shop by department"})

	KeySectionTrust = key("home.trust", Message{ZhHant: "購物保障", En: "Shopping with goen"})

	KeyTrustWarrantyBody = key("home.trust.warranty", Message{
		ZhHant: "有標示保固的商品,維修免費收送(宅配訂單到府收件,超商取貨的訂單由超商寄回),保固可以線上登錄查詢。",
		En: "For a product that states a warranty, repairs are carried both ways for free: " +
			"collected from your door for a home-delivery order, sent back from a " +
			"convenience store for a pickup order. You can register and check your cover online.",
	})

	// %s is the lowest fee, interpolated from shipping_method_versions: a literal
	// here is a promise that stops agreeing with what checkout charges.
	KeyTrustShippingBody = key("home.trust.shipping", Message{
		ZhHant: "宅配與超商取貨皆適用;未達門檻運費 %s 起。",
		En: "Home delivery and store pickup alike. Below the threshold, delivery is from " +
			"%s.",
	})

	KeyTrustShippingHomeBody = key("home.trust.shipping.home", Message{
		ZhHant: "未達門檻運費 %s 起。",
		En:     "Below the threshold, delivery is from %s.",
	})

	// A numbered landing window here is a second SLA next to /returns, which
	// leaves the day to the card issuer.
	KeyTrustReturnsBody = key("home.trust.returns", Message{
		ZhHant: "線上申請、宅配回收,實際入帳時間由發卡銀行決定,通常是數個工作天。",
		En: "Request it online, we collect it. When it lands is your card issuer's " +
			"decision, usually a few working days.",
	})

	KeyTrustPayment = key("home.trust.payment", Message{ZhHant: "付款安全", En: "Secure payment"})

	KeyTrustPaymentBody = key("home.trust.payment.body", Message{
		ZhHant: "Stripe 加密金流,卡號不經過 goen 伺服器。",
		En:     "Stripe handles the card. Your number never touches a goen server.",
	})

	KeyHeroHeadline = key("home.hero.headline", Message{
		ZhHant: "goen 販售的商品",
		En:     "What goen sells",
	})

	KeyHomeSeeAll = key("home.see_all", Message{ZhHant: "看全部", En: "See all"})

	KeyHomePromoTitle = key("home.promo.title", Message{ZhHant: "書桌上的日常", En: "Everyday things for the desk"})

	KeyHomePromoLink = key("home.promo.link", Message{ZhHant: "去看看", En: "Take a look"})

	KeyHomeFeatured = key("home.featured", Message{ZhHant: "精選", En: "Featured"})

	KeyHomeHeading = key("home.heading", Message{ZhHant: "goen 商店首頁", En: "goen shop home"})

	KeyHomeNewIn = key("home.new_in", Message{ZhHant: "新到貨", En: "New in"})

	KeyHeroPrevious = key("home.hero.previous", Message{ZhHant: "上一張", En: "Previous"})

	KeyHeroNext = key("home.hero.next", Message{ZhHant: "下一張", En: "Next"})

	// %d is the campaign's product count, %s the day it ends.
	KeyHomeCampaignFact = countKey("home.campaign.fact", "%d 件商品 · 至 %s",
		"%d item · until %s", "%d items · until %s")

	// %s is a department's name.
	KeyHomeDepartmentCTA = key("home.department.cta", Message{ZhHant: "逛逛%s", En: "Browse %s"})

	KeyHeroCampaignCTA = key("home.hero.cta.campaign", Message{
		ZhHant: "逛逛活動",
		En:     "See the campaign",
	})
)
