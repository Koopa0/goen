package i18n

var (
	KeyFormSlugFormat = key("form.slug.format", Message{
		ZhHant: "網址代稱只能用小寫英數與連字號。",
		En:     "A slug takes lower-case letters, digits and hyphens only.",
	})

	KeyFormNameRequired = key("form.name.required", Message{
		ZhHant: "請填寫名稱,不超過 60 個字。",
		En:     "A name is required, 60 characters at most.",
	})
)
