package i18n

var (
	KeyHomeTitle = key("home.title", Message{
		ZhHant: "goen — 3C 選物",
		En:     "goen — curated 3C",
	})

	KeyHomeDescription = key("home.description", Message{
		ZhHant: "goen 精選手機、筆電、平板、耳機與周邊配件。台灣出貨,原廠保固。",
		En: "Phones, laptops, tablets, headphones and accessories, chosen rather than " +
			"listed. Ships from Taiwan with the manufacturer's warranty.",
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
		ZhHant: "全機種原廠保固,維修免費到府收送,保固可以線上登錄查詢。",
		En: "Every model carries its manufacturer's warranty. We collect repairs from your " +
			"door for free, and you can register and check your cover online.",
	})

	KeyTrustShippingBody = key("home.trust.shipping", Message{
		ZhHant: "宅配與超商取貨皆適用;未達門檻運費 NT$60 起。",
		En: "Home delivery and store pickup alike. Below the threshold, delivery is from " +
			"NT$60.",
	})

	KeyTrustReturnsBody = key("home.trust.returns", Message{
		ZhHant: "線上申請、宅配回收,退款 3–5 個工作天入帳。",
		En: "Request it online, we collect it, and the refund lands in 3 to 5 working " +
			"days.",
	})

	KeyTrustPayment = key("home.trust.payment", Message{ZhHant: "付款安全", En: "Secure payment"})

	KeyTrustPaymentBody = key("home.trust.payment.body", Message{
		ZhHant: "Stripe 加密金流,卡號不經過 goen 伺服器。",
		En:     "Stripe handles the card. Your number never touches a goen server.",
	})

	KeyHeroEyebrow = key("home.hero.eyebrow", Message{
		ZhHant: "台灣出貨 · 原廠保固",
		En:     "Ships from Taiwan · manufacturer's warranty",
	})

	KeyHeroHeadline = key("home.hero.headline", Message{
		ZhHant: "挑一台好的,值得。",
		En:     "A good one is worth choosing.",
	})

	KeyHeroBody = key("home.hero.body", Message{
		ZhHant: "goen 精選手機、筆電、平板與耳機周邊——把難挑的東西,挑好給你。",
		En: "Phones, laptops, tablets and audio, chosen rather than listed — the hard " +
			"decisions made for you.",
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
