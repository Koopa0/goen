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

var (
	KeyAdminNotFoundTitle = key("admin.notfound.title", Message{ZhHant: "找不到頁面", En: "Page not found"})

	KeyAdminNotFoundHead = key("admin.notfound.head", Message{ZhHant: "找不到這個頁面", En: "No such page"})

	KeyAdminNotFoundBody = key("admin.notfound.body", Message{
		ZhHant: "這個網址目前沒有對應的內容。",
		En:     "Nothing lives at this address.",
	})

	KeyAdminErrorTitle = key("admin.error.title", Message{ZhHant: "暫時無法處理", En: "Temporarily unavailable"})

	KeyAdminErrorBody = key("admin.error.body", Message{ZhHant: "請稍後再試。", En: "Please try again shortly."})

	KeyAdminFaultTitle = key("admin.fault.title", Message{ZhHant: "發生錯誤", En: "Something went wrong"})

	KeyAdminFaultHead = key("admin.fault.head", Message{ZhHant: "系統發生錯誤", En: "A system error"})

	KeyAdminFaultBody = key("admin.fault.body", Message{
		ZhHant: "請稍後再試一次。",
		En:     "Please try again in a moment.",
	})

	KeyAdminBadForm = key("admin.badform", Message{
		ZhHant: "400 表單無法解析",
		En:     "400 that form could not be read",
	})
)
