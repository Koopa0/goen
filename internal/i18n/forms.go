package i18n

var (
	KeyFormHasErrors = key("form.errors", Message{
		ZhHant: "有欄位需要修正,請看下方標示。",
		En:     "Some fields need fixing — see the notes below.",
	})

	KeyEmailRequired = key("field.email.required", Message{
		ZhHant: "請填寫 Email",
		En:     "Enter an email address",
	})

	KeyEmailMalformed = key("field.email.malformed", Message{
		ZhHant: "Email 格式看起來不正確",
		En:     "That does not look like an email address",
	})

	KeyEmailTooLong = key("field.email.toolong", Message{
		ZhHant: "Email 請控制在 %d 個字元以內",
		En:     "Keep the email address under %d characters",
	})

	KeyFormUnreadable = key("form.unreadable", Message{
		ZhHant: "表單無法解析",
		En:     "That form could not be read",
	})
)

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

var (
	KeyAdminNoticeOK = key("admin.notice.ok", Message{ZhHant: "已更新。", En: "Saved."})

	KeyAdminNoticeRefused = key("admin.notice.refused", Message{
		ZhHant: "資料庫拒絕了這個變更。可能是狀態流程不允許,或會違反庫存與活動規則。",
		En: "The database refused that change. Either the status move is not a legal one, " +
			"or it would break a stock or campaign rule.",
	})
)

var KeyFormRunDays = key("form.run.days", Message{
	ZhHant: "檔期天數必須介於 0(不限)到 365 天。",
	En:     "A run is 0 days (no end) to 365 days.",
})
