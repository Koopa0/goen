package i18n

var (
	KeyAdminPageDashboard = key("admin.page.dashboard", Message{ZhHant: "後台", En: "Back office"})

	KeyAdminQueueDashboardTitle = key("admin.queue.dashboard.title", Message{
		ZhHant: "後台總覽",
		En:     "Back-office overview",
	})

	KeyAdminQueueStatPending = key("admin.queue.stat.pending", Message{
		ZhHant: "待付款訂單",
		En:     "Orders awaiting payment",
	})

	KeyAdminQueueStatSoldOut = key("admin.queue.stat.soldout", Message{
		ZhHant: "已售完品項",
		En:     "Sold-out items",
	})

	KeyAdminQueueStatMessages = key("admin.queue.stat.messages", Message{
		ZhHant: "待回覆訊息",
		En:     "Messages awaiting a reply",
	})

	KeyAdminQueueStatReturns = key("admin.queue.stat.returns", Message{
		ZhHant: "待處理退貨申請",
		En:     "Return requests to decide",
	})

	KeyAdminQueueStatQuestions = key("admin.queue.stat.questions", Message{
		ZhHant: "待回覆提問",
		En:     "Questions awaiting an answer",
	})

	KeyAdminQueueTasksHeading = key("admin.queue.tasks.heading", Message{
		ZhHant: "等你處理",
		En:     "Waiting for you",
	})

	KeyAdminQueueHealthUnavailable = key("admin.queue.health.unavailable", Message{
		ZhHant: "部分營運狀態暫時無法取得，目前無法確認是否有待處理的金流或發票問題。",
		En: "Some operating status is unavailable right now, so we cannot confirm whether " +
			"any payment or invoice problem is waiting.",
	})

	KeyAdminQueueTaskUninspected = key("admin.queue.task.uninspected", Message{
		ZhHant: "待驗收的退貨",
		En:     "Returns awaiting inspection",
	})

	KeyAdminQueueTaskHolds = key("admin.queue.task.holds", Message{
		ZhHant: "超過期限仍保留庫存的未付款訂單",
		En:     "Unpaid orders still holding stock past their deadline",
	})

	KeyAdminQueueTaskPayments = key("admin.queue.task.payments", Message{
		ZhHant: "付款待核對",
		En:     "Payments to check",
	})

	KeyAdminQueueTaskClaims = key("admin.queue.task.claims", Message{
		ZhHant: "發票待確認",
		En:     "Invoices to confirm",
	})

	KeyAdminQueueTaskUninvoiced = key("admin.queue.task.uninvoiced", Message{
		ZhHant: "已收款、還沒開發票",
		En:     "Paid, not yet invoiced",
	})

	KeyAdminQueueTaskUnderADay = key("admin.queue.task.underaday", Message{
		ZhHant: "不到 1 天",
		En:     "Under a day",
	})

	KeyAdminQueueTaskOldestDays = countKey("admin.queue.task.oldestdays",
		"最久 %d 天",
		"Oldest %d day",
		"Oldest %d days")

	KeyAdminQueueRunwayRest = key("admin.queue.runwayrest", Message{
		ZhHant: "在報表看其餘品項",
		En:     "See the rest in the report",
	})

	KeyAdminQueueRunwayNone = countKey("admin.queue.runwaynone",
		"預計 %d 天內沒有品項會賣完。",
		"Nothing is expected to sell out within %d day.",
		"Nothing is expected to sell out within %d days.")

	KeyAdminQueueRunwayUnknown = key("admin.queue.runwayunknown", Message{
		ZhHant: "銷量還太少，估不出還能賣幾天。",
		En:     "Too few sales yet to estimate how long stock will last.",
	})

	KeyAdminQueueRunwayNoStock = countKey("admin.queue.runwaynostock",
		"近 %d 天賣出的品項都已售完。",
		"Everything that sold in the last %d day is sold out.",
		"Everything that sold in the last %d days is sold out.")

	KeyAdminQueueRunwayAll = key("admin.queue.runwayall", Message{
		ZhHant: "在報表看全部",
		En:     "See all in the report",
	})

	KeyAdminQueueRunwayLegend = key("admin.queue.runwaylegend", Message{
		ZhHant: "依近 %[1]d 天銷量估算。淡色是可能撐到的天數，短豎線是 %[2]d 天。",
		En:     "Estimated from the last %[1]d days of sales. The pale stretch is how long it may last; the short line marks %[2]d days.",
	})

	KeyAdminQueueWeekUnavailable = key("admin.queue.week.unavailable", Message{
		ZhHant: "近 7 天的數字暫時無法取得。",
		En:     "The last 7 days are unavailable right now.",
	})

	KeyAdminQueueLatestPaid = key("admin.queue.latest.paid", Message{
		ZhHant: "最近一筆已付款訂單",
		En:     "Latest paid order",
	})

	KeyAdminQueueLatestNone = key("admin.queue.latest.none", Message{
		ZhHant: "還沒有已付款的訂單",
		En:     "No paid order yet",
	})

	KeyAdminQueueLatestUnavailable = key("admin.queue.latest.unavailable", Message{
		ZhHant: "暫時無法取得",
		En:     "Unavailable right now",
	})

	KeyAdminQueueAgoNow = key("admin.queue.ago.now", Message{ZhHant: "剛剛", En: "Just now"})

	KeyAdminQueueAgoMinutes = countKey("admin.queue.ago.minutes", "%d 分鐘前", "%d minute ago", "%d minutes ago")
	KeyAdminQueueAgoHours   = countKey("admin.queue.ago.hours", "%d 小時前", "%d hour ago", "%d hours ago")
	KeyAdminQueueAgoDays    = countKey("admin.queue.ago.days", "%d 天前", "%d day ago", "%d days ago")
)
