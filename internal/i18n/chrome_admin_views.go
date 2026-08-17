package i18n

var (
	KeyAdminDays    = key("admin.days", Message{ZhHant: "%d 天", En: "%d days"})
	KeyAdminSeconds = key("admin.seconds", Message{ZhHant: "%d 秒", En: "%d seconds"})
	KeyAdminMinutes = key("admin.minutes", Message{ZhHant: "%d 分鐘", En: "%d minutes"})
	KeyAdminHours   = key("admin.hours", Message{ZhHant: "%d 小時", En: "%d hours"})

	KeyAdminErasedAccount      = key("admin.erased.account", Message{ZhHant: "(已刪除帳號)", En: "(erased account)"})
	KeyAdminErasedAccountPlain = key("admin.erased.plain", Message{ZhHant: "已刪除的帳號", En: "Erased account"})
	KeyAdminErasedRecipient    = key("admin.erased.recipient", Message{ZhHant: "(已抹除)", En: "(erased)"})
	KeyAdminErasedShort        = key("admin.erased.short", Message{ZhHant: "(已刪除)", En: "(deleted)"})

	KeyAdminReviewShow = key("admin.review.show", Message{ZhHant: "恢復顯示", En: "Show again"})
	KeyAdminReviewHide = key("admin.review.hide", Message{ZhHant: "隱藏", En: "Hide"})

	KeyAdminQAnswered     = key("admin.question.answered", Message{ZhHant: "已回覆", En: "Answered"})
	KeyAdminQCustomerOnly = key("admin.question.customeronly", Message{
		ZhHant: "只有顧客回覆",
		En:     "Only customers replied",
	})
	KeyAdminQWaiting = key("admin.question.waiting", Message{ZhHant: "待回覆", En: "Awaiting an answer"})

	KeyAdminTOTPOn  = key("admin.totp.on", Message{ZhHant: "已啟用", En: "Enabled"})
	KeyAdminTOTPOff = key("admin.totp.off", Message{ZhHant: "尚未啟用", En: "Not enabled"})

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

	KeyAdminCampaignOff     = key("admin.campaign.off", Message{ZhHant: "已停用", En: "Switched off"})
	KeyAdminCampaignOutside = key("admin.campaign.outside", Message{ZhHant: "不在期間內", En: "Outside its window"})
	KeyAdminCampaignEmpty   = key("admin.campaign.empty", Message{
		ZhHant: "進行中(沒有商品)",
		En:     "Running (nothing featured)",
	})
	KeyAdminCampaignRunning = key("admin.campaign.running", Message{ZhHant: "進行中", En: "Running"})
	KeyAdminToggleOff       = key("admin.toggle.off", Message{ZhHant: "停用", En: "Switch off"})
	KeyAdminToggleOn        = key("admin.toggle.on", Message{ZhHant: "啟用", En: "Switch on"})

	KeyAdminMsgHandled = key("admin.message.handled", Message{ZhHant: "已處理", En: "Handled"})
	KeyAdminMsgToday   = key("admin.message.today", Message{ZhHant: "今天", En: "Today"})
	KeyAdminMsgOneDay  = key("admin.message.oneday", Message{ZhHant: "等了 1 天", En: "Waiting 1 day"})
	KeyAdminMsgDays    = key("admin.message.days", Message{ZhHant: "等了 %d 天", En: "Waiting %d days"})
	KeyAdminMsgReopen  = key("admin.message.reopen", Message{ZhHant: "重新開啟", En: "Reopen"})
	KeyAdminMsgHandle  = key("admin.message.handle", Message{ZhHant: "標記已處理", En: "Mark handled"})

	KeyAdminDestAddress = key("admin.dest.address", Message{ZhHant: "宅配地址", En: "Home address"})
	KeyAdminDestPickup  = key("admin.dest.pickup", Message{ZhHant: "超商門市", En: "Convenience store"})
	KeyAdminNone        = key("admin.none", Message{ZhHant: "無", En: "None"})

	KeyAdminStockOf = key("admin.stock.of", Message{
		ZhHant: "%d 件(可售 %d)",
		En:     "%d in stock (%d sellable)",
	})
)

var (
	KeyAdminCouponCap        = key("admin.coupon.cap", Message{ZhHant: "(上限 %s)", En: "(capped at %s)"})
	KeyAdminCouponMin        = key("admin.coupon.min", Message{ZhHant: "滿 %s", En: "over %s"})
	KeyAdminCouponTotalLimit = key("admin.coupon.totallimit", Message{ZhHant: "限量 %d", En: "%d in total"})
	KeyAdminCouponPerPerson  = key("admin.coupon.perperson", Message{ZhHant: "每人 %d 次", En: "%d per customer"})
	KeyAdminCouponUsed       = key("admin.coupon.used", Message{ZhHant: "%d 次", En: "%d used"})
	KeyAdminCouponGiven      = key("admin.coupon.given", Message{ZhHant: " · 已折抵 %s", En: " · %s discounted"})
	KeyAdminCouponOff        = key("admin.coupon.off", Message{ZhHant: "已停用", En: "Switched off"})
	KeyAdminCouponOutside    = key("admin.coupon.outside", Message{ZhHant: "不在期間內", En: "Outside its window"})
	KeyAdminCouponLive       = key("admin.coupon.live", Message{ZhHant: "使用中", En: "Live"})

	KeyAdminTabAll = key("admin.tab.all", Message{ZhHant: "全部", En: "All"})

	KeyAdminActorCustomer = key("admin.actor.customer", Message{ZhHant: "顧客", En: "Customer"})
	KeyAdminActorSystem   = key("admin.actor.system", Message{ZhHant: "系統", En: "System"})

	KeyAdminCarrierMember = key("admin.carrier.member", Message{ZhHant: "會員載具", En: "Member carrier"})
	KeyAdminCarrierMobile = key("admin.carrier.mobile", Message{
		ZhHant: "手機條碼載具 %s",
		En:     "Mobile barcode carrier %s",
	})
	KeyAdminCarrierTaxID = key("admin.carrier.taxid", Message{
		ZhHant: "公司統編 %s",
		En:     "Company tax number %s",
	})
	KeyAdminDocAllowance = key("admin.doc.allowance", Message{ZhHant: "折讓", En: "Credit note"})
	KeyAdminDocInvoice   = key("admin.doc.invoice", Message{ZhHant: "統一發票", En: "Tax invoice"})

	KeyAdminMoveReceipt    = key("admin.move.receipt", Message{ZhHant: "進貨", En: "Goods receipt"})
	KeyAdminMoveHold       = key("admin.move.hold", Message{ZhHant: "結帳保留", En: "Checkout hold"})
	KeyAdminMoveSale       = key("admin.move.sale", Message{ZhHant: "出貨扣除", En: "Dispatched"})
	KeyAdminMoveRelease    = key("admin.move.release", Message{ZhHant: "釋放回架", En: "Hold released"})
	KeyAdminMoveReturn     = key("admin.move.return", Message{ZhHant: "退貨入庫", En: "Returned to stock"})
	KeyAdminMoveAdjustment = key("admin.move.adjustment", Message{ZhHant: "人工調整", En: "Manual correction"})
)

var (
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
	KeyHealthOutboxClear   = key("health.outbox.clear", Message{ZhHant: "沒有待送的訊息", En: "Nothing waiting to send"})
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
	KeyHealthNoRef    = key("health.noref", Message{
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
