package i18n

var (
	KeyAdminColKind = key("admin.col.kind", Message{ZhHant: "種類", En: "Kind"})

	KeyAdminHPLead = key("admin.hp.lead", Message{
		ZhHant: "這些數字依實際完成的工作計算，背景作業空轉時也看得出來。",
		En:     "These figures count work actually done, so a background job that runs without progress still shows here.",
	})

	KeyAdminHPAllClear = key("admin.hp.allclear", Message{ZhHant: "一切正常", En: "All clear"})

	KeyAdminHPNeedsLook = key("admin.hp.needslook", Message{
		ZhHant: "有需要看的地方",
		En:     "Something needs a look",
	})

	KeyAdminHPOutboxName = key("admin.hp.outbox.name", Message{
		ZhHant: "通知信件（outbox）",
		En:     "Notification email (outbox)",
	})

	KeyAdminHPOutboxNote = key("admin.hp.outbox.note", Message{
		ZhHant: "寄信在訂單的同一個交易裡排入，由背景 worker 送出。卡住代表顧客收不到通知。",
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
		ZhHant: "每 15 分鐘重建一次。過期只影響推薦的新鮮度，不影響任何交易。",
		En: "Rebuilt every 15 minutes. Staleness only affects how fresh the recommendations are; it " +
			"affects no transaction.",
	})

	KeyAdminHPRefundsName = key("admin.hp.refunds.name", Message{ZhHant: "退款", En: "Refunds"})

	KeyAdminHPRefundsNote = key("admin.hp.refunds.note", Message{
		ZhHant: "尚未完成的退款，包括送往 Stripe 途中中斷的。這些不會自動結案。",
		En:     "Refunds that have not completed, including any interrupted on the way to Stripe. None of them closes on its own.",
	})

	KeyAdminHPDisputesName = key("admin.hp.disputes.name", Message{ZhHant: "待回應的爭議款", En: "Disputes"})

	KeyAdminHPDisputesNote = key("admin.hp.disputes.note", Message{
		ZhHant: "即時向 Stripe 查詢。持卡人提出爭議後，期限內沒有回應，款項就會被收回。",
		En:     "Read live from Stripe. A dispute takes the money back unless the shop responds before its deadline.",
	})

	KeyAdminHPDisputesHeading = key("admin.hp.disputes.heading", Message{
		ZhHant: "等你回應的爭議款",
		En:     "Disputes waiting for a response",
	})

	KeyAdminHPColRespondBy = key("admin.hp.col.respondby", Message{ZhHant: "回應期限", En: "Respond by"})

	KeyAdminHPColDispute = key("admin.hp.col.dispute", Message{ZhHant: "爭議款", En: "Dispute"})

	KeyAdminHPDisputeOpen = key("admin.hp.dispute.open", Message{ZhHant: "到 Stripe 處理", En: "Open in Stripe"})

	KeyAdminHPDisputeNoOrder = key("admin.hp.dispute.noorder", Message{
		ZhHant: "找不到對應的訂單",
		En:     "No matching order",
	})

	KeyAdminHPDisputeOrderUnknown = key("admin.hp.dispute.orderunknown", Message{
		ZhHant: "訂單無法查詢",
		En:     "Order could not be looked up",
	})

	KeyAdminHPDisputeNoDeadline = key("admin.hp.dispute.nodeadline", Message{
		ZhHant: "銀行不接受回應",
		En:     "The bank accepts no response",
	})

	KeyAdminHPHousekeepingName = key("admin.hp.housekeeping.name", Message{
		ZhHant: "清理",
		En:     "Housekeeping",
	})

	KeyAdminHPHousekeepingNote = key("admin.hp.housekeeping.note", Message{
		ZhHant: "過期 session 每 6 小時、沒被引用的圖片每小時清一次。堆積只占用空間，不影響任何頁面或交易。",
		En:     "Expired sessions are swept every 6 hours and unreferenced images every hour. A backlog takes up space but changes no page or transaction.",
	})

	KeyAdminHPStuckHeading = key("admin.hp.stuck.heading", Message{
		ZhHant: "重試次數用盡的訊息",
		En:     "Messages that have exhausted their retries",
	})

	KeyAdminHPColTopic = key("admin.hp.col.topic", Message{ZhHant: "主題", En: "Topic"})

	KeyAdminHPColKey = key("admin.hp.col.key", Message{ZhHant: "識別碼", En: "Key"})

	KeyAdminHPColAttempts = key("admin.hp.col.attempts", Message{ZhHant: "次數", En: "Attempts"})

	KeyAdminTechnicalDetails = key("admin.hp.technicaldetails", Message{ZhHant: "技術細節", En: "Technical details"})

	KeyAdminHPColLastError = key("admin.hp.col.lasterror", Message{
		ZhHant: "最後一次的錯誤",
		En:     "Last error",
	})

	// This column is available_at: when the message next becomes DUE, pushed
	// forward by every claim and backoff. No column records when a message became
	// stuck (created_at is when it was queued), so the page must not claim how
	// long it has been broken.
	KeyAdminHPColNextRetry = key("admin.hp.col.nextretry", Message{ZhHant: "下次重試", En: "Next retry"})

	KeyAdminHPPoolsHeading = key("admin.hp.pools.heading", Message{
		ZhHant: "資料庫連線池",
		En:     "Database connection pools",
	})
	KeyAdminHPPoolsHint = key("admin.hp.pools.hint", Message{
		ZhHant: "「等待連線次數」不是零、累計等待時間持續變長，代表連線不夠用。",
		En: "Empty acquires above zero with a growing wait mean requests are " +
			"queuing for a connection.",
	})
	KeyAdminHPColPool         = key("admin.hp.col.pool", Message{ZhHant: "連線池", En: "Pool"})
	KeyAdminHPColPoolMax      = key("admin.hp.col.poolmax", Message{ZhHant: "上限", En: "Max"})
	KeyAdminHPColPoolAcquired = key("admin.hp.col.poolacquired", Message{ZhHant: "使用中", En: "Acquired"})
	KeyAdminHPColPoolIdle     = key("admin.hp.col.poolidle", Message{ZhHant: "閒置", En: "Idle"})
	KeyAdminHPColPoolTotal    = key("admin.hp.col.pooltotal", Message{ZhHant: "已建立", En: "Open"})
	KeyAdminHPColPoolAcquires = key("admin.hp.col.poolacquires", Message{ZhHant: "取用次數", En: "Total acquires"})
	KeyAdminHPColPoolEmpty    = key("admin.hp.col.poolempty", Message{ZhHant: "等待連線次數", En: "Empty acquires"})
	KeyAdminHPColPoolWait     = key("admin.hp.col.poolwait", Message{ZhHant: "累計等待", En: "Cumulative wait"})

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

	KeyAdminHPUninvoicedHeading = key("admin.hp.uninvoiced.heading", Message{
		ZhHant: "沒有發票作業的已收款訂單",
		En:     "Paid orders with no invoice operation",
	})

	KeyAdminHPUninvoicedHint = countKey("admin.hp.uninvoiced.hint",
		"%d 筆已收款的訂單沒有任何開立發票的作業：排入開立的訊息可能遺失或被刪除。請逐筆開立。",
		"%d paid order has no invoice operation: the message that queues its issue may have been "+
			"lost or deleted. Issue it.",
		"%d paid orders have no invoice operation: the messages that queue their issue may have been "+
			"lost or deleted. Issue each one.")

	KeyAdminHPColPaid = key("admin.hp.col.paid", Message{ZhHant: "收款於", En: "Paid"})

	KeyAdminHPCancelledOrderInvoicesHeading = key("admin.hp.cancelledorderinvoices.heading", Message{
		ZhHant: "已取消訂單仍有效的統一發票",
		En:     "Live invoices on cancelled orders",
	})

	KeyAdminHPCancelledOrderInvoicesHint = countKey("admin.hp.cancelledorderinvoices.hint",
		"%d 張統一發票的訂單已取消，發票卻沒有作廢或折讓：已過綠界的作廢期限、作廢被拒絕，或沒有設定加值中心。請到訂單頁處理。",
		"%d invoice belongs to a cancelled order and was neither voided nor credited: ECPay's void "+
			"deadline had passed, the void was refused, or no e-invoice provider is configured. "+
			"Correct it from its order page.",
		"%d invoices belong to cancelled orders and were neither voided nor credited: ECPay's void "+
			"deadline had passed, the void was refused, or no e-invoice provider is configured. "+
			"Correct each from its order page.")

	KeyAdminHPColIssued = key("admin.hp.col.issued", Message{ZhHant: "開立於", En: "Issued"})

	KeyAdminHPClaimsHeading = key("admin.hp.claims.heading", Message{
		ZhHant: "待確認的電子發票操作",
		En:     "E-invoice operations awaiting confirmation",
	})

	KeyAdminHPClaimsHint = key("admin.hp.claims.hint", Message{
		ZhHant: "系統會自動查詢綠界並收斂一般的逾時。這裡只列出過久仍未完成，或查到不一致、" +
			"多筆候選而已安全停住的操作。折讓只有在最後一次送出至少 15 分鐘後，才可能顯示重送授權；" +
			"授權前仍必須先到綠界依發票號碼確認折讓確實不存在。顧客未在 72 小時內確認的折讓也可授權重寄一次。" +
			"其他操作請勿手動重送。" +
			"綠界退回的自動開立也列在這裡，直到有人從訂單頁再開立一次。",
		En: "The worker automatically reconciles ordinary timeouts with ECPay. These operations are " +
			"aged or stopped on a mismatch/multiple candidates. An Allowance resend can be authorized only " +
			"after 15 minutes, and only after checking its invoice number in ECPay and confirming the allowance " +
			"is absent. One the customer did not agree to within 72 hours may also be resent once. " +
			"Do not manually resend any other operation. An automatic issue ECPay refused stays " +
			"here until someone issues the invoice again from the order page.",
	})

	KeyAdminHPAllowanceAbsentConfirm = key("admin.hp.allowance.absent", Message{
		ZhHant: "我已在綠界依發票號碼確認：這筆折讓不存在",
		En:     "I checked the invoice number in ECPay and confirmed this allowance is absent",
	})

	KeyAdminHPAllowanceLapsedConfirm = key("admin.hp.allowance.lapsed", Message{
		ZhHant: "顧客未在 72 小時內確認，再寄一次折讓確認信",
		En:     "The customer did not agree within 72 hours; e-mail them the credit note once more",
	})

	KeyAdminHPAllowanceResendAuthorize = key("admin.hp.allowance.authorize", Message{
		ZhHant: "授權一次重送",
		En:     "Authorize one resend",
	})

	KeyAdminHPEventSafeRelease = key("admin.hp.event.safe", Message{
		ZhHant: "確認已全額退款或已有成功入帳",
		En:     "Confirmed fully refunded/already accounted",
	})

	KeyAdminHPCompletePaymentsHeading = key("admin.hp.completepayments", Message{
		ZhHant: "待確認款項的 Stripe Session",
		En:     "Stripe Sessions awaiting a money outcome",
	})

	KeyAdminHPCompletePaymentsHint = key("admin.hp.completepayments.hint", Message{
		ZhHant: "Stripe 已將 Session 標為 complete，但系統沒有可套用的收款結果。請先到 Stripe 查清楚：若已收款，請入帳；只有確認未收款或已全額退款，才可允許顧客重新付款。這兩個結果不可混用。",
		En: "Stripe marked the Session complete, but goen has no applicable capture outcome. " +
			"Check Stripe first: post it as paid when money was taken; allow another attempt only " +
			"after confirming it was unpaid or fully refunded. These outcomes are not interchangeable.",
	})

	KeyAdminHPCompleteOutcomeUnknown = key("admin.hp.completepayments.unknown", Message{
		ZhHant: "Stripe 回報 Session complete，但尚無可套用的收款結果；請先在 Stripe 核對款項。",
		En:     "Stripe reports the Session complete, but no applicable capture outcome is posted; verify the money in Stripe first.",
	})

	KeyAdminHPCompleteStockReleased = key("admin.hp.completepayments.stockreleased", Message{
		ZhHant: "此訂單的庫存已退回可售；不可再入帳。請先在 Stripe 全額退款，再使用未收款／已退款結論解除閘門。",
		En:     "This order's stock was returned to sale, so capture cannot be posted. Fully refund it in Stripe, then use the unpaid/refunded outcome to release the gate.",
	})

	KeyAdminHPCompletePaid = key("admin.hp.completepayments.paid", Message{
		ZhHant: "確認已收款並入帳",
		En:     "Confirmed paid — post capture",
	})

	KeyAdminHPCompleteSafeToRetry = key("admin.hp.completepayments.retry", Message{
		ZhHant: "確認未收款或已全額退款，允許重新付款",
		En:     "Confirmed unpaid/refunded — allow retry",
	})

	KeyAdminHPUnreconciledHeading = key("admin.hp.unreconciled", Message{
		ZhHant: "需要處理的 Stripe 事件",
		En:     "Stripe events needing action",
	})

	KeyAdminHPRefundFailedAtStripe = key("admin.hp.unreconciled.refundfailed", Message{
		ZhHant: "系統已記為退款成功，但 Stripe 回報這筆退款失敗，款項已回到 Stripe 餘額。請用其他方式把錢還給顧客，再按「確認已全額退款或已有成功入帳」。",
		En:     "goen recorded this refund as succeeded, but Stripe reports it failed and the money is back in the Stripe balance. Repay the customer another way, then press “Confirmed fully refunded/already accounted”.",
	})

	KeyAdminHPUnreconciledHint = key("admin.hp.unreconciled.hint", Message{
		ZhHant: "請依原因與事件編號檢查 Stripe。只有確認款項已全額退款，或已有 succeeded 付款完整入帳，才可解除付款閘門；單純看過事件不算處理完成。",
		En: "Use the reason and event reference to investigate in Stripe. Release the payment gate only " +
			"after every cent was refunded or a succeeded payment already accounts for it; merely reading " +
			"the event is not a resolution.",
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
		ZhHant: "%d 封已用盡重試次數，不會再自動重試",
		En:     "%d have exhausted their retries and will not be retried automatically",
	})

	KeyHealthOutboxClear = key("health.outbox.clear", Message{ZhHant: "沒有待送的訊息", En: "Nothing waiting to send"})

	KeyHealthOutboxOverdue = key("health.outbox.overdue", Message{
		ZhHant: "%d 封待送，最久的已經逾期 %s",
		En:     "%d waiting, the oldest overdue by %s",
	})

	KeyHealthOutboxNotYetDue = key("health.outbox.notyetdue", Message{
		ZhHant: "%d 封待送，都還沒到重試時間",
		En:     "%d waiting, none of them due yet",
	})

	KeyHealthOutboxWaiting = key("health.outbox.waiting", Message{
		ZhHant: "%d 封待送，最久的逾期 %s",
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

	KeyHealthDisputesClear = key("health.disputes.clear", Message{
		ZhHant: "沒有等待回應的爭議款",
		En:     "No disputes waiting for a response",
	})

	KeyHealthDisputesUnknown = key("health.disputes.unknown", Message{
		ZhHant: "無法向 Stripe 查詢，不知道有沒有等待回應的爭議款，請直接到 Stripe 確認",
		En:     "Stripe could not be read, so whether a dispute is waiting is unknown; check Stripe directly",
	})

	KeyHealthDisputesOpen = countKey("health.disputes.open",
		"%d 筆爭議款等待回應",
		"%d dispute is waiting for a response",
		"%d disputes are waiting for a response")

	KeyHealthRefundsStuck = key("health.refunds.stuck", Message{
		ZhHant: "%d 筆退款尚未完成，顧客還沒收到款項",
		En:     "%d refunds have not gone through, so the money has not reached the customer",
	})

	KeyHealthProjectionNever = key("health.projection.never", Message{
		ZhHant: "從來沒有重建過",
		En:     "Never rebuilt",
	})

	KeyHealthProjectionAge = key("health.projection.age", Message{
		ZhHant: "上次重建於 %s前",
		En:     "Last rebuilt %s ago",
	})

	KeyHealthNoReason = key("health.noreason", Message{ZhHant: "（沒有記錄原因）", En: "(no reason recorded)"})

	KeyHealthNoRef = key("health.noref", Message{
		ZhHant: "（金流端沒有回覆編號）",
		En:     "(the provider returned no reference)",
	})

	KeyHealthRefundPending = key("health.refund.pending", Message{
		ZhHant: "已送出，還沒收到金流端的結果",
		En:     "Sent, no answer from the provider yet",
	})

	KeyHealthRefundAction = key("health.refund.action", Message{
		ZhHant: "金流端說還需要處理才會退出去",
		En:     "The provider says something more is needed before the money moves",
	})

	KeyHealthRefundFailed = key("health.refund.failed", Message{
		ZhHant: "金流端拒絕了這筆退款，錢沒有退出去",
		En:     "The provider refused this refund: no money moved",
	})

	KeyHealthRefundCancelled = key("health.refund.cancelled", Message{
		ZhHant: "金流端取消了這筆退款，錢沒有退出去",
		En:     "The provider cancelled this refund attempt: no money moved",
	})

	KeyHealthRefundPendingNext = key("health.refund.pending.next", Message{
		ZhHant: "請在 Stripe 查詢退款結果。",
		En:     "Check the refund status in Stripe.",
	})

	KeyHealthRefundActionNext = key("health.refund.action.next", Message{
		ZhHant: "請先查看 Stripe 顯示的退款處理指示。",
		En:     "Read the refund action instructions shown in Stripe first.",
	})

	KeyHealthRefundFailedNext = key("health.refund.failed.next", Message{
		ZhHant: "請在 Stripe 查明退款失敗原因。",
		En:     "Check why the refund failed in Stripe.",
	})

	KeyHealthRefundCancelledNext = key("health.refund.cancelled.next", Message{
		ZhHant: "請在 Stripe 查明這筆退款被取消的原因。",
		En:     "Check why this refund attempt was cancelled in Stripe.",
	})

	KeyHealthRefundRetryNext = key("health.refund.retry.next", Message{
		ZhHant: "goen 不會自動接續這筆退款；訂單或退貨頁若提供「%s」或「%s」，才可使用該操作核對並繼續退款。",
		En:     "goen does not automatically resume this refund; use “%s” or “%s” on the order or returns page only if offered to check and continue it.",
	})

	KeyHealthRefundExternalNext = key("health.refund.external.next", Message{
		ZhHant: "goen 不會自動接續這筆退款；請依 Stripe 顯示的狀態與指示處理，此頁不提供重試操作。",
		En:     "goen does not automatically resume this refund; follow the status and instructions shown in Stripe. This page offers no retry action.",
	})
)

var (
	KeyAdminNoticeReconciled = key("admin.notice.reconciled", Message{
		ZhHant: "已記錄為處理完成。",
		En:     "Recorded as handled.",
	})

	KeyAdminNoticeNotFlagged = key("admin.notice.notflagged", Message{
		ZhHant: "這筆事件已經處理過了。",
		En:     "That event has already been dealt with.",
	})

	KeyAdminNoticePaymentMustRefund = key("admin.notice.paymentmustrefund", Message{
		ZhHant: "庫存已退回可售，這筆款項不可入帳。請先在 Stripe 全額退款，再選擇「未收款或已全額退款」。",
		En:     "Stock was already returned to sale, so this payment cannot be posted. Fully refund it in Stripe, then choose the unpaid/refunded outcome.",
	})

	KeyAdminNoticeInvoiceQueued = key("admin.notice.invoicequeued", Message{
		ZhHant: "已留下操作人與請求紀錄，並只授權一次折讓重送。",
		En:     "The actor and request were recorded, and exactly one Allowance resend was authorized.",
	})
)
