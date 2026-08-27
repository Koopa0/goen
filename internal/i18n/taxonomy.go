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

	KeyAdminPageTaxonomy = key("admin.page.taxonomy", Message{ZhHant: "品牌與分類", En: "Brands and categories"})

	// KeyFormPositionTaken is a collision, not a mistake. CreateCategory computes
	// position as max(position) + 1, so two staff members adding a category at
	// the same moment both read the same maximum and categories_position_key
	// refuses the loser. Nothing they typed is wrong and the second attempt
	// computes a fresh maximum, so the message says exactly that.
	KeyFormPositionTaken = key("admin.taxonomy.positiontaken", Message{
		ZhHant: "剛剛有人同時新增了分類,請再送出一次。",
		En:     "Someone added a category at the same moment. Please submit again.",
	})

	KeyAdminTaxLead = key("admin.tax.lead", Message{
		ZhHant: "名稱可以改,網址代稱不行 —— 它在每個已經被索引和分享出去的連結裡。需要不同的代稱就建一個新的,再把商品移過去。",
		En: "A name can be changed; a slug cannot — it is in every link that has been indexed and " +
			"every link anybody has sent. If you need a different slug, create a new one and move " +
			"the products across.",
	})

	KeyAdminTaxBrands = key("admin.tax.brands", Message{ZhHant: "品牌", En: "Brands"})

	KeyAdminTaxCategories = key("admin.tax.categories", Message{ZhHant: "分類", En: "Categories"})

	KeyAdminTaxNoBrands = key("admin.tax.no.brands", Message{ZhHant: "還沒有任何品牌。", En: "No brands yet."})

	KeyAdminTaxNoCategories = key("admin.tax.no.categories", Message{ZhHant: "還沒有任何分類。", En: "No categories yet."})

	KeyAdminTaxNewBrand = key("admin.tax.new.brand", Message{ZhHant: "新增品牌", En: "Add a brand"})

	KeyAdminTaxNewCategory = key("admin.tax.new.category", Message{ZhHant: "新增分類", En: "Add a category"})

	KeyAdminTaxNameEn = key("admin.tax.nameen", Message{ZhHant: "英文名稱", En: "English name"})

	KeyAdminTaxNameEnHint = key("admin.tax.nameen.hint", Message{
		ZhHant: "留空的話,英文網站會顯示中文名稱 —— 讀得懂,但看得出還沒翻。",
		En: "Leave it blank and the English site shows the Chinese name — readable, but visibly " +
			"untranslated.",
	})

	KeyAdminTaxSlugFixed = key("admin.tax.slug.fixed", Message{
		ZhHant: "建立之後就不能改。",
		En:     "Cannot be changed once created.",
	})

	KeyAdminTaxParent = key("admin.tax.parent", Message{ZhHant: "上層分類(選填)", En: "Parent category (optional)"})

	KeyAdminTaxParentHint = key("admin.tax.parent.hint", Message{
		ZhHant: "留空就是最上層。",
		En:     "Leave it blank for a top-level category.",
	})

	KeyAdminTaxCreate = key("admin.tax.create", Message{ZhHant: "建立", En: "Create"})

	KeyAdminTaxNameOf = key("admin.tax.nameof", Message{ZhHant: "%s 的名稱", En: "Name of %s"})

	KeyAdminTaxNameEnOf = key("admin.tax.nameenof", Message{ZhHant: "%s 的英文名稱", En: "English name of %s"})

	KeyAdminTaxRename = key("admin.tax.rename", Message{ZhHant: "更名", En: "Rename"})

	KeyAdminTaxUnder = key("admin.tax.under", Message{ZhHant: "· 在 %s 之下", En: "· under %s"})

	KeyAdminTaxonomyBoth = key("admin.taxonomy.both", Message{
		ZhHant: "有 %s 個商品和 %d 個子分類",
		En:     "%s products and %d sub-categories",
	})

	KeyAdminTaxonomyChildren = key("admin.taxonomy.children", Message{
		ZhHant: "有 %d 個子分類",
		En:     "%d sub-categories",
	})

	KeyAdminTaxonomyProducts = key("admin.taxonomy.products", Message{
		ZhHant: "有 %s 個商品",
		En:     "%s products",
	})
)

var (
	KeyAdminNoticeInUse = key("admin.notice.inuse", Message{
		ZhHant: "還有商品或子分類在用它,先把那些移到別的地方再刪。",
		En:     "Products or child categories still point at it. Move those elsewhere first.",
	})
)
