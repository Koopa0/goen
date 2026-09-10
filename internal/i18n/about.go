package i18n

var (
	KeyAboutTitle = key("about.title", Message{ZhHant: "關於我們", En: "About us"})

	KeyAboutDescription = key("about.description", Message{
		ZhHant: "goen 是規格看得懂、保固靠得住的 3C 選品店。每件商品都先過我們自己的比較表。",
		En: "goen is a 3C shop where the specifications make sense and the warranty holds. " +
			"Everything on it has been compared and tested first.",
	})

	KeyAboutName = key("about.name", Message{
		ZhHant: "goen 的名字有三層:Go 是我們寫後端的語言;ご縁(go-en)是日文裡人與物之間的緣分;五円(同音)是日本神社裡討吉利的那枚硬幣。三件事說的是同一件事 — 買 3C 不該是運氣,而是被好好安排的相遇。",
		En: "The name carries three things at once. Go is the language the back end is " +
			"written in; ご縁 (go-en) is the Japanese word for the tie between a person " +
			"and a thing; and 五円, which sounds the same, is the coin people offer at a " +
			"Japanese shrine for luck. All three say one thing — buying 3C should not be " +
			"a matter of luck, but a meeting somebody arranged properly.",
	})

	KeyAboutSelection = key("about.selection", Message{
		ZhHant: "我們不做「什麼都賣」。每一件上架商品都經過規格比對與實測,規格表寫到你看得懂為止;價格含稅、保固寫清楚、退換貨不藏條款。比到眼花的事我們先做完,你只要選。",
		En: "We do not sell everything. Each product is compared and tested before it goes " +
			"up, and its spec table is written out until it makes sense. Prices include " +
			"tax, the warranty is stated plainly, and there are no buried clauses in the " +
			"returns policy. The comparing until your eyes hurt is our job; you only have " +
			"to choose.",
	})

	KeyAboutSpecs = key("about.specs", Message{ZhHant: "規格透明", En: "Specifications in full"})

	KeyAboutSpecsBody = key("about.specs.body", Message{
		ZhHant: "每件商品都有完整 spec table,同系列可逐欄比較。",
		En:     "Every product has a complete spec table, and a range compares column by column.",
	})

	KeyAboutWarranty = key("about.warranty", Message{ZhHant: "保固靠得住", En: "A warranty that holds"})

	KeyAboutWarrantyBody = key("about.warranty.body", Message{
		ZhHant: "原廠保固線上登錄,維修免費到府收送。",
		En:     "Register the manufacturer's warranty online; repairs are collected for free.",
	})

	KeyAboutCurated = key("about.curated", Message{ZhHant: "精選不灌水", En: "Chosen, not padded"})

	KeyAboutCuratedBody = key("about.curated.body", Message{
		ZhHant: "上架前先過我們自己的比較表,賣不過同價位的不上。",
		En: "Everything goes through our own comparison table first. If it loses to " +
			"something at the same price, it does not go up.",
	})
)
