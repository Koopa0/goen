package i18n

var (
	KeyAdminColKind = key("admin.col.kind", Message{ZhHant: "種類", En: "Kind"})

	KeyAdminHPLead = key("admin.hp.lead", Message{
		ZhHant: "這些數字全部是從「工作有沒有被做完」算出來的,不是從 worker 自己回報的心跳 —— 一個空轉的 worker 心跳正常,但工作沒有前進。",
		En: "Every figure here is derived from whether the WORK has been done, not from a heartbeat a " +
			"worker reports about itself — a worker spinning without progress has a perfectly healthy " +
			"heartbeat while nothing moves.",
	})

	KeyAdminHPAllClear = key("admin.hp.allclear", Message{ZhHant: "一切正常", En: "All clear"})

	KeyAdminHPNeedsLook = key("admin.hp.needslook", Message{
		ZhHant: "有需要看的地方",
		En:     "Something needs a look",
	})

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

	KeyAdminHPStuckHeading = key("admin.hp.stuck.heading", Message{
		ZhHant: "重試次數用盡的訊息",
		En:     "Messages that have exhausted their retries",
	})

	KeyAdminHPColTopic = key("admin.hp.col.topic", Message{ZhHant: "主題", En: "Topic"})

	KeyAdminHPColKey = key("admin.hp.col.key", Message{ZhHant: "識別碼", En: "Key"})

	KeyAdminHPColAttempts = key("admin.hp.col.attempts", Message{ZhHant: "次數", En: "Attempts"})

	KeyAdminHPColLastError = key("admin.hp.col.lasterror", Message{
		ZhHant: "最後一次的錯誤",
		En:     "Last error",
	})

	// available_at, which is when the message becomes DUE — pushed forward by
	// every claim and every backoff, so it reads as a future timestamp and is not
	// how long anything has been broken. outbox_messages has no created_at, so
	// "since" is not computable; naming the column for what it holds is the
	// honest option. Meaningless once the attempts are exhausted, which is
	// exactly when this table is read.
	KeyAdminHPColSince = key("admin.hp.col.since", Message{ZhHant: "下次重試", En: "Next retry"})

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

	KeyAdminPageHealth = key("admin.page.health", Message{ZhHant: "背景作業", En: "Background work"})

	KeyAdminHPColEvent = key("admin.hp.col.event", Message{ZhHant: "事件編號", En: "Event"})

	KeyAdminHPClaimsHeading = key("admin.hp.claims.heading", Message{
		ZhHant: "尚未確認的折讓",
		En:     "Credit notes awaiting confirmation",
	})

	KeyAdminHPClaimsHint = key("admin.hp.claims.hint", Message{
		ZhHant: "呼叫加值中心時沒有收到回應，無法確定發票是否已經開出。請到綠界後台查看，" +
			"再決定要作廢或重新開立。在確認之前，這張訂單無法再開折讓。",
		En: "The e-invoice provider did not answer, so whether the document was filed is unknown. " +
			"Check the ECPay console before voiding or re-filing. Until it is settled, " +
			"no further credit note can be filed against this order.",
	})

	KeyAdminHPReconcile = key("admin.hp.reconcile", Message{
		ZhHant: "標記為已處理",
		En:     "Mark handled",
	})

	KeyAdminHPUnreconciledHeading = key("admin.hp.unreconciled", Message{
		ZhHant: "需要處理的 Stripe 事件",
		En:     "Stripe events needing action",
	})

	KeyAdminHPUnreconciledHint = key("admin.hp.unreconciled.hint", Message{
		ZhHant: "請依原因與事件編號檢查 Stripe。確認版本、款項歸屬或手動退款後,再標記為已處理。",
		En: "Use the reason and event reference to investigate in Stripe. After checking the API version, " +
			"attributing the payment, or refunding it by hand, mark the event handled.",
	})

	KeyHealthSweeperClear = key("health.sweeper.clear", Message{
		ZhHant: "沒有待清理的過期 session 或未使用的圖片",
		En:     "No expired sessions or unreferenced images waiting to be swept",
	})

	KeyHealthSweeperBacklog = key("health.sweeper.backlog", Message{
		ZhHant: "%d 個過期 session、%d 張沒被引用的圖片還沒清掉",
		En:     "%d expired sessions and %d unreferenced images still to sweep",
	})

	KeyHealthOutboxStuck = key("health.outbox.stuck", Message{
		ZhHant: "%d 封重試次數用盡 —— 不會自己好",
		En:     "%d have exhausted their retries — these will not recover on their own",
	})

	KeyHealthOutboxClear = key("health.outbox.clear", Message{ZhHant: "沒有待送的訊息", En: "Nothing waiting to send"})

	KeyHealthOutboxOverdue = key("health.outbox.overdue", Message{
		ZhHant: "%d 封待送,最久的已經逾期 %s",
		En:     "%d waiting, the oldest overdue by %s",
	})

	KeyHealthOutboxNotYetDue = key("health.outbox.notyetdue", Message{
		ZhHant: "%d 封待送,都還沒到重試時間",
		En:     "%d waiting, none of them due yet",
	})

	KeyHealthOutboxWaiting = key("health.outbox.waiting", Message{
		ZhHant: "%d 封待送,最久的逾期 %s",
		En:     "%d waiting, the oldest overdue by %s",
	})

	KeyHealthHoldsClear = key("health.holds.clear", Message{
		ZhHant: "沒有過期未釋放的保留",
		En:     "No expired holds left unreleased",
	})

	KeyHealthHoldsStuck = key("health.holds.stuck", Message{
		ZhHant: "%d 筆過期的庫存保留還沒釋放",
		En:     "%d expired stock holds still unreleased",
	})

	KeyHealthRefundsClear = key("health.refunds.clear", Message{
		ZhHant: "沒有卡住的退款",
		En:     "No refunds stuck",
	})

	KeyHealthRefundsStuck = key("health.refunds.stuck", Message{
		ZhHant: "%d 筆退款還沒退成功 —— 顧客還沒拿到錢",
		En:     "%d refunds have not gone through — the customer does not have their money",
	})

	KeyHealthProjectionNever = key("health.projection.never", Message{
		ZhHant: "從來沒有重建過",
		En:     "Never rebuilt",
	})

	KeyHealthProjectionAge = key("health.projection.age", Message{
		ZhHant: "上次重建於 %s前",
		En:     "Last rebuilt %s ago",
	})

	KeyHealthNoReason = key("health.noreason", Message{ZhHant: "(沒有記錄原因)", En: "(no reason recorded)"})

	KeyHealthNoRef = key("health.noref", Message{
		ZhHant: "(金流端沒有回覆編號)",
		En:     "(the provider returned no reference)",
	})

	KeyHealthRefundPending = key("health.refund.pending", Message{
		ZhHant: "已送出,還沒收到金流端的結果",
		En:     "Sent, no answer from the provider yet",
	})

	KeyHealthRefundAction = key("health.refund.action", Message{
		ZhHant: "金流端說還需要處理才會退出去",
		En:     "The provider says something more is needed before the money moves",
	})

	KeyHealthRefundFailed = key("health.refund.failed", Message{
		ZhHant: "金流端拒絕了,錢沒有退出去,退貨也還沒結案",
		En:     "The provider refused it: no money moved, and the return is still open",
	})
)

var (
	// /admin/health's own two.
	KeyAdminNoticeReconciled = key("admin.notice.reconciled", Message{
		ZhHant: "已記錄為處理完成。",
		En:     "Recorded as handled.",
	})

	KeyAdminNoticeNotFlagged = key("admin.notice.notflagged", Message{
		ZhHant: "這筆事件已經處理過了。",
		En:     "That event has already been dealt with.",
	})
)
