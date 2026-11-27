package i18n

var (
	// %s is the root categories as one list: a list written here is one the
	// shop's departments stop agreeing with.
	KeyHomeDescription = key("home.description", Message{
		ZhHant: "涵蓋%s的雙語線上商店，規格、保固與運費都寫在明處。",
		En:     "A bilingual shop spanning %s, with specifications, warranty and delivery costs stated in the open.",
	})

	KeyCannotLoadHome = key("error.cannotload.home", Message{
		ZhHant: "商店首頁暫時無法顯示，請稍後再試。",
		En:     "We cannot show the home page right now. Please try again shortly.",
	})

	KeySectionCategories = key("home.categories", Message{ZhHant: "館別", En: "Departments"})

	KeySectionRules = key("home.rules", Message{ZhHant: "購物規則", En: "Shop rules"})

	KeyRulePayStart = key("home.rules.pay_start", Message{ZhHant: "付款時限", En: "Time to pay"})

	KeyRuleUnitMinutes = key("home.rules.unit.minutes", Message{ZhHant: "分鐘", En: "min"})

	KeyRulePayStartNote = key("home.rules.pay_start.note", Message{
		ZhHant: "送出訂單起算；商品保留到付款完成",
		En:     "From placing the order; the goods are held until payment is complete",
	})

	KeyRuleRescission = key("home.rules.rescission", Message{ZhHant: "猶豫期", En: "Right to cancel"})

	// KeyUnitMonths counts months wherever a term is stated: the number first, then the unit.
	KeyUnitMonths = countKey("unit.months", "%d 個月", "%d month", "%d months")

	KeyRuleUnitDays = key("home.rules.unit.days", Message{ZhHant: "天", En: "days"})

	KeyRuleRescissionNote = key("home.rules.rescission.note", Message{
		ZhHant: "收到次日起算；退貨運費由 goen 負擔",
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
		En:     "Home delivery or store pickup; under that, delivery from %s",
	})

	KeyRuleFreeDeliveryHomeNote = key("home.rules.delivery.home.note", Message{
		ZhHant: "未達門檻運費 %s 起",
		En:     "Below it, delivery is from %s",
	})

	// The bento hero's dark tile, the page's heading: the shop's own sentence,
	// which says what it is and not what one department sells.
	KeyHeroHeadline = key("home.hero.headline", Message{
		ZhHant: "旗艦硬體與日常器物，同一個標準。",
		En:     "Flagship hardware and quiet everyday goods, one roof.",
	})

	// KeyHeroLede counts the products the shop lists now: a number written
	// here would stop agreeing with the catalogue the day a product is added.
	KeyHeroLede = countKey("home.hero.lede",
		"全館 %d 件商品，規格與運費都寫在明處。",
		"%d product, its specifications and delivery cost in plain sight.",
		"%d products, their specifications and delivery costs in plain sight.")

	// The line over the headline: the largest department and what it holds.
	KeyHeroEyebrow = countKey("home.hero.eyebrow", "%[2]s · %[1]d 件商品", "%[2]s · %[1]d product", "%[2]s · %[1]d products")

	// The way into the largest department, with how many products it holds.
	KeyHeroShop = key("home.hero.shop", Message{ZhHant: "探索 %s（%d）", En: "Shop %s (%d)"})

	KeyHeroCompare = key("home.hero.compare", Message{ZhHant: "並排比較規格", En: "Compare side by side"})

	// What the lifestyle tile says over a running campaign and a department:
	// how many products the link lists.
	KeyHomeTileRunning = key("home.tile.running", Message{ZhHant: "進行中的活動", En: "Running now"})

	KeyHomeTileItems = countKey("home.tile.items", "%d 件商品", "%d product", "%d products")

	// The quick index: where to start, and the free-delivery threshold the
	// shop states, read from the delivery methods.
	KeyHomeIndex = key("home.index", Message{ZhHant: "快速索引", En: "Quick index"})

	KeyHomeIndexFree = key("home.index.free", Message{ZhHant: "快速索引 · 滿 %s 免運", En: "Quick index · free delivery over %s"})

	// %s is the rail's ceiling. 未滿 and "under": a product at the ceiling
	// itself is not on the rail.
	KeyHomeIndexUnder = key("home.index.under", Message{ZhHant: "未滿 %s", En: "Under %s"})

	// The facts under the quick index, each read from where checkout and the
	// returns desk enforce it.
	KeyHomeFactFee = key("home.fact.fee", Message{ZhHant: "運費 %s 起", En: "Delivery from %s"})

	KeyHomeFactRescission = key("home.fact.rescission", Message{ZhHant: "猶豫期 %d 天", En: "%d-day right to cancel"})

	// The example under a slide's headline in the back office: a headline of
	// the length the field takes, which the home page's own does not fit.
	KeyAdminHomeHeadlineExample = key("admin.home.headline.example", Message{ZhHant: "茶與咖啡週", En: "Tea and coffee week"})

	KeyHomeFactReturn = countKey("home.fact.return", "未使用 %d 天內可退", "Unused returns within %d day", "Unused returns within %d days")

	// The featured department's shelf: its name, and its share of the shop.
	KeyHomeShelfTitle = key("home.shelf.title", Message{ZhHant: "%s主力貨架", En: "The %s shelf"})

	KeyHomeShelfLede = key("home.shelf.lede", Message{
		ZhHant: "全館 %[2]d 件商品中有 %[1]d 件在這個分館，卡片直接列出規格。",
		En:     "%[1]d of our %[2]d products, with their specifications on each card.",
	})

	KeyHomeShelfAll = key("home.shelf.all", Message{ZhHant: "全部", En: "All"})

	KeyHomeShelfFilter = key("home.shelf.filter", Message{ZhHant: "依分類瀏覽", En: "Browse by category"})

	// The dark stage: the two highest-priced products of the featured
	// department that can be bought. Price is all it claims about them.
	KeyHomeStageEyebrow = key("home.stage.eyebrow", Message{ZhHant: "旗艦現貨", En: "Flagship, in stock"})

	KeyHomeStageTitle = key("home.stage.title", Message{
		ZhHant: "%s館內價位最高的兩件現貨。",
		En:     "The two highest-priced pieces in %s you can buy today.",
	})

	KeyHomeStageSpecs = key("home.stage.specs", Message{ZhHant: "看完整規格", En: "See every specification"})

	// The rail of cheap products. %s is its ceiling, a merchandising constant
	// of the home page.
	KeyHomeRailTitle = key("home.rail.title", Message{ZhHant: "未滿 %s 的日常補給", En: "Everyday pieces under %s"})

	KeyHomeRailLede = key("home.rail.lede", Message{
		ZhHant: "不分館別，價格由低到高；只有一種款式的商品可以直接加入購物袋。",
		En:     "From every department, cheapest first; a product with one variant goes straight into your bag.",
	})

	// The other departments, one at a time.
	KeyHomeMoreTitle = key("home.more.title", Message{ZhHant: "其他分館", En: "More departments"})

	KeyHomeMoreTabs = key("home.more.tabs", Message{ZhHant: "選擇分館", En: "Choose a department"})

	// The product card's savings seal: what the struck price saves, in money,
	// shown only while a campaign strikes it.
	KeyCardSave = key("card.save", Message{ZhHant: "省 %s", En: "Save %s"})

	// A card's way to a product whose variants differ: the choice is made on
	// the product's own page.
	KeyCardChoose = key("card.choose", Message{ZhHant: "選擇選項", En: "Choose options"})

	// A day said the short way. The arguments are the English month name, the
	// month number, the day and the year, picked by index; the year forms are
	// for a day outside the shop's current year.
	KeyShortDate = key("date.short", Message{ZhHant: "%[2]d\u00a0月 %[3]d\u00a0日", En: "%[1]s\u00a0%[3]d"})

	KeyShortDateYear = key("date.short.year", Message{ZhHant: "%[4]d\u00a0年 %[2]d\u00a0月 %[3]d\u00a0日", En: "%[1]s\u00a0%[3]d, %[4]d"})

	// A day on a grid's end: the English month name, the month number and the day.
	KeyDateLabel = key("date.label", Message{ZhHant: "%[2]d/%[3]d", En: "%[1]s\u00a0%[3]d"})

	// %d is the count of what the link lists.
	KeyHomeSeeAllCount = countKey("home.see_all.count", "看全部 %d 件", "See all %d item", "See all %d items")

	KeySlideItems = key("home.slide.items", Message{ZhHant: "商品", En: "Items"})

	KeySlideEnds = key("home.slide.ends", Message{ZhHant: "結束", En: "Ends"})

	// A fact's label already names what is counted, so English writes the bare figure and only zh-Hant adds a counter.
	KeyFactUnitItems = zhOnly("fact.unit.items", "件")
	KeyFactUnitDays  = zhOnly("fact.unit.days", "天")

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
