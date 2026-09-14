package i18n

var (
	KeyTagline = key("site.tagline", Message{
		ZhHant: "台灣的 3C 店",
		En:     "A 3C shop in Taiwan",
	})

	KeySiteTitle = key("site.title", Message{
		ZhHant: "goen",
		En:     "goen",
	})

	KeyFooterTagline = key("site.footer.tagline", Message{
		ZhHant: "退換貨與保固說明在政策頁。",
		En:     "Returns and warranty terms are on the policy pages.",
	})

	KeySupportHours = key("site.hours", Message{
		ZhHant: "客服時間 週一至週五 09:00–18:00",
		En:     "Support Monday to Friday, 09:00–18:00",
	})

	KeyCategoryNav = key("nav.categories", Message{ZhHant: "商品分類", En: "Categories"})

	KeyCloseBanner = key("site.banner.close", Message{ZhHant: "關閉公告", En: "Dismiss"})

	KeyPageNotFound = key("site.notfound", Message{ZhHant: "找不到這個頁面", En: "Page not found"})

	KeyPageNotFoundTitle = key("site.notfound.title", Message{
		ZhHant: "找不到頁面",
		En:     "Not found",
	})

	KeyPageNotFoundBody = key("site.notfound.body", Message{
		ZhHant: "這個網址目前沒有對應的內容。看看店裡其他的東西吧。",
		En:     "Nothing lives at this address. Have a look at what else there is.",
	})
)
