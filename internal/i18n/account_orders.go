package i18n

var (
	KeyOrderHistory2 = key("account.orders", Message{ZhHant: "訂單紀錄", En: "Your orders"})

	KeyOrderLineCount = key("account.orders.lines", Message{ZhHant: "%s 項", En: "%s items"})

	KeyNoOrdersYet = key("account.orders.none", Message{ZhHant: "還沒有訂單。", En: "No orders yet."})

	KeyNoOrdersLink = key("account.orders.none.link", Message{
		ZhHant: "去看看商品",
		En:     "Have a look at what there is",
	})

	KeyStatusAwaitingPayment = key("account.status.pending", Message{ZhHant: "待付款", En: "Awaiting payment"})

	KeyStatusDone = key("account.status.completed", Message{ZhHant: "已完成", En: "Completed"})

	KeyStatusCalledOff = key("account.status.cancelled", Message{ZhHant: "已取消", En: "Cancelled"})
)
