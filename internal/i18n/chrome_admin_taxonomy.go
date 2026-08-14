package i18n

// The words /admin/taxonomy writes — brands and categories, their create forms
// and the two lists beside them.
//
// One page holds two of everything, because a brand and a category are managed
// the same way and differ in exactly two places: only a category has a parent,
// and only a category has an English name. The keys follow that shape rather
// than collapsing into one set with a placeholder in it — "Add a %s" would need
// the noun in a case English and Chinese do not agree about.

var (
	// The heading's own sentence. A slug is never renamed and this is where the
	// page says why, so the reason travels with it: a shop that reads "you
	// cannot change this" without the reason files it as an arbitrary rule.
	KeyAdminTaxLead = key("admin.tax.lead", Message{
		ZhHant: "名稱可以改,網址代稱不行 —— 它在每個已經被索引和分享出去的連結裡。需要不同的代稱就建一個新的,再把商品移過去。",
		En: "A name can be changed; a slug cannot — it is in every link that has been indexed and " +
			"every link anybody has sent. If you need a different slug, create a new one and move " +
			"the products across.",
	})

	// The two section headings, over a LIST. Separate from KeyAdminColCategory
	// and KeyFacetBrand, which name one row and one facet: English pluralises a
	// heading over a list and leaves a column noun singular, and one key could
	// only be right about one of them.
	KeyAdminTaxBrands     = key("admin.tax.brands", Message{ZhHant: "品牌", En: "Brands"})
	KeyAdminTaxCategories = key("admin.tax.categories", Message{ZhHant: "分類", En: "Categories"})

	// The empty state is a whole sentence per kind rather than one with the
	// heading dropped into it. Glued together, English reads "No Brands yet" —
	// a heading is title case and the noun inside a sentence is not, and the
	// two are the same word only in Chinese.
	KeyAdminTaxNoBrands     = key("admin.tax.no.brands", Message{ZhHant: "還沒有任何品牌。", En: "No brands yet."})
	KeyAdminTaxNoCategories = key("admin.tax.no.categories", Message{ZhHant: "還沒有任何分類。", En: "No categories yet."})

	// The create forms.
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

	// One row's controls.
	//
	// The two aria-labels name the row they belong to because they are the only
	// label those boxes have: a list of twenty rows would otherwise announce
	// "Name" twenty times, and somebody using a screen reader could not tell
	// which one they were about to rename.
	KeyAdminTaxNameOf   = key("admin.tax.nameof", Message{ZhHant: "%s 的名稱", En: "Name of %s"})
	KeyAdminTaxNameEnOf = key("admin.tax.nameenof", Message{ZhHant: "%s 的英文名稱", En: "English name of %s"})
	KeyAdminTaxRename   = key("admin.tax.rename", Message{ZhHant: "更名", En: "Rename"})
	KeyAdminTaxUnder    = key("admin.tax.under", Message{ZhHant: "· 在 %s 之下", En: "· under %s"})
)
