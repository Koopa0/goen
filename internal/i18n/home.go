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

	// KeyUnitMonths counts months wherever a term is stated: the number first, then the unit.
	KeyUnitMonths = countKey("unit.months", "%d 個月", "%d month", "%d months")

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
		En:     "Home delivery or store pickup; below it, from %s",
	})

	KeyRuleFreeDeliveryHomeNote = key("home.rules.delivery.home.note", Message{
		ZhHant: "未達門檻運費 %s 起",
		En:     "Below it, delivery is from %s",
	})

	KeyHeroHeadline = key("home.hero.headline", Message{
		ZhHant: "goen 販售的商品",
		En:     "What goen sells",
	})

	KeyHomeFeatured = key("home.featured", Message{ZhHant: "精選", En: "Featured"})

	KeyHomeHeading = key("home.heading", Message{ZhHant: "goen 商店首頁", En: "goen shop home"})

	KeyHomeNewIn = key("home.new_in", Message{ZhHant: "新到貨", En: "New in"})

	KeyHeroPrevious = key("home.hero.previous", Message{ZhHant: "上一張", En: "Previous"})

	KeyHeroNext = key("home.hero.next", Message{ZhHant: "下一張", En: "Next"})

	// A day said the short way. The arguments are the English month name, the
	// month number, the day and the year, picked by index; the year forms are
	// for a day outside the shop's current year.
	KeyShortDate = key("date.short", Message{ZhHant: "%[2]d\u00a0月 %[3]d\u00a0日", En: "%[1]s\u00a0%[3]d"})

	KeyShortDateYear = key("date.short.year", Message{ZhHant: "%[4]d\u00a0年 %[2]d\u00a0月 %[3]d\u00a0日", En: "%[1]s\u00a0%[3]d, %[4]d"})

	// A day on a grid's end: the English month name, the month number and the day.
	KeyDateLabel = key("date.label", Message{ZhHant: "%[2]d/%[3]d", En: "%[1]s\u00a0%[3]d"})

	// %d is the count of what the link lists.
	KeyHomeSeeAllCount = countKey("home.see_all.count", "看全部 %d 件", "See all %d item", "See all %d items")

	// The link under New in, which lists products and is no campaign.
	KeyHomeNewInCTA = key("home.row.newin.cta", Message{ZhHant: "看全部商品", En: "See all products"})

	KeyHeroCampaignCTA = key("home.hero.cta.campaign", Message{
		ZhHant: "看全部商品",
		En:     "See all items",
	})

	KeySlideItems = key("home.slide.items", Message{ZhHant: "商品", En: "Items"})

	KeySlideCategories = key("home.slide.categories", Message{ZhHant: "分類", En: "Categories"})

	KeySlideEnds = key("home.slide.ends", Message{ZhHant: "結束", En: "Ends"})

	KeySlideDaysLeft = key("home.slide.days_left", Message{ZhHant: "剩餘", En: "Days left"})

	// A fact's label already names what is counted, so English writes the bare figure and only zh-Hant adds a counter.
	KeyFactUnitItems      = zhOnly("fact.unit.items", "件")
	KeyFactUnitCategories = zhOnly("fact.unit.categories", "類")
	KeyFactUnitDays       = zhOnly("fact.unit.days", "天")

	KeyEndsTomorrow = key("home.slide.ends_tomorrow", Message{ZhHant: "明天結束", En: "Ends tomorrow"})

	KeyEndsToday = key("home.slide.ends_today", Message{ZhHant: "今天結束", En: "Ends today"})

	KeySlides = key("home.hero.slides", Message{ZhHant: "選擇焦點", En: "Choose a slide"})

	KeyPeriodToday = key("period.today", Message{ZhHant: "今天", En: "Today"})

	// The arguments of every period sentence are the title, the first day, the
	// last day and the number of days; then what the reader needs of today.
	KeyPeriodRunning = countKey("period.running",
		"%s：%s至 %s，共 %d 天；今天 %s是第 %d 天，還有 %d 天。",
		"%s: %s to %s, %d day; today, %s, is day %d, with %d days left.",
		"%s: %s to %s, %d days; today, %s, is day %d, with %d days left.")

	KeyPeriodEndsTomorrow = countKey("period.ends_tomorrow",
		"%s：%s至 %s，共 %d 天；今天 %s是第 %d 天，明天結束。",
		"%s: %s to %s, %d day; today, %s, is day %d, ending tomorrow.",
		"%s: %s to %s, %d days; today, %s, is day %d, ending tomorrow.")

	KeyPeriodEndsToday = countKey("period.ends_today",
		"%s：%s至 %s，共 %d 天；今天 %s是第 %d 天，今天結束。",
		"%s: %s to %s, %d day; today, %s, is day %d, ending today.",
		"%s: %s to %s, %d days; today, %s, is day %d, ending today.")

	KeyPeriodNotStarted = countKey("period.not_started",
		"%s：%s至 %s，共 %d 天；還沒開始，今天是 %s。",
		"%s: %s to %s, %d day; not started yet, today is %s.",
		"%s: %s to %s, %d days; not started yet, today is %s.")

	KeyPeriodEnded = countKey("period.ended",
		"%s：%s至 %s，共 %d 天；已結束。",
		"%s: %s to %s, %d day; ended.",
		"%s: %s to %s, %d days; ended.")
)
