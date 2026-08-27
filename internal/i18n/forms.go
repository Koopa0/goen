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
