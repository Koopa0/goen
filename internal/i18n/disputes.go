package i18n

var (
	KeyAdminPageDisputes = key("admin.page.disputes", Message{
		ZhHant: "付款爭議",
		En:     "Payment disputes",
	})

	KeyAdminQueueDisputes = key("admin.queue.disputes", Message{
		ZhHant: "付款爭議",
		En:     "Payment disputes",
	})

	KeyAdminDisLead = key("admin.dis.lead", Message{
		ZhHant: "爭議與退款不同：這裡追蹤發卡行爭議、回覆期限與資金移動。證據可在 Stripe 後台提交。",
		En: "A dispute is not a refund: this queue tracks issuer disputes, response deadlines and " +
			"funds movement. Evidence is submitted in the Stripe Dashboard.",
	})

	KeyAdminDisEmpty = key("admin.dis.empty", Message{
		ZhHant: "目前沒有需要處理的付款爭議。",
		En:     "There are no payment disputes that need attention.",
	})

	KeyAdminDisOrder = key("admin.dis.order", Message{ZhHant: "訂單 %s", En: "Order %s"})

	KeyAdminDisUnattributed = key("admin.dis.unattributed", Message{
		ZhHant: "無法對應到本地付款",
		En:     "No local payment attributed",
	})

	KeyAdminDisAmount = key("admin.dis.amount", Message{ZhHant: "爭議金額 %s", En: "Disputed %s"})

	KeyAdminDisDeadline = key("admin.dis.deadline", Message{
		ZhHant: "回覆期限 %s",
		En:     "Respond by %s",
	})

	KeyAdminDisDeadlineUnknown = key("admin.dis.deadline_unknown", Message{
		ZhHant: "回覆期限未知",
		En:     "Response deadline unknown",
	})

	KeyAdminDisOverdue = key("admin.dis.overdue", Message{
		ZhHant: "已逾回覆期限",
		En:     "Past response deadline",
	})

	KeyAdminDisStripe = key("admin.dis.stripe", Message{
		ZhHant: "在 Stripe 查看爭議",
		En:     "View dispute in Stripe",
	})

	KeyAdminDisReviewHint = key("admin.dis.review_hint", Message{
		ZhHant: "記錄誰看過這筆爭議以及準備採取的處置；不會自動判定客戶詐欺。",
		En: "Record who reviewed this dispute and the intended response. Nothing here labels the " +
			"customer fraudulent.",
	})

	KeyAdminDisDisposition = key("admin.dis.disposition", Message{ZhHant: "處置", En: "Disposition"})

	KeyAdminDisDispositionMonitoring = key("admin.dis.disposition.monitoring", Message{
		ZhHant: "持續關注",
		En:     "Monitoring",
	})

	KeyAdminDisDispositionAccepted = key("admin.dis.disposition.accepted", Message{
		ZhHant: "接受結果",
		En:     "Accept outcome",
	})

	KeyAdminDisDispositionChallenging = key("admin.dis.disposition.challenging", Message{
		ZhHant: "準備申辩",
		En:     "Preparing challenge",
	})

	KeyAdminDisDispositionClosed = key("admin.dis.disposition.closed", Message{
		ZhHant: "結案",
		En:     "Closed",
	})

	KeyAdminDisReviewButton = key("admin.dis.review_button", Message{ZhHant: "記錄", En: "Record"})

	KeyAdminDisStatus = key("admin.dis.status", Message{ZhHant: "狀態 %s", En: "Status %s"})
)
