package i18n

var (
	KeyAdminPageMessages = key("admin.page.messages", Message{ZhHant: "聯絡訊息", En: "Contact messages"})

	KeyAdminMessagesLead = key("admin.messages.lead", Message{
		ZhHant: "等最久的排在最前面 —— 三天前寫信的人比今天早上寫的更急,最新排在前面剛好把他埋掉。",
		En: "The longest wait comes first — somebody who wrote three days ago is more urgent " +
			"than somebody who wrote this morning, and newest-first would bury them exactly then.",
	})

	KeyAdminMessagesOpen = key("admin.messages.open", Message{ZhHant: "待處理 %s", En: "%s open"})

	KeyAdminMessagesEmpty = key("admin.messages.empty", Message{
		ZhHant: "沒有任何聯絡訊息。",
		En:     "No contact messages.",
	})

	KeyAdminMessagesOrder = key("admin.messages.order", Message{ZhHant: "訂單", En: "Order"})

	KeyAdminMsgHandled = key("admin.message.handled", Message{ZhHant: "已處理", En: "Handled"})

	KeyAdminMsgToday = key("admin.message.today", Message{ZhHant: "今天", En: "Today"})

	KeyAdminMsgOneDay = key("admin.message.oneday", Message{ZhHant: "等了 1 天", En: "Waiting 1 day"})

	KeyAdminMsgDays = key("admin.message.days", Message{ZhHant: "等了 %d 天", En: "Waiting %d days"})

	KeyAdminMsgReopen = key("admin.message.reopen", Message{ZhHant: "重新開啟", En: "Reopen"})

	KeyAdminMsgHandle = key("admin.message.handle", Message{ZhHant: "標記已處理", En: "Mark handled"})
)
