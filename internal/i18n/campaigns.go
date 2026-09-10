package i18n

var (
	KeyCampaignEyebrow = key("campaign.eyebrow", Message{ZhHant: "限時活動", En: "Limited-time offer"})

	KeyCampaignEndsAt = key("campaign.endsat", Message{ZhHant: "活動至 %s", En: "Until %s"})

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
		ZhHant: "%s — goen 限時優惠",
		En:     "%s — a limited-time offer from goen",
	})

	KeyCampaignProducts = key("campaign.products", Message{ZhHant: "%s 件商品", En: "%s products"})

	KeyDealsEmpty = key("deals.empty", Message{
		ZhHant: "目前沒有正在特價的商品。歡迎逛逛全部分類。",
		En:     "Nothing is on sale at the moment. Have a look through the categories.",
	})

	KeyEndsWithinHour = key("campaign.ends.soon", Message{
		ZhHant: "不到 1 小時",
		En:     "under an hour left",
	})

	KeyEndsInHours = key("campaign.ends.hours", Message{ZhHant: "剩 %d 小時", En: "%d hours left"})

	KeyEndsInDays = key("campaign.ends.days", Message{ZhHant: "剩 %d 天", En: "%d days left"})

	KeyCampaignNotFound = key("campaign.notfound", Message{ZhHant: "找不到這個活動", En: "Offer not found"})

	KeyCampaignNotFoundBody = key("campaign.notfound.body", Message{
		ZhHant: "這個活動可能已經結束了。看看目前的優惠。",
		En:     "That promotion has probably ended. Have a look at what is running now.",
	})

	KeyDeals2 = key("deals.eyebrow", Message{ZhHant: "優惠", En: "Offers"})

	KeyDealsTitle = key("deals.title", Message{ZhHant: "現正優惠", En: "On sale now"})

	KeyDealsCount = key("deals.count", Message{ZhHant: "%s 件商品正在特價", En: "%s products reduced"})
)

var (
	KeyFormSlugTakenCampaign = key("form.slug.taken.campaign", Message{
		ZhHant: "這個網址代稱已經有活動用了。",
		En:     "Another campaign already uses that slug.",
	})

	KeyAdminCampLead = key("admin.camp.lead", Message{
		ZhHant: "活動只能收錄有標示原價的商品。新活動一開始是空的,建立後再挑選商品。",
		En: "A campaign can only feature products that state an original price. " +
			"A new campaign starts empty; you choose its products after creating it.",
	})

	KeyAdminCampTitle = key("admin.camp.title", Message{ZhHant: "活動標題", En: "Campaign title"})

	KeyAdminCampTitleExample = key("admin.camp.title.example", Message{
		ZhHant: "夏季 3C 展",
		En:     "Summer electronics show",
	})

	KeyAdminCampDays = key("admin.camp.days", Message{ZhHant: "活動天數", En: "Days it runs"})

	KeyAdminCampCreate = key("admin.camp.create", Message{ZhHant: "建立活動", En: "Create the campaign"})

	KeyAdminCampCurrent = key("admin.camp.current", Message{ZhHant: "目前的活動", En: "Existing campaigns"})

	KeyAdminCampEmpty = key("admin.camp.empty", Message{ZhHant: "還沒有任何活動。", En: "No campaigns yet."})

	KeyAdminCampMeta = key("admin.camp.meta", Message{
		ZhHant: "%s 件商品 · 至 %s",
		En:     "%s products · until %s",
	})

	KeyAdminCampProductSlug = key("admin.camp.product.slug", Message{
		ZhHant: "商品網址代稱",
		En:     "Product slug",
	})

	KeyAdminCampProductHint = key("admin.camp.product.hint", Message{
		ZhHant: "商品必須已經標示原價,否則無法加入。",
		En:     "The product must already state an original price, or it cannot be added.",
	})

	KeyAdminCampAdd = key("admin.camp.add", Message{ZhHant: "加入活動", En: "Add to the campaign"})

	KeyAdminCampProducts = key("admin.camp.products", Message{
		ZhHant: "活動商品",
		En:     "Products in this campaign",
	})

	KeyAdminCampNoProducts = key("admin.camp.noproducts", Message{
		ZhHant: "這個活動還沒有收錄商品,顧客看到的會是空頁面。",
		En:     "This campaign features nothing yet, so a customer would see an empty page.",
	})

	KeyFormCampaignTitle = key("form.campaign.title", Message{
		ZhHant: "請填寫活動標題,不超過 60 個字。",
		En:     "A campaign title is required, 60 characters at most.",
	})

	KeyFormCampaignDays = key("form.campaign.days", Message{
		ZhHant: "活動天數必須介於 1 到 90 天。",
		En:     "A campaign runs 1 to 90 days.",
	})

	KeyAdminPageCampaigns = key("admin.page.campaigns", Message{ZhHant: "限時活動", En: "Campaigns"})

	KeyAdminCampaignOff = key("admin.campaign.off", Message{ZhHant: "已停用", En: "Switched off"})

	KeyAdminCampaignOutside = key("admin.campaign.outside", Message{ZhHant: "不在期間內", En: "Outside its window"})

	KeyAdminCampaignEmpty = key("admin.campaign.empty", Message{
		ZhHant: "進行中(沒有商品)",
		En:     "Running (nothing featured)",
	})

	KeyAdminCampaignRunning = key("admin.campaign.running", Message{ZhHant: "進行中", En: "Running"})
)

var (
	KeyAdminNoticeNoDiscount = key("admin.notice.nodiscount", Message{
		ZhHant: "這個商品沒有標示原價,無法加入活動。先在商品頁設定原價再試一次。",
		En: "This product has no compare-at price, so nothing on it is marked down and a campaign " +
			"cannot feature it. Set one on the product page and try again.",
	})
)
