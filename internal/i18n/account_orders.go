package i18n

var (
	KeyOrderHistory2 = key("account.orders", Message{ZhHant: "訂單紀錄", En: "Your orders"})

	KeyOrderLineCount = countKey("account.orders.lines", "%s 項", "%s item", "%s items")

	// KeyOrderRowRescissionEnds labels the last day to cancel in a row of the order history, where nothing around
	// it says what the day is the last of.
	KeyOrderRowRescissionEnds = key("account.orders.rescission_ends", Message{ZhHant: "猶豫期到", En: "Last day to cancel"})

	KeyNoOrdersYet = key("account.orders.none", Message{ZhHant: "還沒有訂單。", En: "No orders yet."})

	KeyNoOrdersLink = key("account.orders.none.link", Message{
		ZhHant: "去看看商品",
		En:     "Have a look at what there is",
	})

	KeyStatusAwaitingPayment = key("account.status.pending", Message{ZhHant: "待付款", En: "Awaiting payment"})

	KeyStatusDone = key("account.status.completed", Message{ZhHant: "已完成", En: "Completed"})

	KeyStatusCalledOff = key("account.status.cancelled", Message{ZhHant: "已取消", En: "Cancelled"})
)
