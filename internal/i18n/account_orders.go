package i18n

var (
	KeyOrderHistory2 = key("account.orders", Message{ZhHant: "訂單紀錄", En: "Your orders"})

	KeyOrderLineCount = key("account.orders.lines", Message{ZhHant: "%s 項", En: "%s items"})

	KeyNoOrdersYet = key("account.orders.none", Message{ZhHant: "還沒有訂單。", En: "No orders yet."})

	KeyNoOrdersLink = key("account.orders.none.link", Message{
		ZhHant: "去看看商品",
		En:     "Have a look at what there is",
	})

	KeyPayLaterNotice = key("account.order.paylater", Message{
		ZhHant: "這筆訂單尚未付款,商品已為您保留。",
		En:     "This order is not paid for yet. The stock is being held for you.",
	})

	KeyOrderNotYours2 = key("account.order.notyours", Message{
		ZhHant: "這個訂單編號不在你的帳號下。",
		En:     "That order number is not on your account.",
	})

	KeyStatusAwaitingPayment = key("account.status.pending", Message{ZhHant: "待付款", En: "Awaiting payment"})

	KeyStatusDone = key("account.status.completed", Message{ZhHant: "已完成", En: "Completed"})

	KeyStatusCalledOff = key("account.status.cancelled", Message{ZhHant: "已取消", En: "Cancelled"})
)
