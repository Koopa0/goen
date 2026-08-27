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
