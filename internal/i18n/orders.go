package i18n

var (
	KeyOrderPlaced = key("order.placed", Message{ZhHant: "訂單成立", En: "Order placed"})

	KeyOrderPlacedAt = key("order.placedat", Message{
		ZhHant: "%s 送出",
		En:     "Placed %s",
	})

	KeyOrderEmailNotice = key("order.email", Message{
		ZhHant: "確認信將寄至 %s",
		En:     "A confirmation is on its way to %s",
	})

	KeyOrderCancelled = key("order.cancelled.notice", Message{
		ZhHant: "訂單已取消,保留的商品已經放回。",
		En:     "This order is cancelled. The reserved stock has gone back on the shelf.",
	})

	KeyOrderUnpaid = key("order.unpaid", Message{
		ZhHant: "這筆訂單尚未付款,商品已為您保留。",
		En:     "This order is not paid for yet. The stock is being held for you.",
	})

	KeyOrderReorder = key("order.reorder", Message{ZhHant: "再買一次", En: "Order again"})

	KeyOrderReorderNote = key("order.reorder.note", Message{
		ZhHant: "用今天的價格,把還買得到的商品放回購物車。",
		En:     "Puts whatever is still available back in your cart, at today's prices.",
	})

	KeyOrderCancel = key("order.cancel", Message{ZhHant: "取消訂單", En: "Cancel order"})

	KeyOrderCancelNote = key("order.cancel.note", Message{
		ZhHant: "未付款的訂單可以自行取消。",
		En:     "You can cancel an order yourself until it is paid for.",
	})

	KeyOrderTracking = key("order.tracking", Message{ZhHant: "配送資訊", En: "Delivery"})

	KeyOrderTrackingNo = key("order.tracking.no", Message{
		ZhHant: "查詢編號 %s",
		En:     "Tracking number %s",
	})

	KeyOrderDeliveredAt = key("order.deliveredat", Message{ZhHant: "%s 已送達", En: "Delivered %s"})

	KeyOrderShippedAt = key("order.shippedat", Message{ZhHant: "%s 出貨", En: "Dispatched %s"})

	KeyOrderHistory = key("order.history", Message{ZhHant: "訂單紀錄", En: "Order history"})

	KeyOrderDeliveryTo = key("order.deliveryto", Message{ZhHant: "配送到", En: "Delivering to"})

	KeyOrderShippingFee = key("order.shippingfee", Message{ZhHant: "運費(%s)", En: "Delivery (%s)"})

	KeyOrderGrandTotal = key("order.grandtotal", Message{ZhHant: "總計", En: "Total"})

	KeyOrderReturn = key("order.return", Message{ZhHant: "申請退貨", En: "Request a return"})

	KeyOrderMeta = key("order.meta", Message{ZhHant: "訂單 %s", En: "Order %s"})

	KeyStatusPlaced = key("order.status.placed", Message{ZhHant: "訂單成立", En: "Placed"})

	KeyStatusPaid = key("order.status.paid", Message{ZhHant: "付款完成", En: "Paid"})

	KeyStatusPicking = key("order.status.picking", Message{ZhHant: "備貨中", En: "Being packed"})

	KeyStatusShipped = key("order.status.shipped", Message{ZhHant: "已出貨", En: "Dispatched"})

	KeyStatusInTransit = key("order.status.transit", Message{ZhHant: "運送中", En: "In transit"})

	KeyStatusDelivered = key("order.status.delivered", Message{ZhHant: "已送達", En: "Delivered"})

	KeyStatusCompleted = key("order.status.completed", Message{ZhHant: "訂單完成", En: "Complete"})

	KeyStatusCancelled = key("order.status.cancelled", Message{ZhHant: "訂單取消", En: "Cancelled"})

	KeyStatusRefunded = key("order.status.refunded", Message{ZhHant: "已退款", En: "Refunded"})

	KeyOrderCreditApplied = key("order.credit", Message{
		ZhHant: "商店額度折抵",
		En:     "Paid with store credit",
	})

	KeyOrderNotFound = key("order.notfound", Message{ZhHant: "找不到這筆訂單", En: "Order not found"})

	KeyOrderNotYours = key("order.notyours", Message{
		ZhHant: "訂單編號可能不正確,或這筆訂單不屬於這個瀏覽器。登入後可以在會員中心查看。",
		En: "The number may be wrong, or this order was not placed from this browser. " +
			"Sign in to see it in your account.",
	})

	KeyOrderGone = key("order.gone", Message{
		ZhHant: "訂單編號可能不正確,或這筆訂單已經不存在。",
		En:     "The number may be wrong, or the order no longer exists.",
	})

	KeyOrderNotYoursShort = key("order.notyours.short", Message{
		ZhHant: "訂單編號可能不正確,或這筆訂單不屬於這個瀏覽器。",
		En:     "The number may be wrong, or this order was not placed from this browser.",
	})

	KeyCancelRefusedTitle = key("order.cancel.refused", Message{
		ZhHant: "這筆訂單無法取消",
		En:     "This order cannot be cancelled",
	})

	KeyCancelRefusedBody = key("order.cancel.refused.body", Message{
		ZhHant: "已經付款或已經開始出貨的訂單不能自行取消。需要更動請聯絡我們。",
		En: "An order that has been paid for or started shipping cannot be cancelled here. " +
			"Get in touch and we will sort it out.",
	})

	KeyFindOrderTitle = key("order.find.title", Message{
		ZhHant: "查詢訂單",
		En:     "Find your order",
	})

	KeyFindOrderSub = key("order.find.sub", Message{
		ZhHant: "用訂單編號和下單時填的 Email 查詢。編號在確認信裡。",
		En: "Use the order number and the email address you gave at checkout. The number " +
			"is in your confirmation email.",
	})

	KeyFieldOrderNumber = key("field.ordernumber", Message{ZhHant: "訂單編號", En: "Order number"})

	KeyFindOrderSubmit = key("order.find.submit", Message{ZhHant: "查詢", En: "Find it"})

	KeyFindOrderSignIn = key("order.find.signin", Message{
		ZhHant: "有帳號的話,",
		En:     "If you have an account, ",
	})

	KeyFindOrderRefused = key("order.find.refused", Message{
		ZhHant: "查不到符合的訂單。請確認訂單編號和 Email 都和確認信上的一樣。",
		En: "No order matches those details. Check that the number and the address are " +
			"both exactly as they appear in your confirmation email.",
	})
)
