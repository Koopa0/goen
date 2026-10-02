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

	KeyAdminQueueStatLowStock = key("admin.queue.stat.lowstock", Message{
		ZhHant: "低庫存品項",
		En:     "Low-stock items",
	})

	KeyAdminQueueStatActive = key("admin.queue.stat.active", Message{
		ZhHant: "上架商品",
		En:     "Published products",
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

	// KeyAdminQueueStatReturnsBy is the line under the returns figure: the first
	// last day of the seven-day right of rescission among the open requests.
	KeyAdminQueueStatReturnsBy = key("admin.queue.stat.returnsby", Message{
		ZhHant: "最近的七日期限:%s",
		En:     "Earliest seven-day deadline: %s",
	})

	KeyAdminQueueRestockHead = key("admin.queue.restock", Message{
		ZhHant: "需要補貨",
		En:     "Needs restocking",
	})

	KeyAdminQueueSellableHint = key("admin.queue.sellable", Message{
		ZhHant: "「可售」是庫存減去安全庫存,也就是資料庫實際允許賣出的數量。",
		En: "Sellable is stock minus safety stock — the number the database will actually " +
			"let the shop sell.",
	})

	KeyAdminQueueAllLowStock = key("admin.queue.alllow", Message{
		ZhHant: "查看全部低庫存",
		En:     "See every low-stock item",
	})
)
