package i18n

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
