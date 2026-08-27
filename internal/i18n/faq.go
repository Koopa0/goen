package i18n

var (
	KeyFAQEmptyTail = key("faq.empty.tail", Message{ZhHant: "。", En: "."})

	KeyFAQTitle = key("faq.title", Message{ZhHant: "常見問題", En: "Frequently asked questions"})

	KeyFAQDescription = key("faq.description", Message{
		ZhHant: "goen 的訂購、配送、退換貨與發票說明。",
		En:     "Ordering, delivery, returns and invoices at goen.",
	})

	KeyFAQEmpty = key("faq.empty", Message{
		ZhHant: "還沒有整理常見問題。有疑問請",
		En:     "No questions written up yet. If you have one, ",
	})
)
