package i18n

var (
	KeyAdminRefusalStock      = key("admin.refusal.stock", Message{ZhHant: "庫存調整", En: "Stock adjustment"})
	KeyAdminRefusalInspection = key("admin.refusal.inspection", Message{ZhHant: "退貨驗貨", En: "Return inspection"})
	KeyAdminRefusalFAQ        = key("admin.refusal.faq", Message{ZhHant: "編輯常見問題", En: "Edit FAQ"})
	KeyAdminRefusalHero       = key("admin.refusal.hero", Message{ZhHant: "新增輪播", En: "Add a slide"})
	KeyAdminRefusalBanner     = key("admin.refusal.banner", Message{ZhHant: "新增公告", En: "Add a banner"})
	KeyAdminRefusalSummary    = countKey("admin.refusal.summary", "「%[2]s」未儲存，請修正 %[1]d 個欄位。", "%[2]s was not saved. Please correct %[1]d field.", "%[2]s was not saved. Please correct %[1]d fields.")
	KeyAdminRefusalFirst      = key("admin.refusal.first", Message{ZhHant: "前往第一個錯誤欄位", En: "Go to the first invalid field"})
)
