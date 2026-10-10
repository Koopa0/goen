package i18n

var (
	KeyContactTitle = key("contact.title", Message{ZhHant: "聯絡我們", En: "Contact us"})

	KeyContactDescription = key("contact.description", Message{
		ZhHant: "goen 客服信箱，以及線上留言表單。週一至週五 09:00–18:00。",
		En:     "Email, plus a form. Monday to Friday, 09:00–18:00.",
	})

	KeyContactHours = key("contact.hours", Message{
		ZhHant: "客服時間 週一至週五 09:00–18:00。訂單問題請附上訂單編號。",
		En: "Monday to Friday, 09:00–18:00. " +
			"For anything about an order, include the order number.",
	})

	KeyContactSent = key("contact.sent", Message{ZhHant: "訊息已送出", En: "Message sent"})

	KeyContactSentBody = key("contact.sent.body", Message{
		ZhHant: "我們會回覆到你留的信箱。訂單相關問題會優先處理。",
		En: "We will reply to the address you gave. Anything about " +
			"an order goes first.",
	})

	KeyContactFormTitle = key("contact.form", Message{ZhHant: "留言給我們", En: "Send us a message"})

	KeyContactFormErrors = key("contact.form.errors", Message{
		ZhHant: "表單還有欄位需要修正，請檢查下方標示的項目。",
		En:     "Some fields still need fixing — check the ones marked below.",
	})

	KeyNamePlaceholder = key("field.name.placeholder", Message{ZhHant: "王小明", En: "Your name"})

	// KeyOrderRefPlaceholder shows an order number in the shape goen issues:
	// GO, the day as YYMMDD, then a six-digit sequence.
	KeyOrderRefPlaceholder = key("field.orderref.placeholder", Message{ZhHant: "GO-260101-000001", En: "GO-260101-000001"})

	KeyFieldSubject = key("field.subject", Message{ZhHant: "主題", En: "Subject"})

	KeyFieldOrderRefOpt = key("field.orderref.optional", Message{
		ZhHant: "訂單編號（選填）",
		En:     "Order number (optional)",
	})

	KeyFieldMessage = key("field.message", Message{ZhHant: "訊息內容", En: "Message"})

	KeyMessagePlaceholder = key("field.message.placeholder", Message{
		ZhHant: "請描述你遇到的狀況…",
		En:     "Tell us what happened…",
	})

	KeyContactSubmit = key("contact.submit", Message{ZhHant: "送出訊息", En: "Send message"})

	KeyContactBusy = key("contact.busy", Message{
		ZhHant: "系統暫時無法接收訊息，請稍後再試，或直接寄信給我們。",
		En:     "We cannot take the message right now. Try again shortly, or email us directly.",
	})

	KeySubjectOrder = key("contact.subject.order", Message{ZhHant: "訂單問題", En: "An order"})

	KeySubjectReturns = key("contact.subject.returns", Message{ZhHant: "退貨", En: "Returns"})

	KeySubjectWarranty = key("contact.subject.warranty", Message{ZhHant: "保固維修", En: "Warranty or repair"})

	KeySubjectProduct = key("contact.subject.product", Message{ZhHant: "商品諮詢", En: "A product question"})

	KeySubjectPartnership = key("contact.subject.partnership", Message{
		ZhHant: "合作提案",
		En:     "A partnership",
	})

	KeyNameRequired2 = key("valid.contact.name", Message{ZhHant: "請填寫姓名", En: "Enter your name"})

	KeyNameTooLong2 = key("valid.contact.name.long", Message{
		ZhHant: "姓名請控制在 %d 個字以內",
		En:     "Keep your name under %d characters",
	})

	KeySubjectRequired = key("valid.contact.subject", Message{
		ZhHant: "請選擇一個主題",
		En:     "Choose a subject",
	})

	KeyOrderRefTooLong = key("valid.contact.orderref", Message{
		ZhHant: "訂單編號請控制在 %d 個字元以內",
		En:     "Keep the order number under %d characters",
	})

	KeyMessageRequired = key("valid.contact.message", Message{
		ZhHant: "請填寫訊息內容",
		En:     "Write a message",
	})

	KeyMessageTooShort = key("valid.contact.message.short", Message{
		ZhHant: "訊息內容請至少 %d 個字，讓我們知道發生什麼事",
		En:     "At least %d characters, so we know what happened",
	})

	KeyMessageTooLong = key("valid.contact.message.long", Message{
		ZhHant: "訊息內容請控制在 %d 個字以內",
		En:     "Keep the message under %d characters",
	})

	KeyNoControlChars = key("valid.nocontrol", Message{
		ZhHant: "不能包含控制字元",
		En:     "Cannot contain control characters",
	})

	KeyNoNewlines = key("valid.nonewlines", Message{
		ZhHant: "不能包含換行或控制字元",
		En:     "Cannot contain line breaks or control characters",
	})
)

var (
	KeyAdminPageMessages = key("admin.page.messages", Message{ZhHant: "聯絡訊息", En: "Contact messages"})

	KeyAdminMessagesLead = key("admin.messages.lead", Message{
		ZhHant: "尚未處理的訊息排在前面，其中等最久的排第一。",
		En:     "Messages not yet handled come first, the longest wait at the top.",
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

	KeyAdminMsgOverdue = key("admin.message.overdue", Message{ZhHant: "逾時 · %s", En: "Overdue · %s"})

	KeyAdminMsgReopen = key("admin.message.reopen", Message{ZhHant: "重新開啟", En: "Reopen"})

	KeyAdminMsgHandle = key("admin.message.handle", Message{ZhHant: "標記已處理", En: "Mark handled"})
)

var KeyContactMyOrders = key("contact.myorders", Message{ZhHant: "我的訂單", En: "My orders"})
