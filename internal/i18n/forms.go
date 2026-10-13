package i18n

var (
	KeyFormHasErrors = key("form.errors", Message{
		ZhHant: "有欄位需要修正，請看下方標示。",
		En:     "Some fields need fixing — see the notes below.",
	})

	KeyEmailRequired = key("field.email.required", Message{
		ZhHant: "請填寫電子郵件",
		En:     "Enter an email address",
	})

	KeyEmailMalformed = key("field.email.malformed", Message{
		ZhHant: "電子郵件格式看起來不正確",
		En:     "That does not look like an email address",
	})

	KeyEmailTooLong = key("field.email.toolong", Message{
		ZhHant: "電子郵件請在 %d 個字元以內",
		En:     "Keep the email address under %d characters",
	})

	KeyFormUnreadable = key("form.unreadable", Message{
		ZhHant: "表單無法解析",
		En:     "That form could not be read",
	})

	KeyAddressUnreadable = key("request.unreadable", Message{
		ZhHant: "這個網址無法解析",
		En:     "That web address could not be read",
	})

	KeyAddressRecovery = key("request.unreadable.recovery", Message{ZhHant: "請確認網址是否完整，或回到商店繼續瀏覽。", En: "Check that the address is complete, or return to the shop to keep browsing."})
)

var (
	KeyFormSlugFormat = key("form.slug.format", Message{
		ZhHant: "網址代稱只能用小寫英數與連字號。",
		En:     "A slug takes lower-case letters, digits and hyphens only.",
	})

	KeyFormNameRequired = key("form.name.required", Message{
		ZhHant: "請填寫名稱，不超過 60 個字。",
		En:     "A name is required, 60 characters at most.",
	})
)

var (
	KeyAdminNoticeOK = key("admin.notice.ok", Message{ZhHant: "已更新。", En: "Saved."})

	KeyAdminNoticeRefused = key("admin.notice.refused", Message{
		ZhHant: "這個變更不符合狀態、庫存或活動的規則。請重新整理，確認目前的狀態後再試。",
		En: "That change does not fit the status, stock or campaign rules. " +
			"Reload the page, check the current state, then try again.",
	})

	KeyAdminNoticeLeadRefused = key("admin.notice.lead.refused", Message{ZhHant: "未儲存", En: "Not saved"})

	KeyAdminNoticeLeadFailed = key("admin.notice.lead.failed", Message{ZhHant: "未完成", En: "Did not finish"})

	KeyAdminNoticeGone = key("admin.notice.gone", Message{
		ZhHant: "這筆資料已不存在，或狀態已變更。請重新載入列表。",
		En:     "That record is gone or its state has already changed. Reload the list.",
	})
)

var KeyFormRunDays = key("form.run.days", Message{
	ZhHant: "檔期天數必須介於 0（不限）到 365 天。",
	En:     "A run is 0 days (no end) to 365 days.",
})
