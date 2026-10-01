package i18n

var (
	KeyAboutTitle = key("about.title", Message{ZhHant: "關於我們", En: "About us"})

	// %s is the root categories as one list, as in home.description.
	KeyAboutDescription = key("about.description", Message{
		ZhHant: "goen 的商品分類:%s。",
		En:     "Categories at goen: %s.",
	})

	KeyAboutName = key("about.name", Message{
		ZhHant: "goen 這個名字取自日文的「ご縁」,意思是人與物之間的緣分。",
		En:     "The name comes from the Japanese ご縁 (go-en): the bond between a person and a thing.",
	})

	KeyAboutSelection = key("about.selection", Message{
		ZhHant: "商品頁列出店家對每件商品標示的資訊,有標示保固的商品也寫明保固期限。訂單頁可以申請退貨。",
		En: "Each product page lists what the shop states about that item, including the " +
			"warranty term where one is stated. Request a return from the order page.",
	})

	KeyAboutSpecs = key("about.specs", Message{ZhHant: "規格表", En: "Spec tables"})

	KeyAboutSpecsBody = key("about.specs.body", Message{
		ZhHant: "規格逐項列在商品頁,幾件商品可以放進比較頁逐列對照。",
		En:     "Specs are listed item by item on the product page, and the compare page sets several products side by side.",
	})

	KeyAboutWarranty = key("about.warranty", Message{ZhHant: "原廠保固", En: "Manufacturer warranty"})

	KeyAboutWarrantyBody = key("about.warranty.body", Message{
		ZhHant: "有標示保固的商品,原廠保固可在訂單頁登錄。送修時,宅配訂單由 goen 安排到府收件,超商取貨的訂單請由超商寄回;兩種訂單的收送費用都由店家負擔。",
		En: "For a product that states a warranty, register the manufacturer's warranty from the order " +
			"page. For a home-delivery order goen arranges collection from your door, and a " +
			"convenience-store pickup order is sent back from a convenience store; goen pays " +
			"carriage both ways in either case.",
	})

	KeyAboutCurated = key("about.curated", Message{ZhHant: "商品分類", En: "Categories"})
)

var (
	KeyAboutImageAlt = key("about.image_alt", Message{ZhHant: "goen 的工作台與商品", En: "goen's workbench and products"})
	KeyOGImageAlt    = key("og.image_alt", Message{
		ZhHant: "平板、手機、筆電、無線耳機與耳罩式耳機,放在淺灰色背景上",
		En:     "A tablet, a phone, a laptop, wireless earbuds and over-ear headphones on a pale grey background",
	})
)
