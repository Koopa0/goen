package i18n

// What the back office's view models compute.
//
// These are the sentences a queue builds out of a number — "3 封待送,最久的逾期
// 12 分鐘" — rather than the labels a template writes. They carry %s and %d holes
// for the same reason every mail body here does: the SHAPE of a sentence belongs
// with its words, and a page that assembles one out of three catalogue lookups
// and two plus signs cannot be translated into a language that orders them
// differently.
//
// A DURATION is a hole too. `humanDuration` picks the unit and the message takes
// the whole phrase, so "12 分鐘前" and "12 minutes ago" are each one string
// rather than a number glued to a suffix that only works in one language.

var (
	// A count of days, used by two report figures.
	KeyAdminDays = key("admin.days", Message{ZhHant: "%d 天", En: "%d days"})
	// The units humanDuration picks between.
	KeyAdminSeconds = key("admin.seconds", Message{ZhHant: "%d 秒", En: "%d seconds"})
	KeyAdminMinutes = key("admin.minutes", Message{ZhHant: "%d 分鐘", En: "%d minutes"})
	KeyAdminHours   = key("admin.hours", Message{ZhHant: "%d 小時", En: "%d hours"})

	// An account that erase_user has taken away. The row survives — an order,
	// a review, a question is part of what happened — so the cell says which of
	// the two states it is rather than going blank.
	KeyAdminErasedAccount      = key("admin.erased.account", Message{ZhHant: "(已刪除帳號)", En: "(erased account)"})
	KeyAdminErasedAccountPlain = key("admin.erased.plain", Message{ZhHant: "已刪除的帳號", En: "Erased account"})
	// erase_user takes the customer's DETAILS and leaves the order, so the
	// recipient name goes and the row stays. These used to be coalesce()
	// fallbacks INSIDE the queries — chrome written where nobody can ask who is
	// reading, the same correction RecordCancellation's note and /shipping's
	// string_agg already took. The query returns the empty string now and the
	// view decides the word.
	KeyAdminErasedRecipient = key("admin.erased.recipient", Message{ZhHant: "(已抹除)", En: "(erased)"})
	KeyAdminErasedShort     = key("admin.erased.short", Message{ZhHant: "(已刪除)", En: "(deleted)"})

	// Moderating a review. Hiding takes it out of the SCORE as well as the list,
	// which is the whole point, and it is reversible.
	KeyAdminReviewShow = key("admin.review.show", Message{ZhHant: "恢復顯示", En: "Show again"})
	KeyAdminReviewHide = key("admin.review.hide", Message{ZhHant: "隱藏", En: "Hide"})

	// A product question. "Answered" means the SHOP answered: three customer
	// replies and no official one is still an unanswered question, and the
	// middle state says so rather than being folded into either end.
	KeyAdminQAnswered     = key("admin.question.answered", Message{ZhHant: "已回覆", En: "Answered"})
	KeyAdminQCustomerOnly = key("admin.question.customeronly", Message{
		ZhHant: "只有顧客回覆",
		En:     "Only customers replied",
	})
	KeyAdminQWaiting = key("admin.question.waiting", Message{ZhHant: "待回覆", En: "Awaiting an answer"})

	// A staff account's second factor. confirmed_at is what 已啟用 means:
	// somebody who mistyped the secret has working 2FA on paper and no way to
	// generate a code, so "started" is not "enabled".
	KeyAdminTOTPOn  = key("admin.totp.on", Message{ZhHant: "已啟用", En: "Enabled"})
	KeyAdminTOTPOff = key("admin.totp.off", Message{ZhHant: "尚未啟用", En: "Not enabled"})

	// A category's contents, in the one sentence the taxonomy page shows under
	// each row. Three shapes rather than one with zeroes in it: "0 個子分類" is
	// noise on a leaf category, and the page is read by scanning.
	KeyAdminTaxonomyBoth = key("admin.taxonomy.both", Message{
		ZhHant: "有 %s 個商品和 %d 個子分類",
		En:     "%s products and %d sub-categories",
	})
	KeyAdminTaxonomyChildren = key("admin.taxonomy.children", Message{
		ZhHant: "有 %d 個子分類",
		En:     "%d sub-categories",
	})
	KeyAdminTaxonomyProducts = key("admin.taxonomy.products", Message{
		ZhHant: "有 %s 個商品",
		En:     "%s products",
	})

	// A campaign's state. 進行中(沒有商品) is its own answer because a running
	// campaign with nothing featured is a live URL showing an empty page, which
	// reads as a bug to whoever followed the link.
	KeyAdminCampaignOff     = key("admin.campaign.off", Message{ZhHant: "已停用", En: "Switched off"})
	KeyAdminCampaignOutside = key("admin.campaign.outside", Message{ZhHant: "不在期間內", En: "Outside its window"})
	KeyAdminCampaignEmpty   = key("admin.campaign.empty", Message{
		ZhHant: "進行中(沒有商品)",
		En:     "Running (nothing featured)",
	})
	KeyAdminCampaignRunning = key("admin.campaign.running", Message{ZhHant: "進行中", En: "Running"})
	// One control, both directions, so the label is the ACT and not the state.
	KeyAdminToggleOff = key("admin.toggle.off", Message{ZhHant: "停用", En: "Switch off"})
	KeyAdminToggleOn  = key("admin.toggle.on", Message{ZhHant: "啟用", En: "Switch on"})

	// A contact message's age. Oldest first on that page, so the wait is what a
	// staff member scans — and 今天 rather than "0 天" because a message that
	// arrived this morning is not a message that has waited no days.
	KeyAdminMsgHandled = key("admin.message.handled", Message{ZhHant: "已處理", En: "Handled"})
	KeyAdminMsgToday   = key("admin.message.today", Message{ZhHant: "今天", En: "Today"})
	KeyAdminMsgOneDay  = key("admin.message.oneday", Message{ZhHant: "等了 1 天", En: "Waiting 1 day"})
	KeyAdminMsgDays    = key("admin.message.days", Message{ZhHant: "等了 %d 天", En: "Waiting %d days"})
	KeyAdminMsgReopen  = key("admin.message.reopen", Message{ZhHant: "重新開啟", En: "Reopen"})
	KeyAdminMsgHandle  = key("admin.message.handle", Message{ZhHant: "標記已處理", En: "Mark handled"})

	// Where a delivery method sends a parcel. destination_kind decides which
	// half of the checkout exists, so the two are named rather than implied.
	KeyAdminDestAddress = key("admin.dest.address", Message{ZhHant: "宅配地址", En: "Home address"})
	KeyAdminDestPickup  = key("admin.dest.pickup", Message{ZhHant: "超商門市", En: "Convenience store"})
	KeyAdminNone        = key("admin.none", Message{ZhHant: "無", En: "None"})

	// A variant's stock, on the product page.
	KeyAdminStockOf = key("admin.stock.of", Message{
		ZhHant: "%d 件(可售 %d)",
		En:     "%d in stock (%d sellable)",
	})
)

var (
	// A coupon, as the list describes it.
	KeyAdminCouponCap = key("admin.coupon.cap", Message{ZhHant: "(上限 %s)", En: "(capped at %s)"})
	KeyAdminCouponMin = key("admin.coupon.min", Message{ZhHant: "滿 %s", En: "over %s"})
	// 限量 is a TOTAL across every customer; 每人 is per customer. Two different
	// limits, counted from coupon_redemptions under redeem_coupon's lock.
	KeyAdminCouponTotalLimit = key("admin.coupon.totallimit", Message{ZhHant: "限量 %d", En: "%d in total"})
	KeyAdminCouponPerPerson  = key("admin.coupon.perperson", Message{ZhHant: "每人 %d 次", En: "%d per customer"})
	KeyAdminCouponUsed       = key("admin.coupon.used", Message{ZhHant: "%d 次", En: "%d used"})
	KeyAdminCouponGiven      = key("admin.coupon.given", Message{ZhHant: " · 已折抵 %s", En: " · %s discounted"})
	KeyAdminCouponOff        = key("admin.coupon.off", Message{ZhHant: "已停用", En: "Switched off"})
	KeyAdminCouponOutside    = key("admin.coupon.outside", Message{ZhHant: "不在期間內", En: "Outside its window"})
	KeyAdminCouponLive       = key("admin.coupon.live", Message{ZhHant: "使用中", En: "Live"})

	// The order queue's status tabs. 全部 is not a status — it is the absence of
	// the filter — which is why it carries an empty value.
	KeyAdminTabAll = key("admin.tab.all", Message{ZhHant: "全部", En: "All"})

	// Who caused an order event, and who caused a stock movement. 系統 rather
	// than blank: a sale is the shop doing its work, not a missing value.
	KeyAdminActorCustomer = key("admin.actor.customer", Message{ZhHant: "顧客", En: "Customer"})
	KeyAdminActorSystem   = key("admin.actor.system", Message{ZhHant: "系統", En: "System"})

	// The 發票 choice a customer made at checkout. A 統編 invoice STILL needs a
	// carrier — the 統編 says who it is FOR and the carrier says where it is
	// held — which is why these are three answers and not two.
	KeyAdminCarrierMember = key("admin.carrier.member", Message{ZhHant: "會員載具", En: "Member carrier"})
	KeyAdminCarrierMobile = key("admin.carrier.mobile", Message{
		ZhHant: "手機條碼載具 %s",
		En:     "Mobile barcode carrier %s",
	})
	KeyAdminCarrierTaxID = key("admin.carrier.taxid", Message{
		ZhHant: "公司統編 %s",
		En:     "Company tax number %s",
	})
	// A 統一發票 cannot be edited, so a correction is a 折讓 against it or a
	// void and a reissue. The two documents are named apart for that reason.
	KeyAdminDocAllowance = key("admin.doc.allowance", Message{ZhHant: "折讓", En: "Credit note"})
	KeyAdminDocInvoice   = key("admin.doc.invoice", Message{ZhHant: "統一發票", En: "Tax invoice"})

	// Why stock moved. The ledger's closed set — a movement with an unknown
	// reason is a programming error, which is why the caller panics rather than
	// printing a code at somebody.
	KeyAdminMoveReceipt    = key("admin.move.receipt", Message{ZhHant: "進貨", En: "Goods receipt"})
	KeyAdminMoveHold       = key("admin.move.hold", Message{ZhHant: "結帳保留", En: "Checkout hold"})
	KeyAdminMoveSale       = key("admin.move.sale", Message{ZhHant: "出貨扣除", En: "Dispatched"})
	KeyAdminMoveRelease    = key("admin.move.release", Message{ZhHant: "釋放回架", En: "Hold released"})
	KeyAdminMoveReturn     = key("admin.move.return", Message{ZhHant: "退貨入庫", En: "Returned to stock"})
	KeyAdminMoveAdjustment = key("admin.move.adjustment", Message{ZhHant: "人工調整", En: "Manual correction"})
)

var (
	// /admin/health. Every figure is derived from the WORK rather than from a
	// heartbeat, and the wording keeps that: a worker looping without progress
	// passes "I am running" and fails these.
	KeyHealthSweeperClear = key("health.sweeper.clear", Message{
		ZhHant: "沒有待清理的過期 session 或未使用的圖片",
		En:     "No expired sessions or unreferenced images waiting to be swept",
	})
	KeyHealthSweeperBacklog = key("health.sweeper.backlog", Message{
		ZhHant: "%d 個過期 session、%d 張沒被引用的圖片還沒清掉",
		En:     "%d expired sessions and %d unreferenced images still to sweep",
	})
	// Exhausted its attempts, so it will NOT come back on its own — the reason
	// this page lists them rather than only counting them.
	KeyHealthOutboxStuck = key("health.outbox.stuck", Message{
		ZhHant: "%d 封重試次數用盡 —— 不會自己好",
		En:     "%d have exhausted their retries — these will not recover on their own",
	})
	KeyHealthOutboxClear   = key("health.outbox.clear", Message{ZhHant: "沒有待送的訊息", En: "Nothing waiting to send"})
	KeyHealthOutboxOverdue = key("health.outbox.overdue", Message{
		ZhHant: "%d 封待送,最久的已經逾期 %s",
		En:     "%d waiting, the oldest overdue by %s",
	})
	// Overdue is measured from available_at — when a message became DUE — and
	// not from when it was written, because the claim and the backoff both push
	// that forward. A message legitimately waiting must not read as a backlog.
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
	// Says who is out of pocket, because that is what makes it urgent.
	KeyHealthRefundsStuck = key("health.refunds.stuck", Message{
		ZhHant: "%d 筆退款還沒退成功 —— 顧客還沒拿到錢",
		En:     "%d refunds have not gone through — the customer does not have their money",
	})
	// "Never" and "just now" are separate answers. max() over an empty table is
	// NULL, and collapsing that into a zero age would make the one state meaning
	// "this worker has never run" read as the healthiest possible one.
	KeyHealthProjectionNever = key("health.projection.never", Message{
		ZhHant: "從來沒有重建過",
		En:     "Never rebuilt",
	})
	KeyHealthProjectionAge = key("health.projection.age", Message{
		ZhHant: "上次重建於 %s前",
		En:     "Last rebuilt %s ago",
	})
	KeyHealthNoReason = key("health.noreason", Message{ZhHant: "(沒有記錄原因)", En: "(no reason recorded)"})
	KeyHealthNoRef    = key("health.noref", Message{
		ZhHant: "(金流端沒有回覆編號)",
		En:     "(the provider returned no reference)",
	})

	// A refund the provider has not settled. Only these three reach the page —
	// the query's own WHERE clause bounds it — so a fourth value means that
	// query changed, and the default arm shows the raw status rather than a 500
	// on the page somebody opened to find out something is wrong.
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
