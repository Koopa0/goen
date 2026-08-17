package i18n

var (
	KeyAdminTaxLead = key("admin.tax.lead", Message{
		ZhHant: "名稱可以改,網址代稱不行 —— 它在每個已經被索引和分享出去的連結裡。需要不同的代稱就建一個新的,再把商品移過去。",
		En: "A name can be changed; a slug cannot — it is in every link that has been indexed and " +
			"every link anybody has sent. If you need a different slug, create a new one and move " +
			"the products across.",
	})

	KeyAdminTaxBrands     = key("admin.tax.brands", Message{ZhHant: "品牌", En: "Brands"})
	KeyAdminTaxCategories = key("admin.tax.categories", Message{ZhHant: "分類", En: "Categories"})

	KeyAdminTaxNoBrands     = key("admin.tax.no.brands", Message{ZhHant: "還沒有任何品牌。", En: "No brands yet."})
	KeyAdminTaxNoCategories = key("admin.tax.no.categories", Message{ZhHant: "還沒有任何分類。", En: "No categories yet."})

	KeyAdminTaxNewBrand    = key("admin.tax.new.brand", Message{ZhHant: "新增品牌", En: "Add a brand"})
	KeyAdminTaxNewCategory = key("admin.tax.new.category", Message{ZhHant: "新增分類", En: "Add a category"})
	KeyAdminTaxNameEn      = key("admin.tax.nameen", Message{ZhHant: "英文名稱", En: "English name"})
	KeyAdminTaxNameEnHint  = key("admin.tax.nameen.hint", Message{
		ZhHant: "留空的話,英文網站會顯示中文名稱 —— 讀得懂,但看得出還沒翻。",
		En: "Leave it blank and the English site shows the Chinese name — readable, but visibly " +
			"untranslated.",
	})
	KeyAdminTaxSlugFixed = key("admin.tax.slug.fixed", Message{
		ZhHant: "建立之後就不能改。",
		En:     "Cannot be changed once created.",
	})
	KeyAdminTaxParent     = key("admin.tax.parent", Message{ZhHant: "上層分類(選填)", En: "Parent category (optional)"})
	KeyAdminTaxParentHint = key("admin.tax.parent.hint", Message{
		ZhHant: "留空就是最上層。",
		En:     "Leave it blank for a top-level category.",
	})
	KeyAdminTaxCreate = key("admin.tax.create", Message{ZhHant: "建立", En: "Create"})

	KeyAdminTaxNameOf   = key("admin.tax.nameof", Message{ZhHant: "%s 的名稱", En: "Name of %s"})
	KeyAdminTaxNameEnOf = key("admin.tax.nameenof", Message{ZhHant: "%s 的英文名稱", En: "English name of %s"})
	KeyAdminTaxRename   = key("admin.tax.rename", Message{ZhHant: "更名", En: "Rename"})
	KeyAdminTaxUnder    = key("admin.tax.under", Message{ZhHant: "· 在 %s 之下", En: "· under %s"})
)
