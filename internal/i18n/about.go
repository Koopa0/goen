package i18n

var (
	KeyAboutTitle = key("about.title", Message{ZhHant: "關於我們", En: "About us"})

	KeyAboutDescription = key("about.description", Message{
		ZhHant: "goen 賣手機、筆電、平板、耳機、穿戴裝置與配件。商品頁有規格表。",
		En: "goen sells phones, laptops, tablets, headphones, wearables and accessories. " +
			"Product pages include a spec table.",
	})

	KeyAboutName = key("about.name", Message{
		ZhHant: "名字有三層:Go 是後端用的語言;ご縁(go-en)是日文裡人與物的緣;五円同音,是日本神社常見的硬幣。",
		En: "The name stacks three facts. Go is the language of the back end; ご縁 (go-en) " +
			"is the Japanese word for a tie between a person and a thing; 五円, which " +
			"sounds the same, is a coin often left at a Japanese shrine.",
	})

	KeyAboutSelection = key("about.selection", Message{
		ZhHant: "商品頁有規格表。保固期限以該頁為準。訂單頁可以申請退貨。",
		En: "Each product page has a spec table and states that item's warranty term. " +
			"Request a return from the order page.",
	})

	KeyAboutSpecs = key("about.specs", Message{ZhHant: "規格表", En: "Spec tables"})

	KeyAboutSpecsBody = key("about.specs.body", Message{
		ZhHant: "每件商品都有完整 spec table,同系列可逐欄比較。",
		En:     "Every product has a complete spec table, and a range compares column by column.",
	})

	KeyAboutWarranty = key("about.warranty", Message{ZhHant: "原廠保固", En: "Manufacturer warranty"})

	KeyAboutWarrantyBody = key("about.warranty.body", Message{
		ZhHant: "原廠保固可在訂單頁登錄。送修時由 goen 安排到府收件,收送費用由店家負擔。",
		En: "Register the manufacturer's warranty from the order page. goen arranges " +
			"collection and pays carriage both ways.",
	})

	KeyAboutCurated = key("about.curated", Message{ZhHant: "商品分類", En: "Categories"})

	KeyAboutCuratedBody = key("about.curated.body", Message{
		ZhHant: "手機、筆電、平板、耳機、穿戴裝置與配件。",
		En:     "Phones, laptops, tablets, headphones, wearables and accessories.",
	})
)
