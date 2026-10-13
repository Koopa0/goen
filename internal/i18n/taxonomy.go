package i18n

var (
	KeyFormSlugTakenBrand = key("form.slug.taken.brand", Message{
		ZhHant: "這個網址代稱已經有品牌用了。",
		En:     "Another brand already uses that slug.",
	})

	KeyFormSlugTakenCategory = key("form.slug.taken.category", Message{
		ZhHant: "這個網址代稱已經有分類用了。",
		En:     "Another category already uses that slug.",
	})

	KeyFormNameEnTooLong = key("form.name.en.toolong", Message{
		ZhHant: "英文名稱不超過 60 個字。",
		En:     "The English name is 60 characters at most.",
	})

	KeyFormParentMissing = key("form.parent.missing", Message{
		ZhHant: "找不到這個上層分類。",
		En:     "No such parent category.",
	})

	KeyFormIconUnknown = key("form.icon.unknown", Message{
		ZhHant: "請從清單中選一個圖示。",
		En:     "Pick an icon from the list.",
	})

	KeyAdminColIcon = key("admin.col.icon", Message{ZhHant: "圖示", En: "Icon"})

	KeyAdminIconNone = key("admin.icon.none", Message{ZhHant: "不設圖示", En: "No icon"})

	// KeyAdminTaxIconOf labels the icon control on one category's row.
	KeyAdminTaxIconOf = key("admin.tax.iconof", Message{
		ZhHant: "%s 的圖示",
		En:     "Icon for %s",
	})

	KeyAdminColTone = key("admin.col.tone", Message{ZhHant: "色調", En: "Tone"})

	// KeyAdminColComparable labels the department's answer to "compare products
	// here"; a sub-category shows no control, it takes its department's.
	KeyAdminColComparable = key("admin.col.comparable", Message{ZhHant: "開放商品比較", En: "Offer comparison"})

	// KeyAdminTaxComparableOf labels the comparison control on one department's row.
	KeyAdminTaxComparableOf = key("admin.tax.comparableof", Message{
		ZhHant: "%s 開放商品比較",
		En:     "Offer comparison in %s",
	})

	// KeyAdminToneInherit is the empty choice: the category takes its department's.
	KeyAdminTonePaper = key("admin.tone.paper", Message{ZhHant: "紙白", En: "Paper"})
	KeyAdminToneStone = key("admin.tone.stone", Message{ZhHant: "石白", En: "Stone"})
	KeyAdminToneMist  = key("admin.tone.mist", Message{ZhHant: "霧藍", En: "Mist"})
	KeyAdminToneSage  = key("admin.tone.sage", Message{ZhHant: "淡綠", En: "Sage"})
	KeyAdminToneBlush = key("admin.tone.blush", Message{ZhHant: "淡粉", En: "Blush"})
	KeyAdminToneInk   = key("admin.tone.ink", Message{ZhHant: "墨黑", En: "Ink"})

	KeyAdminToneInherit = key("admin.tone.inherit", Message{ZhHant: "沿用上層色調", En: "Inherit the department's tone"})

	KeyFormToneUnknown = key("form.tone.unknown", Message{
		ZhHant: "請從清單中選一個色調。",
		En:     "Pick a tone from the list.",
	})

	// KeyAdminTaxToneOf labels the tone control on one category's row.
	KeyAdminTaxToneOf = key("admin.tax.toneof", Message{
		ZhHant: "%s 的色調",
		En:     "Tone for %s",
	})

	// KeyAdminTaxHeader links a category's row to the page that holds its photograph.
	KeyAdminTaxHeader = key("admin.tax.header", Message{ZhHant: "頁首圖片", En: "Header photo"})

	KeyAdminCatImage = key("admin.cat.image", Message{ZhHant: "分類頁首圖片", En: "Category header photograph"})

	KeyAdminCatImageHint = key("admin.cat.imagehint", Message{
		ZhHant: "顯示在首頁、館別頁標題旁和選單裡，各處都從中央裁切，主體請放在中間。建議 1200×800。子分類沿用上層的圖片。",
		En:     "Shown on the home page, beside the department's heading and in the menu, each cropped from the centre, so keep the subject in the middle. 1200×800 works best. A sub-category shows its department's photograph.",
	})

	KeyAdminPageTaxonomy = key("admin.page.taxonomy", Message{ZhHant: "品牌與分類", En: "Brands and categories"})

	// KeyFormPositionTaken is a collision, not a mistake: CreateCategory computes
	// position as max(position) + 1, so two staff members adding at the same
	// moment read the same maximum and categories_position_key refuses the
	// loser. A second attempt computes a fresh maximum.
	KeyFormPositionTaken = key("admin.taxonomy.positiontaken", Message{
		ZhHant: "剛剛有人同時新增了分類，請再送出一次。",
		En:     "Someone added a category at the same moment. Please submit again.",
	})

	KeyAdminTaxLead = key("admin.tax.lead", Message{
		ZhHant: "名稱可以修改，網址代稱不能。需要不同的代稱時，請建立新的，再把商品移過去。",
		En:     "A name can be changed; a slug cannot. For a different slug, create a new one and move the products across.",
	})

	KeyAdminTaxBrands = key("admin.tax.brands", Message{ZhHant: "品牌", En: "Brands"})

	KeyAdminTaxCategories = key("admin.tax.categories", Message{ZhHant: "分類", En: "Categories"})

	KeyAdminTaxNoBrands = key("admin.tax.no.brands", Message{ZhHant: "還沒有任何品牌。", En: "No brands yet."})

	KeyAdminTaxNoCategories = key("admin.tax.no.categories", Message{ZhHant: "還沒有任何分類。", En: "No categories yet."})

	KeyAdminTaxNewBrand = key("admin.tax.new.brand", Message{ZhHant: "新增品牌", En: "Add a brand"})

	KeyAdminTaxNewCategory = key("admin.tax.new.category", Message{ZhHant: "新增分類", En: "Add a category"})

	KeyAdminTaxNameEn = key("admin.tax.nameen", Message{ZhHant: "英文名稱", En: "English name"})

	KeyAdminTaxNameEnHint = key("admin.tax.nameen.hint", Message{
		ZhHant: "留空時，英文網站會顯示中文名稱。",
		En:     "Leave it blank and the English site shows the Chinese name.",
	})

	KeyAdminTaxSlugFixed = key("admin.tax.slug.fixed", Message{
		ZhHant: "建立之後就不能改。",
		En:     "Cannot be changed once created.",
	})

	KeyAdminTaxParent = key("admin.tax.parent", Message{ZhHant: "上層分類（選填）", En: "Parent category (optional)"})

	KeyAdminTaxParentHint = key("admin.tax.parent.hint", Message{
		ZhHant: "留空就是最上層。",
		En:     "Leave it blank for a top-level category.",
	})

	KeyAdminTaxCreate = key("admin.tax.create", Message{ZhHant: "建立", En: "Create"})

	KeyAdminTaxNameOf = key("admin.tax.nameof", Message{ZhHant: "%s 的名稱", En: "Name of %s"})

	KeyAdminTaxNameEnOf = key("admin.tax.nameenof", Message{ZhHant: "%s 的英文名稱", En: "English name of %s"})

	KeyAdminTaxUnder = key("admin.tax.under", Message{ZhHant: "· 在 %s 之下", En: "· under %s"})

	KeyAdminTaxonomyBoth = key("admin.taxonomy.both", Message{
		ZhHant: "有 %s 個商品和 %d 個子分類",
		En:     "%s products and %d sub-categories",
	})

	KeyAdminTaxonomyChildren = countKey("admin.taxonomy.children", "有 %d 個子分類", "%d sub-category", "%d sub-categories")

	KeyAdminTaxonomyProducts = countKey("admin.taxonomy.products", "有 %s 個商品", "%s product", "%s products")
)

var (
	KeyAdminNoticeInUse = key("admin.notice.inuse", Message{
		ZhHant: "還有商品或子分類在用它，先把那些移到別的地方再刪。",
		En:     "Products or child categories still point at it. Move those elsewhere first.",
	})
)
