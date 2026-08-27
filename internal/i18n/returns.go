package i18n

var (
	KeyReturnTitle = key("returns.title", Message{ZhHant: "退貨申請", En: "Return request"})

	KeyReturnSub = key("returns.sub", Message{
		ZhHant: "可退貨的數量是「已出貨」的數量,扣掉先前已經申請過的部分。",
		En:     "What can be returned is what SHIPPED, less anything already requested.",
	})

	KeyReturnHistory = key("returns.history", Message{ZhHant: "申請紀錄", En: "Previous requests"})

	KeyReturnSentAt = key("returns.sentat", Message{ZhHant: "%s 送出", En: "Sent %s"})

	KeyReturnResolution = key("returns.resolution", Message{
		ZhHant: "處理說明:%s",
		En:     "Outcome: %s",
	})

	KeyReturnInFlight = key("returns.inflight", Message{
		ZhHant: "這筆訂單已經有一筆還在處理中的退貨申請,處理完成後才能再次申請。",
		En: "There is already a request being handled for this order. You can send another " +
			"once it is decided.",
	})

	KeyReturnNothing = key("returns.nothing", Message{
		ZhHant: "這筆訂單目前沒有可以退貨的商品。尚未出貨的訂單請改用取消。",
		En: "Nothing on this order can be returned. If it has not shipped, cancel it " +
			"instead.",
	})

	KeyReturnChoose = key("returns.choose", Message{
		ZhHant: "選擇要退回的商品",
		En:     "Choose what to send back",
	})

	KeyReturnQuantity = key("returns.quantity", Message{
		ZhHant: "退貨數量(最多 %s)",
		En:     "How many (up to %s)",
	})

	KeyFieldReturnReason = key("field.return.reason", Message{ZhHant: "退貨原因", En: "Reason"})

	KeyReturnSubmit = key("returns.submit", Message{ZhHant: "送出申請", En: "Send request"})

	KeyBackToOrder = key("returns.backtoorder", Message{ZhHant: "回到訂單", En: "Back to the order"})

	KeyReturnTooMany = key("returns.toomany", Message{
		ZhHant: "數量超出可退貨的範圍。",
		En:     "That is more than can be returned.",
	})

	KeyReturnAlreadyOpen = key("returns.alreadyopen", Message{
		ZhHant: "這筆訂單已經有一筆還在處理中的退貨申請。",
		En:     "There is already an open request for this order.",
	})

	KeyReturnNothingShort = key("returns.nothing.short", Message{
		ZhHant: "這筆訂單目前沒有可以退貨的商品。",
		En:     "Nothing on this order can be returned.",
	})

	KeyReturnNeedsReason = key("returns.needsreason", Message{
		ZhHant: "請填寫退貨原因,並至少選擇一件商品。",
		En:     "Give a reason and choose at least one item.",
	})

	KeyReturnMeta = key("returns.meta", Message{ZhHant: "退貨申請 %s", En: "Return request — %s"})

	KeyReturnStateOpen = key("returns.state.open", Message{ZhHant: "已送出,等待處理", En: "Sent, awaiting a decision"})

	KeyReturnStateApproved = key("returns.state.approved", Message{ZhHant: "已同意退貨", En: "Approved"})

	KeyReturnStateRefused = key("returns.state.refused", Message{ZhHant: "未同意退貨", En: "Declined"})

	KeyReturnStateDone = key("returns.state.done", Message{ZhHant: "退貨完成", En: "Completed"})
)
