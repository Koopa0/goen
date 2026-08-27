package i18n

var (
	KeyBusyTitle = key("error.busy", Message{ZhHant: "暫時無法處理", En: "Cannot do that right now"})

	KeyBusyBody = key("error.busy.body", Message{ZhHant: "請稍後再試。", En: "Please try again shortly."})

	KeyTooManyRequests = key("error.ratelimited", Message{
		ZhHant: "請求過於頻繁,請稍後再試。",
		En:     "Too many requests. Please try again shortly.",
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
