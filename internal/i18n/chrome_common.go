package i18n

var (
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

	KeyTryAgainTitle = key("error.retry.title", Message{
		ZhHant: "系統暫時無法處理",
		En:     "Something went wrong",
	})
	KeyTryAgainBody = key("error.retry.body", Message{
		ZhHant: "請稍後再試一次。",
		En:     "Please try again shortly.",
	})
)
