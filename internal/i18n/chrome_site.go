package i18n

var (
	KeyTagline = key("site.tagline", Message{
		ZhHant: "讓買家與對的好商品相遇",
		En:     "Bringing buyers and the right things together",
	})
	KeySiteTitle = key("site.title", Message{
		ZhHant: "goen · 讓買家與對的好商品相遇",
		En:     "goen · bringing buyers and the right things together",
	})
	KeyFooterTagline = key("site.footer.tagline", Message{
		ZhHant: "讓買家與對的好商品相遇。規格看得懂、保固靠得住的 3C 選品店。",
		En: "Bringing buyers and the right things together — a 3C shop where the " +
			"specifications make sense and the warranty holds.",
	})
	KeySupportHours = key("site.hours", Message{
		ZhHant: "客服時間 週一至週五 09:00–18:00",
		En:     "Support Monday to Friday, 09:00–18:00",
	})
	KeyCategoryNav = key("nav.categories", Message{ZhHant: "商品分類", En: "Categories"})
	KeyCloseBanner = key("site.banner.close", Message{ZhHant: "關閉公告", En: "Dismiss"})

	KeyNewsletter     = key("site.newsletter", Message{ZhHant: "電子報", En: "Newsletter"})
	KeyNewsletterNote = key("site.newsletter.note", Message{
		ZhHant: "每月一封,新品與比價重點,不灌水。",
		En:     "One letter a month: new arrivals and what is worth comparing, nothing padded.",
	})
	KeyNewsletterSubmit     = key("site.newsletter.submit", Message{ZhHant: "訂閱", En: "Subscribe"})
	KeyNewsletterInlineDone = key("site.newsletter.done", Message{
		ZhHant: "確認信已寄出,請到信箱點一下連結。",
		En:     "Check your inbox and follow the link to finish.",
	})

	KeyAboutTitle       = key("about.title", Message{ZhHant: "關於我們", En: "About us"})
	KeyAboutDescription = key("about.description", Message{
		ZhHant: "goen 是規格看得懂、保固靠得住的 3C 選品店。每件商品都先過我們自己的比較表。",
		En: "goen is a 3C shop where the specifications make sense and the warranty holds. " +
			"Everything on it has been compared and tested first.",
	})
	KeyAboutName = key("about.name", Message{
		ZhHant: "goen 的名字有三層:Go 是我們寫後端的語言;ご縁(go-en)是日文裡人與物之間的緣分;五円(同音)是日本神社裡討吉利的那枚硬幣。三件事說的是同一件事 — 買 3C 不該是運氣,而是被好好安排的相遇。",
		En: "The name carries three things at once. Go is the language the back end is " +
			"written in; ご縁 (go-en) is the Japanese word for the tie between a person " +
			"and a thing; and 五円, which sounds the same, is the coin people offer at a " +
			"Japanese shrine for luck. All three say one thing — buying 3C should not be " +
			"a matter of luck, but a meeting somebody arranged properly.",
	})
	KeyAboutSelection = key("about.selection", Message{
		ZhHant: "我們不做「什麼都賣」。每一件上架商品都經過規格比對與實測,規格表寫到你看得懂為止;價格含稅、保固寫清楚、退換貨不藏條款。比到眼花的事我們先做完,你只要選。",
		En: "We do not sell everything. Each product is compared and tested before it goes " +
			"up, and its spec table is written out until it makes sense. Prices include " +
			"tax, the warranty is stated plainly, and there are no buried clauses in the " +
			"returns policy. The comparing until your eyes hurt is our job; you only have " +
			"to choose.",
	})
	KeyAboutSpecs     = key("about.specs", Message{ZhHant: "規格透明", En: "Specifications in full"})
	KeyAboutSpecsBody = key("about.specs.body", Message{
		ZhHant: "每件商品都有完整 spec table,同系列可逐欄比較。",
		En:     "Every product has a complete spec table, and a range compares column by column.",
	})
	KeyAboutWarranty     = key("about.warranty", Message{ZhHant: "保固靠得住", En: "A warranty that holds"})
	KeyAboutWarrantyBody = key("about.warranty.body", Message{
		ZhHant: "原廠保固線上登錄,維修免費到府收送。",
		En:     "Register the manufacturer's warranty online; repairs are collected for free.",
	})
	KeyAboutCurated     = key("about.curated", Message{ZhHant: "精選不灌水", En: "Chosen, not padded"})
	KeyAboutCuratedBody = key("about.curated.body", Message{
		ZhHant: "上架前先過我們自己的比較表,賣不過同價位的不上。",
		En: "Everything goes through our own comparison table first. If it loses to " +
			"something at the same price, it does not go up.",
	})

	KeyContactTitle       = key("contact.title", Message{ZhHant: "聯絡我們", En: "Contact us"})
	KeyContactDescription = key("contact.description", Message{
		ZhHant: "goen 客服信箱、電話與 LINE 官方帳號,以及線上留言表單。週一至週五 09:00–18:00。",
		En:     "Email, phone and LINE, plus a form. Monday to Friday, 09:00–18:00.",
	})
	KeyContactHours = key("contact.hours", Message{
		ZhHant: "客服時間 週一至週五 09:00–18:00 · 平均 10 分鐘回覆。訂單問題請附上訂單編號。",
		En: "Monday to Friday, 09:00–18:00 · usually answered within 10 minutes. " +
			"For anything about an order, include the order number.",
	})
	KeyContactPhone      = key("contact.phone", Message{ZhHant: "電話", En: "Phone"})
	KeyContactLine       = key("contact.line", Message{ZhHant: "LINE 官方帳號", En: "LINE official account"})
	KeyContactOnMap      = key("contact.map", Message{ZhHant: "在 Google 地圖查看", En: "Open in Google Maps"})
	KeyContactDirections = key("contact.directions", Message{
		ZhHant: "捷運市政府站 2 號出口步行 5 分鐘",
		En:     "Five minutes' walk from Taipei City Hall MRT, exit 2",
	})
	KeyContactSent     = key("contact.sent", Message{ZhHant: "訊息已送出", En: "Message sent"})
	KeyContactSentBody = key("contact.sent.body", Message{
		ZhHant: "我們會在一個工作天內回覆到你留的信箱。訂單相關問題會優先處理。",
		En: "We will reply to the address you gave within one working day. Anything about " +
			"an order goes first.",
	})
	KeyContactFormTitle  = key("contact.form", Message{ZhHant: "留言給我們", En: "Send us a message"})
	KeyContactFormErrors = key("contact.form.errors", Message{
		ZhHant: "表單還有欄位需要修正,請檢查下方標示的項目。",
		En:     "Some fields still need fixing — check the ones marked below.",
	})
	KeyNamePlaceholder  = key("field.name.placeholder", Message{ZhHant: "王小明", En: "Your name"})
	KeyFieldSubject     = key("field.subject", Message{ZhHant: "主題", En: "Subject"})
	KeyFieldOrderRefOpt = key("field.orderref.optional", Message{
		ZhHant: "訂單編號(選填)",
		En:     "Order number (optional)",
	})
	KeyFieldMessage       = key("field.message", Message{ZhHant: "訊息內容", En: "Message"})
	KeyMessagePlaceholder = key("field.message.placeholder", Message{
		ZhHant: "請描述你遇到的狀況…",
		En:     "Tell us what happened…",
	})
	KeyContactSubmit = key("contact.submit", Message{ZhHant: "送出訊息", En: "Send message"})
	KeyContactBusy   = key("contact.busy", Message{
		ZhHant: "系統暫時無法接收訊息,請稍後再試,或直接寄信給我們。",
		En:     "We cannot take the message right now. Try again shortly, or email us directly.",
	})

	KeySubjectOrder       = key("contact.subject.order", Message{ZhHant: "訂單問題", En: "An order"})
	KeySubjectReturns     = key("contact.subject.returns", Message{ZhHant: "退換貨", En: "Returns or exchanges"})
	KeySubjectWarranty    = key("contact.subject.warranty", Message{ZhHant: "保固維修", En: "Warranty or repair"})
	KeySubjectProduct     = key("contact.subject.product", Message{ZhHant: "商品諮詢", En: "A product question"})
	KeySubjectPartnership = key("contact.subject.partnership", Message{
		ZhHant: "合作提案",
		En:     "A partnership",
	})

	KeyNameRequired2 = key("valid.contact.name", Message{ZhHant: "請填寫姓名", En: "Enter your name"})
	KeyNameTooLong2  = key("valid.contact.name.long", Message{
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
		ZhHant: "訊息內容請至少 %d 個字,讓我們知道發生什麼事",
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

	KeyPolicyEyebrow  = key("policy.eyebrow", Message{ZhHant: "政策", En: "Policy"})
	KeyPolicyHelp     = key("policy.help", Message{ZhHant: "說明", En: "Help"})
	KeyPolicyMore     = key("policy.more", Message{ZhHant: "還有問題?", En: "Still stuck?"})
	KeyPolicyMoreFAQ  = key("policy.more.faq", Message{ZhHant: ",或看看", En: ", or have a look at the "})
	KeyFAQEmptyTail   = key("faq.empty.tail", Message{ZhHant: "。", En: "."})
	KeyFAQTitle       = key("faq.title", Message{ZhHant: "常見問題", En: "Frequently asked questions"})
	KeyFAQDescription = key("faq.description", Message{
		ZhHant: "goen 的訂購、配送、退換貨與發票說明。",
		En:     "Ordering, delivery, returns and invoices at goen.",
	})
	KeyFAQEmpty = key("faq.empty", Message{
		ZhHant: "還沒有整理常見問題。有疑問請",
		En:     "No questions written up yet. If you have one, ",
	})
	KeyShippingTitle       = key("shipping.title", Message{ZhHant: "配送說明", En: "Delivery"})
	KeyShippingDescription = key("shipping.description", Message{
		ZhHant: "goen 的配送方式、運費與免運門檻。",
		En:     "Delivery methods, charges and the free-delivery threshold.",
	})
	KeyShippingSub = key("shipping.sub", Message{
		ZhHant: "以下金額直接來自系統實際計費的設定。",
		En:     "These figures come straight from what the checkout actually charges.",
	})
	KeyShippingMethods = key("shipping.methods", Message{
		ZhHant: "配送方式與運費",
		En:     "Methods and charges",
	})
	KeyShippingNoMethods = key("shipping.nomethods", Message{
		ZhHant: "目前沒有可用的配送方式。",
		En:     "No delivery methods are available at the moment.",
	})
	KeyShippingFreeOver = key("shipping.freeover", Message{ZhHant: "滿 %s 免運", En: "Free over %s"})
	KeyShippingZoneNote = key("shipping.zonenote", Message{
		ZhHant: "%s(免運不含)",
		En:     "%s (not covered by free delivery)",
	})

	KeyShippingZoneSurcharge = key("shipping.zonesurcharge", Message{
		ZhHant: "%s 另加 %s",
		En:     "%s costs %s extra",
	})

	KeyListSeparator = key("common.listseparator", Message{
		ZhHant: "、",
		En:     ", ",
	})
	KeyShippingTracking = key("shipping.tracking", Message{ZhHant: "出貨與追蹤", En: "Dispatch and tracking"})
	KeyShippingHold     = key("shipping.hold", Message{ZhHant: "庫存保留", En: "Stock reservation"})
	KeyShippingHoldBody = key("shipping.hold.body", Message{
		ZhHant: "送出訂單時系統會保留庫存 %s 分鐘,讓您完成付款。超過時間未付款,商品會回到架上供其他人購買。",
		En: "Placing an order holds the stock for %s minutes while you pay. If the payment " +
			"does not arrive, the goods go back on the shelf for somebody else.",
	})

	KeyPageNotFound      = key("site.notfound", Message{ZhHant: "找不到這個頁面", En: "Page not found"})
	KeyPageNotFoundTitle = key("site.notfound.title", Message{
		ZhHant: "找不到頁面",
		En:     "Not found",
	})
	KeyPageNotFoundBody = key("site.notfound.body", Message{
		ZhHant: "這個網址目前沒有對應的內容。商品、分類與結帳流程會在後續批次陸續上線。",
		En:     "Nothing lives at this address. Have a look at what else there is.",
	})

	KeyShippingTrackingBody = key("shipping.tracking.body", Message{
		ZhHant: "付款完成後我們會開始備貨。出貨時會記錄物流商與查詢編號,您可以在訂單頁看到,系統也會寄信通知。",
		En: "We start packing once the payment clears. When it ships we record the carrier " +
			"and the tracking number: both appear on your order page, and an email goes out.",
	})
	KeyPolicyContactLink = key("policy.contact", Message{ZhHant: "聯絡我們", En: "get in touch"})
	KeyPolicyFAQLink     = key("policy.faq", Message{ZhHant: "常見問題", En: "FAQ"})
)
