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

	KeySectionCategories = key("home.categories", Message{ZhHant: "館別", En: "Departments"})

	KeySectionRules = key("home.rules", Message{ZhHant: "購物規則", En: "Shop rules"})

	KeyRuleHold = key("home.rules.hold", Message{ZhHant: "庫存保留", En: "Stock reservation"})

	KeyRuleUnitMinutes = key("home.rules.unit.minutes", Message{ZhHant: "分鐘", En: "min"})

	// %s is the minutes a customer has to start paying.
	KeyRuleHoldNote = key("home.rules.hold.note", Message{
		ZhHant: "送出訂單起算；%s 分鐘內開始付款",
		En:     "From placing the order; start paying within %s min",
	})

	KeyRuleRescission = key("home.rules.rescission", Message{ZhHant: "猶豫期", En: "Right to cancel"})

	KeyRuleUnitDays = key("home.rules.unit.days", Message{ZhHant: "天", En: "days"})

	KeyRuleRescissionNote = key("home.rules.rescission.note", Message{
		ZhHant: "收到次日起算，退貨運費由 goen 負擔",
		En:     "From the day after delivery; goen pays the return postage",
	})

	KeyRuleReturn = key("home.rules.return", Message{ZhHant: "未使用退貨", En: "Unused returns"})

	KeyRuleReturnNote = key("home.rules.return.note", Message{
		ZhHant: "收到次日起算；包裝配件齊全，運費自付",
		En:     "From the day after delivery; unused and complete, you pay the postage",
	})

	KeyRuleFreeDelivery = key("home.rules.delivery", Message{ZhHant: "免運門檻", En: "Free delivery"})

	// %s is the lowest fee, interpolated from shipping_method_versions: a literal
	// here is a promise that stops agreeing with what checkout charges.
	KeyRuleFreeDeliveryNote = key("home.rules.delivery.note", Message{
		ZhHant: "宅配與超商取貨；未達門檻運費 %s 起",
		En:     "Home or store pickup; below it from %s",
	})

	KeyRuleFreeDeliveryHomeNote = key("home.rules.delivery.home.note", Message{
		ZhHant: "未達門檻運費 %s 起",
		En:     "Below it, delivery is from %s",
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
	KeyShortDate = key("date.short", Message{ZhHant: "%[2]d\u00a0月 %[3]d\u00a0日", En: "%[1]s\u00a0%[3]d"})

	KeyShortDateYear = key("date.short.year", Message{ZhHant: "%[4]d\u00a0年 %[2]d\u00a0月 %[3]d\u00a0日", En: "%[1]s\u00a0%[3]d, %[4]d"})

	// %s is a department's name.
	KeyHomeDepartmentCTA = key("home.department.cta", Message{ZhHant: "逛逛%s", En: "Browse %s"})

	KeyHeroCampaignCTA = key("home.hero.cta.campaign", Message{
		ZhHant: "逛逛活動",
		En:     "See the campaign",
	})
)
