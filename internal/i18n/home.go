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

	KeyHeroPlaceholder = key("home.hero.placeholder", Message{
		ZhHant: "主視覺待實拍素材",
		En:     "Hero photography pending",
	})

	KeySectionCategories = key("home.categories", Message{ZhHant: "分類選購", En: "Shop by category"})

	KeySectionRecommended = key("home.recommended", Message{
		ZhHant: "綜合推薦",
		En:     "Recommended",
	})

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

	KeyHeroPrimaryCTA = key("home.hero.cta.primary", Message{
		ZhHant: "看本週優惠",
		En:     "This week's offers",
	})

	KeyHeroSecondaryCTA = key("home.hero.cta.secondary", Message{
		ZhHant: "關於 goen",
		En:     "About goen",
	})
)
