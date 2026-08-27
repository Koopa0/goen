package i18n

var (
	KeyFormSpecLabel = key("form.spec.label", Message{ZhHant: "請填寫規格名稱", En: "A spec label is required"})

	KeyFormSpecLabelLong = key("form.spec.label.long", Message{ZhHant: "規格名稱太長", En: "That spec label is too long"})

	KeyFormSpecValue = key("form.spec.value", Message{ZhHant: "請填寫規格內容", En: "A spec value is required"})

	KeyFormSpecValueLong = key("form.spec.value.long", Message{ZhHant: "規格內容太長", En: "That spec value is too long"})

	KeyFormSpecLabelEnLong = key("form.spec.label.en.long", Message{
		ZhHant: "英文規格名稱太長",
		En:     "That English spec label is too long",
	})

	KeyFormSpecValueEnLong = key("form.spec.value.en.long", Message{
		ZhHant: "英文規格內容太長",
		En:     "That English spec value is too long",
	})

	KeyAdminProdSpecs = key("admin.prod.specs", Message{
		ZhHant: "規格表",
		En:     "Specification table",
	})

	KeyAdminProdSpecsLead = key("admin.prod.specslead", Message{
		ZhHant: "商品頁與「比較」頁讀的就是這一份。沒有規格表的商品在比較頁是空的一欄。",
		En: "This is what the product page and Compare read. A product with no specification table " +
			"is an empty column on Compare.",
	})

	KeyAdminProdSpecLabel = key("admin.prod.speclabel", Message{ZhHant: "項目", En: "Label"})

	KeyAdminProdSpecValue = key("admin.prod.specvalue", Message{ZhHant: "內容", En: "Value"})

	KeyAdminProdSpecLabelEn = key("admin.prod.speclabelen", Message{
		ZhHant: "項目(英文)",
		En:     "Label (English)",
	})

	KeyAdminProdSpecValueEn = key("admin.prod.specvalueen", Message{
		ZhHant: "內容(英文)",
		En:     "Value (English)",
	})

	KeyAdminProdSpecHint = key("admin.prod.spechint", Message{
		ZhHant: "英文留空的話,英文網站會顯示中文 —— 數字型的內容(6.3 吋)通常不必翻。",
		En: "Leave the English blank and the English site shows the Chinese — a value that is " +
			"mostly a number (6.3-inch) rarely needs translating.",
	})

	KeyAdminProdSpecAdd = key("admin.prod.specadd", Message{
		ZhHant: "新增規格表項目",
		En:     "Add spec row",
	})
)
