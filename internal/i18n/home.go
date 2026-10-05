package i18n

var (
	// %s is the root categories as one list: a list written here is one the
	// shop's departments stop agreeing with.
	KeyHomeDescription = key("home.description", Message{
		ZhHant: "goen 販售%s。",
		En:     "goen sells %s.",
	})

	KeyCannotLoadHome = key("error.cannotload.home", Message{
		ZhHant: "商店首頁暫時無法顯示，請稍後再試。",
		En:     "We cannot show the home page right now. Please try again shortly.",
	})

	KeySectionCategories = key("home.categories", Message{ZhHant: "依館別選購", En: "Shop by department"})

	KeySectionTrust = key("home.trust", Message{ZhHant: "購物保障", En: "Shopping with goen"})

	KeyTrustWarrantyBody = key("home.trust.warranty", Message{
		ZhHant: "保固期內送修來回免運，保固在訂單頁登錄。",
		En:     "Repairs under warranty travel free both ways. Register the warranty from your order page.",
	})

	// %s is the lowest fee, interpolated from shipping_method_versions: a literal
	// here is a promise that stops agreeing with what checkout charges.
	KeyTrustShippingBody = key("home.trust.shipping", Message{
		ZhHant: "宅配與超商取貨皆適用；未達門檻運費 %s 起。",
		En:     "Home delivery or store pickup. Below that, delivery is from %s.",
	})

	KeyTrustShippingHomeBody = key("home.trust.shipping.home", Message{
		ZhHant: "未達門檻運費 %s 起。",
		En:     "Below that, delivery is from %s.",
	})

	// A numbered landing window here is a second SLA next to /returns, which
	// leaves the day to the card issuer.
	KeyTrustReturnsBody = key("home.trust.returns", Message{
		ZhHant: "在訂單頁申請退貨，退貨運費由 goen 負擔。",
		En:     "Start a return from your order page. We pay the postage.",
	})

	KeyTrustPayment = key("home.trust.payment", Message{ZhHant: "付款安全", En: "Secure payment"})

	KeyTrustPaymentBody = key("home.trust.payment.body", Message{
		ZhHant: "刷卡由 Stripe 處理，卡號不經過 goen。",
		En:     "Stripe handles the card. Your number never touches a goen server.",
	})

	KeyHeroHeadline = key("home.hero.headline", Message{
		ZhHant: "goen 販售的商品",
		En:     "What goen sells",
	})

	KeyHomeSeeAll = key("home.see_all", Message{ZhHant: "看全部", En: "See all"})

	KeyHomeFeatured = key("home.featured", Message{ZhHant: "精選", En: "Featured"})

	KeyHomeHeading = key("home.heading", Message{ZhHant: "goen 商店首頁", En: "goen shop home"})

	KeyHomeNewIn = key("home.new_in", Message{ZhHant: "新到貨", En: "New in"})

	KeyHeroPrevious = key("home.hero.previous", Message{ZhHant: "上一張", En: "Previous"})

	KeyHeroNext = key("home.hero.next", Message{ZhHant: "下一張", En: "Next"})

	// %d is the campaign's product count, %s the day it ends.
	KeyHomeCampaignFact = countKey("home.campaign.fact", "%d 件商品 · 至 %s",
		"%d item · until %s", "%d items · until %s")

	// What joins a section's name to its grey continuation on the same line.
	KeyHomeAside = key("home.heading.aside", Message{ZhHant: " · ", En: ". "})

	// The campaign row's continuation: %d is the product count, %s the last day.
	KeyHomeCampaignRowFact = countKey("home.campaign.row_fact", "%d 件商品，至 %s",
		"%d item, until %s", "%d items, until %s")

	// A day said the short way. The arguments are the English month name, the
	// month number, the day and the year, picked by index; the year forms are
	// for a day outside the shop's current year.
	KeyShortDate = key("date.short", Message{ZhHant: "%[2]d 月 %[3]d 日", En: "%[1]s %[3]d"})

	KeyShortDateYear = key("date.short.year", Message{ZhHant: "%[4]d 年 %[2]d 月 %[3]d 日", En: "%[1]s %[3]d, %[4]d"})

	// %s is a department's name.
	KeyHomeDepartmentCTA = key("home.department.cta", Message{ZhHant: "逛逛%s", En: "Browse %s"})

	KeyHeroCampaignCTA = key("home.hero.cta.campaign", Message{
		ZhHant: "逛逛活動",
		En:     "See the campaign",
	})
)
