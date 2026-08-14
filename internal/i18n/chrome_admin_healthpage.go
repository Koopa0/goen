package i18n

// The words /admin/health's TEMPLATE writes.
//
// The figures themselves are in chrome_admin_views.go, beside the view model
// that computes them. These are the page around them: the sentence that says
// where every number comes from, the name and the explanation for each of the
// five workers, and the two tables that name WHICH message and WHICH refund an
// operator has to act on.
//
// Each worker's explanation says what breaks when that worker stops, not what
// the worker does. That is the sentence somebody reads at the moment a row goes
// red, and it is the difference between a number and a decision.

var (
	// The page's own claim about itself: every figure is derived from the WORK,
	// never from a heartbeat. A worker looping without progress passes "I am
	// running" and fails everything below, which is the whole reason the line is
	// on the page rather than only in the code.
	KeyAdminHPLead = key("admin.hp.lead", Message{
		ZhHant: "這些數字全部是從「工作有沒有被做完」算出來的,不是從 worker 自己回報的心跳 —— 一個空轉的 worker 心跳正常,但工作沒有前進。",
		En: "Every figure here is derived from whether the WORK has been done, not from a heartbeat a " +
			"worker reports about itself — a worker spinning without progress has a perfectly healthy " +
			"heartbeat while nothing moves.",
	})
	// The badge covers refunds as well as the four workers, so it answers "is
	// there anything nobody has been told about" rather than "are the workers
	// running".
	KeyAdminHPAllClear  = key("admin.hp.allclear", Message{ZhHant: "一切正常", En: "All clear"})
	KeyAdminHPNeedsLook = key("admin.hp.needslook", Message{
		ZhHant: "有需要看的地方",
		En:     "Something needs a look",
	})

	// The five rows, each a name and what its failure costs.
	KeyAdminHPOutboxName = key("admin.hp.outbox.name", Message{
		ZhHant: "通知信件(outbox)",
		En:     "Notification email (outbox)",
	})
	KeyAdminHPOutboxNote = key("admin.hp.outbox.note", Message{
		ZhHant: "寄信在訂單的同一個交易裡排入,由背景 worker 送出。卡住代表顧客收不到通知。",
		En: "Mail is enqueued inside the order's own transaction and sent by a background worker. " +
			"Stuck means customers are not being told anything.",
	})
	KeyAdminHPHoldsName = key("admin.hp.holds.name", Message{
		ZhHant: "庫存保留清掃",
		En:     "Stock-hold sweeper",
	})
	KeyAdminHPHoldsNote = key("admin.hp.holds.note", Message{
		ZhHant: "沒付款的訂單保留的庫存要放回架上。堆積代表有貨卻賣不出去。",
		En: "Stock held by an unpaid order has to go back on the shelf. A backlog means goods that are " +
			"in the warehouse and cannot be sold.",
	})
	KeyAdminHPProjectionName = key("admin.hp.projection.name", Message{
		ZhHant: "買了又買投影",
		En:     "Bought-together projection",
	})
	KeyAdminHPProjectionNote = key("admin.hp.projection.note", Message{
		ZhHant: "每 15 分鐘重建一次。過期只影響推薦的新鮮度,不影響任何交易。",
		En: "Rebuilt every 15 minutes. Staleness only affects how fresh the recommendations are; it " +
			"affects no transaction.",
	})
	KeyAdminHPRefundsName = key("admin.hp.refunds.name", Message{ZhHant: "退款", En: "Refunds"})
	// Nothing settles a refund by itself — goen consumes no refund webhook — so
	// the note has to say that, or an operator waits for a worker that does not
	// exist.
	KeyAdminHPRefundsNote = key("admin.hp.refunds.note", Message{
		ZhHant: "退款的紀錄是在打金流之前就寫進資料庫的,這樣中途斷線也留得下線索 —— 但沒有任何人在看那張表。這裡就是在看。沒有任何背景作業會自己把它結掉。",
		En: "A refund is written to the database BEFORE the payment provider is called, so a crash " +
			"half-way through still leaves a trail — but nobody was reading that table. This is the " +
			"reading of it. No background job ever closes one of these on its own.",
	})
	KeyAdminHPHousekeepingName = key("admin.hp.housekeeping.name", Message{
		ZhHant: "清理",
		En:     "Housekeeping",
	})
	KeyAdminHPHousekeepingNote = key("admin.hp.housekeeping.note", Message{
		ZhHant: "過期 session 每 6 小時、沒被引用的圖片每小時清一次。兩者都不影響任何答案 —— 讀取本來就會擋過期 session —— 堆積的是資料表本身。",
		En: "Expired sessions are swept every 6 hours and unreferenced images every hour. Neither " +
			"changes any answer — a read already refuses an expired session — what piles up is the " +
			"table itself.",
	})

	// The stuck-message table. It exists because a count cannot be acted on:
	// these have exhausted their attempts and will not come back by themselves.
	KeyAdminHPStuckHeading = key("admin.hp.stuck.heading", Message{
		ZhHant: "重試次數用盡的訊息",
		En:     "Messages that have exhausted their retries",
	})
	// 主題 here is the outbox TOPIC — order.paid, order.shipped — and not the
	// subject line of a letter, which is why it is not the contact form's key.
	KeyAdminHPColTopic     = key("admin.hp.col.topic", Message{ZhHant: "主題", En: "Topic"})
	KeyAdminHPColKey       = key("admin.hp.col.key", Message{ZhHant: "識別碼", En: "Key"})
	KeyAdminHPColAttempts  = key("admin.hp.col.attempts", Message{ZhHant: "次數", En: "Attempts"})
	KeyAdminHPColLastError = key("admin.hp.col.lasterror", Message{
		ZhHant: "最後一次的錯誤",
		En:     "Last error",
	})
	KeyAdminHPColSince = key("admin.hp.col.since", Message{ZhHant: "自從", En: "Since"})

	// The open-refund table. Two identifiers rather than one: the provider's own
	// reference is blank exactly when goen never heard an answer, and the key it
	// sent is then the only handle on what happened at the provider.
	KeyAdminHPOpenRefundsHeading = key("admin.hp.openrefunds.heading", Message{
		ZhHant: "還沒退成功的退款",
		En:     "Refunds that have not gone through",
	})
	KeyAdminHPColProviderRef = key("admin.hp.col.providerref", Message{
		ZhHant: "金流端編號",
		En:     "Provider reference",
	})
	KeyAdminHPColSentKey = key("admin.hp.col.sentkey", Message{
		ZhHant: "送出的識別碼",
		En:     "Key goen sent",
	})
	KeyAdminHPColStarted = key("admin.hp.col.started", Message{ZhHant: "開始於", En: "Started"})
)
