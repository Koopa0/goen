package i18n

var (
	KeyAdminEyebrow = key("admin.eyebrow", Message{ZhHant: "後台", En: "Back office"})

	KeyAdminAuditLead = key("admin.audit.lead", Message{
		ZhHant: "誰在什麼時候做了什麼。這份紀錄只能新增,寫進去就改不了也刪不掉。",
		En: "Who did what, and when. This record is append-only: nothing written here " +
			"can be changed or removed.",
	})
	KeyAdminAuditEmpty = key("admin.audit.empty", Message{
		ZhHant: "還沒有任何紀錄。",
		En:     "Nothing recorded yet.",
	})

	KeyAdminMessagesLead = key("admin.messages.lead", Message{
		ZhHant: "等最久的排在最前面 —— 三天前寫信的人比今天早上寫的更急,最新排在前面剛好把他埋掉。",
		En: "The longest wait comes first — somebody who wrote three days ago is more urgent " +
			"than somebody who wrote this morning, and newest-first would bury them exactly then.",
	})
	KeyAdminMessagesOpen  = key("admin.messages.open", Message{ZhHant: "待處理 %s", En: "%s open"})
	KeyAdminMessagesEmpty = key("admin.messages.empty", Message{
		ZhHant: "沒有任何聯絡訊息。",
		En:     "No contact messages.",
	})
	KeyAdminMessagesOrder = key("admin.messages.order", Message{ZhHant: "訂單", En: "Order"})

	KeyAdminReviewsLead = key("admin.reviews.lead", Message{
		ZhHant: "評價寫完就顯示,不先審 —— 每一則都要人核准的評價頁,讀起來就是廣告。隱藏是例外,而且會把那一則從評分裡一起拿掉。",
		En: "A review appears as soon as it is written, with no queue in front of it — a review page " +
			"where every entry was approved by the shop reads as advertising. Hiding is the exception, " +
			"and it takes that review out of the rating as well as off the page.",
	})
	KeyAdminReviewsEmpty = key("admin.reviews.empty", Message{ZhHant: "還沒有任何評價。", En: "No reviews yet."})
	KeyAdminReviewStars  = key("admin.review.stars", Message{ZhHant: "%s 分", En: "%s out of 5"})
	KeyAdminReviewBought = key("admin.review.bought", Message{ZhHant: "· 已購買", En: "· verified purchase"})
	KeyAdminReviewHidden = key("admin.review.hidden", Message{
		ZhHant: "· 已隱藏(不計入評分)",
		En:     "· hidden (not counted in the rating)",
	})

	KeyAdminCreditLead = key("admin.credit.lead", Message{
		ZhHant: "發放的額度會在該會員下次結帳時自動折抵。金額以「元」為單位。",
		En: "Credit granted here is spent automatically at that customer's next checkout. " +
			"Amounts are in whole New Taiwan dollars.",
	})
	KeyAdminCreditEmail  = key("admin.credit.email", Message{ZhHant: "會員 Email", En: "Customer email"})
	KeyAdminCreditAmount = key("admin.credit.amount", Message{ZhHant: "金額(元)", En: "Amount (NT$)"})
	KeyAdminCreditReason = key("admin.credit.reason", Message{ZhHant: "事由", En: "Reason"})
	KeyAdminCreditGrant  = key("admin.credit.grant", Message{ZhHant: "發放額度", En: "Grant credit"})
	KeyAdminCreditRecent = key("admin.credit.recent", Message{ZhHant: "最近的異動", En: "Recent postings"})
	KeyAdminCreditEmpty  = key("admin.credit.empty", Message{
		ZhHant: "還沒有任何額度異動。",
		En:     "No credit postings yet.",
	})

	KeyAdminQuestionsLead = key("admin.questions.lead", Message{
		ZhHant: "等最久的排在最前面 —— 問了三天沒人回的比今天早上剛問的更急。回覆會標示「官方回覆」,並排在該問題的最上面。",
		En: "The longest wait comes first — a question nobody answered for three days is more urgent " +
			"than one asked this morning. Your reply is marked as the shop's and sorts above the rest.",
	})
	KeyAdminQuestionsWaiting = key("admin.questions.waiting", Message{
		ZhHant: "%s 則待回覆",
		En:     "%s awaiting an answer",
	})
	KeyAdminQuestionsEmpty  = key("admin.questions.empty", Message{ZhHant: "還沒有人提問", En: "Nobody has asked anything yet"})
	KeyAdminQuestionAnswers = key("admin.question.answers", Message{ZhHant: "%s 則回覆", En: "%s replies"})
	KeyAdminQuestionReply   = key("admin.question.reply", Message{ZhHant: "回覆", En: "Reply"})
	KeyAdminQuestionHint    = key("admin.question.hint", Message{
		ZhHant: "以 goen 的名義回覆",
		En:     "Reply as goen",
	})
	KeyAdminQuestionSend = key("admin.question.send", Message{ZhHant: "送出官方回覆", En: "Post the shop's reply"})
	KeyAdminQuestionHide = key("admin.question.hide", Message{ZhHant: "隱藏這則提問", En: "Hide this question"})
)

var (
	KeyAdminWarrantyLead = key("admin.warranty.lead", Message{
		ZhHant: "用序號或訂單編號查一件的保固。兩個都要完全相符 —— 序號是從機身上唸出來的,訂單編號是從確認信上唸出來的,而登錄名單不是拿來瀏覽的。",
		En: "Look a unit's cover up by serial number or order number. Both match exactly — a serial is " +
			"read off the machine and an order number off a confirmation email, and a list of " +
			"registrations is not something to browse.",
	})
	KeyAdminWarrantySearch      = key("admin.warranty.search", Message{ZhHant: "查詢保固", En: "Search warranties"})
	KeyAdminWarrantyPlaceholder = key("admin.warranty.placeholder", Message{
		ZhHant: "序號或訂單編號",
		En:     "Serial or order number",
	})
	KeyAdminSearchButton = key("admin.search.button", Message{ZhHant: "查詢", En: "Search"})
	KeyAdminSearchShort  = key("admin.search.short", Message{
		ZhHant: "查詢字串太短,至少要兩個字。",
		En:     "That search is too short — two characters at least.",
	})
	KeyAdminWarrantyNoneFound = key("admin.warranty.nonefound", Message{
		ZhHant: "找不到「%s」的登錄紀錄。序號和訂單編號都是完全比對,如果是客人唸錯一碼就會查不到 —— 也可能是這一件根本沒登錄過。",
		En: "No registration matches %q. Both fields match exactly, so one wrong character finds " +
			"nothing — and it may simply never have been registered.",
	})
	KeyAdminColSerial     = key("admin.col.serial", Message{ZhHant: "序號", En: "Serial"})
	KeyAdminColProduct    = key("admin.col.product", Message{ZhHant: "商品", En: "Product"})
	KeyAdminColOrder      = key("admin.col.order", Message{ZhHant: "訂單", En: "Order"})
	KeyAdminColCustomer   = key("admin.col.customer", Message{ZhHant: "客戶", En: "Customer"})
	KeyAdminColRegistered = key("admin.col.registered", Message{ZhHant: "登錄日", En: "Registered"})
	KeyAdminColExpires    = key("admin.col.expires", Message{ZhHant: "保固到期", En: "Cover ends"})
	KeyAdminUnitNo        = key("admin.unit.no", Message{ZhHant: "第 %s 件", En: "unit %s"})
	KeyAdminWarrantyClock = key("admin.warranty.clock", Message{
		ZhHant: "保固從送達那天起算,不是從出貨那天 —— 到期日是登錄當下用該筆包裹的送達時間和商品保固月數算出來的,存下來就不再變動。",
		En: "Cover runs from the day the parcel ARRIVED, not the day it was dispatched. The end date is " +
			"computed at registration from that parcel's delivery time and the product's term, and does " +
			"not move afterwards.",
	})

	KeyAdminStockLink    = key("admin.stock.link", Message{ZhHant: "庫存", En: "Stock"})
	KeyAdminStockNowSafe = key("admin.stock.nowsafe", Message{
		ZhHant: "· 目前 %s 件,安全庫存 %s",
		En:     "· %s in stock, safety level %s",
	})
	KeyAdminLedgerEmpty = key("admin.ledger.empty", Message{
		ZhHant: "這個規格還沒有任何異動。新規格的庫存是 0,所有的量都從這張帳本進來。",
		En: "Nothing has moved for this variant yet. A new variant starts at zero, and every unit it " +
			"ever holds arrives through this ledger.",
	})
	KeyAdminColWhen    = key("admin.col.when", Message{ZhHant: "時間", En: "When"})
	KeyAdminColChange  = key("admin.col.change", Message{ZhHant: "異動", En: "Change"})
	KeyAdminColReason  = key("admin.col.reason", Message{ZhHant: "原因", En: "Reason"})
	KeyAdminColKind    = key("admin.col.kind", Message{ZhHant: "種類", En: "Kind"})
	KeyAdminHPColEvent = key("admin.hp.col.event", Message{ZhHant: "事件編號", En: "Event"})
	// The only thing that can be done about money against a cancelled order is a
	// refund by hand at the provider; this records that somebody did it.
	KeyAdminHPReconcile = key("admin.hp.reconcile", Message{
		ZhHant: "已手動退款",
		En:     "Refunded by hand",
	})
	// Money at the provider against goods the shop has already taken back. The
	// only case goen writes today is a capture that arrived after a cancel: the
	// database refuses it, Stripe is told the event was handled because retrying
	// changes nothing, and somebody has to refund it by hand.
	KeyAdminHPUnreconciledHeading = key("admin.hp.unreconciled", Message{
		ZhHant: "收到但無法處理的款項",
		En:     "Payments accepted and not applied",
	})
	KeyAdminHPUnreconciledHint = key("admin.hp.unreconciled.hint", Message{
		ZhHant: "錢在金流商那裡,商品已經回到架上 —— 每一筆都要到 Stripe 後台手動退款。",
		En: "The money is at the payment provider and the goods are back on the shelf. " +
			"Each of these has to be refunded by hand in the Stripe dashboard.",
	})
	KeyAdminColActor   = key("admin.col.actor", Message{ZhHant: "操作者", En: "By"})
	KeyAdminColBalance = key("admin.col.balance", Message{ZhHant: "結存", En: "Balance"})
	KeyAdminLedgerFoot = key("admin.ledger.foot", Message{
		ZhHant: "最近 50 筆。結存是從帳本開頭累加到那一筆的數字,所以就算只看這一頁也是對的。",
		En: "The last 50 movements. The balance accumulates from the start of the ledger rather than " +
			"from this page, so these rows are still true on their own.",
	})
	KeyAdminReceiveQty    = key("admin.receive.qty", Message{ZhHant: "進貨數量", En: "Quantity received"})
	KeyAdminReceiveButton = key("admin.receive.button", Message{ZhHant: "登記進貨", En: "Record receipt"})
	KeyAdminReceiveHint   = key("admin.receive.hint", Message{
		ZhHant: "這會在帳本上記一筆「進貨」。數字算錯要往回修的話請用庫存頁的「調整」—— 東西進來和數字算錯是兩件事,帳本要分得出來。",
		En: "This writes a goods receipt to the ledger. To correct a count downward use Adjust on the " +
			"stock page — goods arriving and a number being wrong are two different things, and the " +
			"ledger has to keep them apart.",
	})

	KeyAdminStaffLead = key("admin.staff.lead", Message{
		ZhHant: "兩階段驗證擋的是 /admin,不是登入。沒有啟用的人只用密碼就能進來。",
		En: "Two-factor guards /admin, not signing in. Anybody who has not enrolled reaches the back " +
			"office with a password alone.",
	})
	KeyAdminStaffUnprotected = key("admin.staff.unprotected", Message{
		ZhHant: "還有 %s 個帳號只用密碼就能進後台。請他們到 /admin/verify 啟用。",
		En:     "%s accounts still reach the back office with a password alone. Ask them to enrol at /admin/verify.",
	})
	KeyAdminColPerson    = key("admin.col.person", Message{ZhHant: "人員", En: "Person"})
	KeyAdminColRole      = key("admin.col.role", Message{ZhHant: "角色", En: "Role"})
	KeyAdminColTwoFa     = key("admin.col.twofa", Message{ZhHant: "兩階段驗證", En: "Two-factor"})
	KeyAdminStaffYou     = key("admin.staff.you", Message{ZhHant: "你自己", En: "you"})
	KeyAdminStaffDropFa  = key("admin.staff.dropfa", Message{ZhHant: "解除兩階段", En: "Remove two-factor"})
	KeyAdminStaffRevoke  = key("admin.staff.revoke", Message{ZhHant: "移除權限", En: "Revoke access"})
	KeyAdminStaffAdd     = key("admin.staff.add", Message{ZhHant: "新增人員", En: "Add a colleague"})
	KeyAdminStaffAddLead = key("admin.staff.addlead", Message{
		ZhHant: "不會設定密碼 —— 對方用「忘記密碼」自己設,那是唯一能證明信箱是他的路徑。帳號在他設定之前無法登入。已經是顧客的信箱會直接升級,不會另開一個。",
		En: "No password is set here — they set their own through Forgot password, which is the one " +
			"path that proves they own the mailbox. The account cannot sign in until they do. An " +
			"address that already belongs to a customer is promoted rather than duplicated.",
	})
	KeyAdminStaffEmail = key("admin.staff.email", Message{ZhHant: "電子郵件", En: "Email"})
	KeyAdminStaffName  = key("admin.staff.name", Message{ZhHant: "姓名(選填)", En: "Name (optional)"})
	KeyAdminAddButton  = key("admin.add.button", Message{ZhHant: "新增", En: "Add"})
)
