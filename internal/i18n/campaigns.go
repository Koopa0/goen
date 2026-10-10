package i18n

var (
	KeyCampaignsRunning = key("campaign.running", Message{ZhHant: "進行中的活動", En: "Running campaigns"})

	KeyCampaignEyebrow = key("campaign.eyebrow", Message{ZhHant: "活動", En: "Campaign"})

	KeyCampaignStarts = key("campaign.starts", Message{ZhHant: "開始", En: "Starts"})

	KeyCampaignEnded = key("campaign.ended", Message{ZhHant: "已結束", En: "Ended"})

	// What is left of a running campaign, said inside a sentence, so English starts in lower case.
	KeyCampaignDaysLeft = countKey("campaign.daysleft", "剩\u00a0%d\u00a0天", "%d day left", "%d days left")

	KeyCampaignEndsTomorrow = key("campaign.endstomorrow", Message{ZhHant: "明天結束", En: "ends tomorrow"})

	KeyCampaignEndsTomorrowAt = key("campaign.endstomorrowat", Message{ZhHant: "明天 %s 結束", En: "ends tomorrow at %s"})

	KeyCampaignEndsToday = key("campaign.endstoday", Message{ZhHant: "今天結束", En: "ends today"})

	KeyCampaignEndsTodayAt = key("campaign.endstodayat", Message{ZhHant: "今天 %s 結束", En: "ends today at %s"})

	// A day and the time of day on it, as one unit.
	KeyCampaignDayAt = key("campaign.dayat", Message{ZhHant: "%s %s", En: "%s at %s"})

	KeyCampaignEmpty = key("campaign.empty", Message{
		ZhHant: "這個活動目前沒有可購買的商品",
		En:     "Nothing in this promotion is available right now",
	})

	KeyCampaignEmptyHint = key("campaign.empty.hint", Message{
		ZhHant: "商品可能剛好都下架了。看看",
		En:     "Everything in it may have just sold out. Have a look at",
	})

	KeyCampaignEmptyLink = key("campaign.empty.link", Message{
		ZhHant: "其他優惠",
		En:     "the other offers",
	})

	KeyCampaignDescription = key("campaign.description", Message{
		ZhHant: "%s — goen 優惠",
		En:     "%s — deals at goen",
	})

	KeyCampaignProducts = countKey("campaign.products", "%s 件商品", "%s product", "%s products")

	KeyDealsEmpty = key("deals.empty", Message{
		ZhHant: "目前沒有正在特價的商品。歡迎逛逛全部分類。",
		En:     "Nothing is on sale at the moment. Have a look through the categories.",
	})

	// %s is the last day: after the product count on the offers page.
	KeyCampaignUntil = key("campaign.until", Message{ZhHant: "至 %s", En: "until %s"})

	// The notice's last day, followed in English by a pause a screen reader takes before the time left: the
	// comma after the date is hidden on screen, where a dot already parts the two.
	KeyCampaignNoticeUntil = key("campaign.notice.until", Message{ZhHant: "至 %s", En: "until %s,"})

	KeyCampaignNotFound = key("campaign.notfound", Message{ZhHant: "找不到這個活動", En: "Campaign not found"})

	KeyCampaignNotFoundBody = key("campaign.notfound.body", Message{
		ZhHant: "找不到這個活動。看看目前的優惠。",
		En:     "We could not find that campaign. See the deals running now.",
	})

	KeyDealsTitle = key("deals.title", Message{ZhHant: "優惠", En: "Deals"})

	KeyDealsCount = countKey("deals.count", "%s 件商品正在特價", "%s product reduced", "%s products reduced")

	KeyCurrentDeals = key("campaign.currentdeals", Message{ZhHant: "看目前的優惠", En: "See current deals"})
)

var (
	KeyFormSlugTakenCampaign = key("form.slug.taken.campaign", Message{
		ZhHant: "這個網址代稱已經有活動用了。",
		En:     "Another campaign already uses that slug.",
	})

	KeyAdminCampLead = key("admin.camp.lead", Message{
		ZhHant: "活動只能收錄有標示原價的商品。新活動一開始是空的，建立後再挑選商品。",
		En: "A campaign can only feature products that state an original price. " +
			"A new campaign starts empty; you choose its products after creating it.",
	})

	KeyAdminCampTitle = key("admin.camp.title", Message{ZhHant: "活動標題", En: "Campaign title"})

	KeyAdminCampTitleExample = key("admin.camp.title.example", Message{
		ZhHant: "夏季特賣",
		En:     "Summer sale",
	})

	KeyAdminCampDays = key("admin.camp.days", Message{ZhHant: "活動天數", En: "Days it runs"})

	KeyAdminCampCreate = key("admin.camp.create", Message{ZhHant: "建立活動", En: "Create the campaign"})

	KeyAdminCampCurrent = key("admin.camp.current", Message{ZhHant: "目前的活動", En: "Existing campaigns"})

	KeyAdminCampEmpty = key("admin.camp.empty", Message{ZhHant: "還沒有任何活動。", En: "No campaigns yet."})

	KeyAdminCampMeta = countKey("admin.camp.meta", "%s 件商品 · 至 %s", "%s product · until %s", "%s products · until %s")

	KeyAdminCampFind = key("admin.camp.find", Message{ZhHant: "搜尋商品", En: "Search products"})

	KeyAdminCampFindPlaceholder = key("admin.camp.findplaceholder", Message{
		ZhHant: "商品名稱或網址代稱",
		En:     "Product name or slug",
	})

	KeyAdminCampFindNone = key("admin.camp.findnone", Message{
		ZhHant: "找不到符合的商品，或它已經在這個活動裡。",
		En:     "No product matches, or it is already in this campaign.",
	})

	KeyAdminCampWindow = key("admin.camp.window", Message{ZhHant: "檔期", En: "Dates"})

	KeyAdminCampStarts = key("admin.camp.starts", Message{ZhHant: "開始時間", En: "Starts"})

	KeyAdminCampEnds = key("admin.camp.ends", Message{ZhHant: "結束時間", En: "Ends"})

	KeyFormCampaignWindow = key("form.campaign.window", Message{
		ZhHant: "請填入開始與結束時間，結束必須晚於開始。",
		En:     "Give a start and an end, the end after the start.",
	})

	KeyAdminCampProductHint = key("admin.camp.product.hint", Message{
		ZhHant: "商品必須已經標示原價，否則無法加入。",
		En:     "The product must already state an original price, or it cannot be added.",
	})

	KeyAdminCampImage = key("admin.camp.image", Message{ZhHant: "活動頁首圖片", En: "Campaign header image"})

	KeyAdminCampImageHint = key("admin.camp.imagehint", Message{
		ZhHant: "顯示在活動頁最上方，會從中央裁成 8:3。建議 1600×600。",
		En:     "Shown across the top of the campaign page, cropped from the centre to 8:3. 1600×600 works best.",
	})

	KeyAdminCampAdd = key("admin.camp.add", Message{ZhHant: "加入活動", En: "Add to the campaign"})

	KeyAdminCampProducts = key("admin.camp.products", Message{
		ZhHant: "活動商品",
		En:     "Products in this campaign",
	})

	KeyAdminCampNoProducts = key("admin.camp.noproducts", Message{
		ZhHant: "這個活動還沒有收錄商品，顧客看到的會是空頁面。",
		En:     "This campaign features nothing yet, so a customer would see an empty page.",
	})

	KeyFormCampaignTitle = key("form.campaign.title", Message{
		ZhHant: "請填寫活動標題，不超過 60 個字。",
		En:     "A campaign title is required, 60 characters at most.",
	})

	KeyFormCampaignDays = key("form.campaign.days", Message{
		ZhHant: "活動天數必須介於 1 到 90 天。",
		En:     "A campaign runs 1 to 90 days.",
	})

	KeyAdminPageCampaigns = key("admin.page.campaigns", Message{ZhHant: "活動", En: "Campaigns"})

	KeyAdminCampaignOff = key("admin.campaign.off", Message{ZhHant: "已停用", En: "Switched off"})

	KeyAdminCampaignOutside = key("admin.campaign.outside", Message{ZhHant: "不在期間內", En: "Outside its window"})

	KeyAdminCampaignHidden = key("admin.campaign.hidden", Message{
		ZhHant: "商店上看不到：沒有有庫存的優惠商品",
		En:     "Not shown in the shop: no product on sale is in stock",
	})

	KeyAdminCampaignRunning = key("admin.campaign.running", Message{ZhHant: "進行中", En: "Running"})
)

var (
	KeyAdminNoticeNoDiscount = key("admin.notice.nodiscount", Message{
		ZhHant: "這個商品沒有標示原價，無法加入活動。先在商品頁設定原價再試一次。",
		En: "This product has no compare-at price, so nothing on it is marked down and a campaign " +
			"cannot feature it. Set one on the product page and try again.",
	})
)

var KeyCampaignPagination = key("campaign.pagination", Message{ZhHant: "活動分頁", En: "Campaign pages"})

var (
	KeyAdminCampaignTitleEnLength = key("admin.campaign.title_en_length", Message{ZhHant: "英文活動標題不得超過 60 字。", En: "Use at most 60 characters for the English campaign title."})
	KeyAdminCampaignTitleEn       = key("admin.campaign.title_en", Message{ZhHant: "英文活動標題（選填）", En: "English campaign title (optional)"})
	KeyAdminCampaignTitleEnHint   = key("admin.campaign.title_en_hint", Message{ZhHant: "留白時，英文頁面會顯示原活動標題。", En: "Leave blank to show the original campaign title on English pages."})

	KeyAdminCampResults = key("admin.camp.results", Message{ZhHant: "成效", En: "Results"})

	// %[1]s is a number of days, %[2]s the units sold in the first of them since
	// the campaign began, %[3]s the units sold in as many days before it.
	KeyAdminCampFacts = key("admin.camp.facts", Message{
		ZhHant: "活動開始後的 %[1]s 售出 %[2]s；開始前的 %[1]s 售出 %[3]s。",
		En:     "%[2]s sold in the campaign's first %[1]s; %[3]s in the %[1]s before.",
	})

	// What the columns wait for: %[1]s is a number of days, %[2]s a number of units.
	KeyAdminCampFactsHint = key("admin.camp.facts.hint", Message{
		ZhHant: "活動滿 %[1]s、前後合計售出 %[2]s 後，這裡會畫出每日直條。",
		En:     "The daily columns appear once the campaign has run %[1]s and %[2]s have sold, before and during.",
	})

	KeyAdminCampResultsNote = countKey("admin.camp.results.note",
		"只計活動目前的 %d 件商品；改了商品清單，這些數字也跟著變。",
		"Counts the campaign's current %d product; change the list and these figures change with it.",
		"Counts the campaign's current %d products; change the list and these figures change with it.")

	// What the units are, once today is no longer counted.
	KeyAdminCampBasis = key("admin.camp.basis", Message{ZhHant: "只計入已付款的訂單，依下單時間。", En: "Paid orders only, by the time placed."})

	KeyAdminCampUnits = key("admin.camp.units", Message{ZhHant: "件數", En: "Units"})

	KeyAdminCampPeriod = key("admin.camp.period", Message{ZhHant: "時段", En: "Period"})

	// The days before a campaign began, which it is compared with.
	KeyAdminCampBefore = key("admin.camp.before", Message{ZhHant: "活動前", En: "Before"})
)
