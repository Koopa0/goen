package i18n

var (
	KeyOrderPlaced = key("order.placed", Message{ZhHant: "訂單已送出", En: "Order placed"})

	KeyOrderEmailNotice = key("order.email", Message{
		ZhHant: "確認信將寄至 %s",
		En:     "A confirmation is on its way to %s",
	})

	KeyOrderCancelled = key("order.cancelled.notice", Message{
		ZhHant: "訂單已取消，保留的商品已經放回。",
		En:     "This order is cancelled. The reserved stock has gone back on the shelf.",
	})

	KeyOrderUnpaid = key("order.unpaid", Message{
		ZhHant: "這筆訂單尚未付款，商品已為你保留。",
		En:     "This order is not paid for yet. The stock is being held for you.",
	})

	KeyOrderReorder = key("order.reorder", Message{ZhHant: "再買一次", En: "Order again"})

	KeyOrderReorderNote = key("order.reorder.note", Message{
		ZhHant: "用今天的價格，把還買得到的商品放回購物車。",
		En:     "Puts whatever is still available back in your cart, at today's prices.",
	})

	KeyOrderCancel = key("order.cancel", Message{ZhHant: "取消訂單", En: "Cancel order"})

	KeyOrderCancelNote = key("order.cancel.note", Message{
		ZhHant: "未付款的訂單可以自行取消。",
		En:     "You can cancel an order yourself until it is paid for.",
	})

	KeyOrderCancelVoidsInvoice = key("order.cancel.voidsinvoice", Message{
		ZhHant: "這筆訂單以購物金付清。取消後購物金會退回，統一發票也會作廢。",
		En:     "Paid with store credit: cancelling returns the credit and voids this order's invoice.",
	})

	KeyOrderTrackingNo = key("order.tracking.no", Message{
		ZhHant: "查詢編號 %s",
		En:     "Tracking number %s",
	})

	KeyOrderDeliveredAt = key("order.deliveredat", Message{ZhHant: "%s 已送達", En: "Delivered %s"})

	KeyOrderInvoiceIssued = key("order.invoice.issued", Message{ZhHant: "%s 開立", En: "Issued %s"})

	// The statutory right, stated plainly; the day is the database's.
	KeyOrderRescissionEnds = key("order.rescissionends", Message{
		ZhHant: "依消費者保護法，你可自收到商品的次日起七日內退貨，最後一日為 %s。",
		En:     "Under Taiwan's Consumer Protection Act you may return what you bought within seven days, counted from the day after you receive the goods; the last day is %s.",
	})

	KeyOrderShippedAt = key("order.shippedat", Message{ZhHant: "%s 出貨", En: "Dispatched %s"})

	KeyOrderFactPlaced = key("order.fact.placed", Message{ZhHant: "送出", En: "Placed"})

	KeyOrderFactDelivered = key("order.fact.delivered", Message{ZhHant: "送達", En: "Delivered"})

	KeyOrderFactCollected = key("order.fact.collected", Message{ZhHant: "取貨", En: "Collected"})

	KeyOrderFactNote = key("order.fact.note", Message{ZhHant: "%s，%s", En: "%s, %s"})

	KeyOrderFactCancelled = key("order.fact.cancelled", Message{ZhHant: "取消", En: "Cancelled"})

	KeyOrderFactRefund = key("order.fact.refund", Message{ZhHant: "退款", En: "Refund"})

	KeyOrderFactReturned = key("order.fact.returned", Message{ZhHant: "退貨完成", En: "Return completed"})

	KeyOrderReturnedTitle = key("order.returned.title", Message{ZhHant: "退貨", En: "Returns"})

	KeyOrderReturnedAll = key("order.returned.all", Message{
		ZhHant: "這筆訂單的商品已全部退回並退款。",
		En:     "Everything in this order has been returned and refunded.",
	})

	KeyOrderItems = key("order.items", Message{ZhHant: "商品", En: "Items"})

	KeyOrderNotShipped = key("order.notshipped", Message{ZhHant: "尚未出貨", En: "Not shipped yet"})

	KeyOrderParcelOf = key("order.parcel.of", Message{ZhHant: "包裹 %d／%d", En: "Parcel %d of %d"})

	KeyOrderParcelsNote = key("order.parcels.note", Message{
		ZhHant: "分成 %d 個包裹寄出，猶豫期每個包裹分開算。",
		En:     "Sent in %d parcels; the right to cancel is counted for each parcel on its own.",
	})

	KeyOrderRescissionTitle = key("order.rescission.title", Message{
		ZhHant: "猶豫期與退貨",
		En:     "Right to cancel and returns",
	})

	KeyOrderLastDay = key("order.rescission.lastday", Message{ZhHant: "最後一天", En: "Last day to cancel"})

	KeyOrderDaysLeft = key("order.rescission.daysleft", Message{ZhHant: "剩餘", En: "Days left"})

	KeyOrderUnusedUntil = key("order.rescission.unuseduntil", Message{
		ZhHant: "未使用退貨至",
		En:     "Return unused goods by",
	})

	KeyOrderRescissionAwaits = countKey("order.rescission.awaits",
		"猶豫期 %d 天，收到次日起算。",
		"The right to cancel is %d day, counted from the day after you receive the goods.",
		"The right to cancel is %d days, counted from the day after you receive the goods.")

	KeyOrderRescissionAwaitsPickup = countKey("order.rescission.awaits.pickup",
		"猶豫期 %d 天，取貨次日起算。",
		"The right to cancel is %d day, counted from the day after you collect the goods.",
		"The right to cancel is %d days, counted from the day after you collect the goods.")

	KeyOrderPickupCounted = key("order.rescission.pickup", Message{
		ZhHant: "超商取貨：從取貨的次日起算。",
		En:     "Store pickup: counted from the day after you collect.",
	})

	KeyOrderReceivedOn = key("order.received.on", Message{ZhHant: "%s收到", En: "Received %s"})

	KeyOrderCollectedOn = key("order.collected.on", Message{ZhHant: "%s取貨", En: "Collected %s"})

	// The arguments of every return-window sentence are the receipt, the last day of the right to cancel
	// and the last day goen takes unused goods back; the running one adds today, its number and the days left.
	KeyPeriodReturnStarts = key("period.return.starts", Message{
		ZhHant: "%[1]s；猶豫期從明天起算，到 %[2]s；未使用的商品可退到 %[3]s。",
		En:     "%[1]s; the right to cancel runs from tomorrow, to %[2]s; unused goods can be returned until %[3]s.",
	})

	KeyPeriodReturnRunning = countKey("period.return.running",
		"%[1]s；猶豫期到 %[2]s，今天 %[4]s是第 %[5]d 天，還有 %[6]d 天；未使用的商品可退到 %[3]s。",
		"%[1]s; the right to cancel runs to %[2]s, today, %[4]s, is day %[5]d, with %[6]d day left; unused goods can be returned until %[3]s.",
		"%[1]s; the right to cancel runs to %[2]s, today, %[4]s, is day %[5]d, with %[6]d days left; unused goods can be returned until %[3]s.")

	KeyPeriodReturnLastDay = key("period.return.lastday", Message{
		ZhHant: "%[1]s；猶豫期到 %[2]s，今天是最後一天；未使用的商品可退到 %[3]s。",
		En:     "%[1]s; the right to cancel ends today, %[2]s; unused goods can be returned until %[3]s.",
	})

	KeyPeriodReturnGoodwill = key("period.return.goodwill", Message{
		ZhHant: "%[1]s；猶豫期已於 %[2]s結束；未使用的商品仍可退到 %[3]s。",
		En:     "%[1]s; the right to cancel ended on %[2]s; unused goods can still be returned until %[3]s.",
	})

	KeyPeriodReturnOver = key("period.return.over", Message{
		ZhHant: "%[1]s；猶豫期已於 %[2]s結束，未使用退貨也已於 %[3]s截止。",
		En:     "%[1]s; the right to cancel ended on %[2]s, and unused returns closed on %[3]s.",
	})

	KeyOrderWarranty = key("order.warranty", Message{ZhHant: "保固", En: "Warranty"})

	KeyOrderWarrantyUntil = key("order.warranty.until", Message{ZhHant: "保固至", En: "Covered until"})

	KeyOrderWarrantyFromDelivery = key("order.warranty.fromdelivery", Message{ZhHant: "送達日起算", En: "Counted from delivery"})

	KeyOrderWarrantyFromCollection = key("order.warranty.fromcollection", Message{ZhHant: "取貨日起算", En: "Counted from collection"})

	KeyOrderWarrantyAfterDelivery = key("order.warranty.afterdelivery", Message{
		ZhHant: "送達後才能登錄",
		En:     "you can register it once it is delivered",
	})

	KeyOrderWarrantyAfterCollection = key("order.warranty.aftercollection", Message{
		ZhHant: "取貨後才能登錄",
		En:     "you can register it once you have collected it",
	})

	KeyOrderWarrantyNeedsAccount = key("order.warranty.needsaccount", Message{
		ZhHant: "登錄保固需要帳號",
		En:     "registering it needs an account",
	})

	KeyOrderWarrantyNote = key("order.warranty.note", Message{ZhHant: "%s；%s", En: "%s; %s"})

	KeyOrderWarrantyRegister = key("order.warranty.register", Message{ZhHant: "登錄保固", En: "Register the warranty"})

	KeyEventPlaced = key("order.event.placed", Message{ZhHant: "送出訂單", En: "Order placed"})

	KeyEventPicking = key("order.event.picking", Message{ZhHant: "開始備貨", En: "Packing started"})

	KeyEventShipped = key("order.event.shipped", Message{ZhHant: "出貨", En: "Dispatched"})

	KeyEventDelivered = key("order.event.delivered", Message{ZhHant: "送達", En: "Delivered"})

	KeyEventCancelled = key("order.event.cancelled", Message{ZhHant: "取消", En: "Cancelled"})

	KeyOrderHistory = key("order.history", Message{ZhHant: "訂單紀錄", En: "Order history"})

	KeyOrderDeliveryTo = key("order.deliveryto", Message{ZhHant: "配送到", En: "Delivering to"})

	KeyOrderShippingFee = key("order.shippingfee", Message{ZhHant: "運費（%s）", En: "Delivery (%s)"})

	KeyOrderGrandTotal = key("order.grandtotal", Message{ZhHant: "總計", En: "Total"})

	KeyOrderReturn = key("order.return", Message{ZhHant: "申請退貨", En: "Request a return"})

	KeyOrderMeta = key("order.meta", Message{ZhHant: "訂單 %s", En: "Order %s"})

	KeyStatusPlaced = key("order.status.placed", Message{ZhHant: "訂單送出", En: "Placed"})

	KeyStatusPaid = key("order.status.paid", Message{ZhHant: "付款完成", En: "Paid"})

	KeyStatusPicking = key("order.status.picking", Message{ZhHant: "備貨中", En: "Being packed"})

	KeyStatusShipped = key("order.status.shipped", Message{ZhHant: "已出貨", En: "Dispatched"})

	KeyStatusInTransit = key("order.status.transit", Message{ZhHant: "運送中", En: "In transit"})

	KeyStatusDelivered = key("order.status.delivered", Message{ZhHant: "已送達", En: "Delivered"})

	KeyStatusCompleted = key("order.status.completed", Message{ZhHant: "訂單完成", En: "Complete"})

	KeyStatusCancelled = key("order.status.cancelled", Message{ZhHant: "訂單取消", En: "Cancelled"})

	KeyStatusRefunded = key("order.status.refunded", Message{ZhHant: "已退款", En: "Refunded"})

	KeyOrderCreditApplied = key("order.credit", Message{
		ZhHant: "購物金折抵",
		En:     "Paid with store credit",
	})

	KeyOrderPayment = key("order.payment", Message{ZhHant: "付款狀態", En: "Payment"})

	// What is left to pay once store credit has taken its share.
	KeyOrderAmountDue = key("order.due", Message{ZhHant: "應付", En: "Amount due"})

	KeyOrderNotFound = key("order.notfound", Message{ZhHant: "找不到這筆訂單", En: "Order not found"})

	KeyOrderNotYours = key("order.notyours", Message{
		ZhHant: "訂單編號可能不正確，或這筆訂單不屬於這個瀏覽器。登入後可以在會員中心查看。",
		En: "The number may be wrong, or this order was not placed from this browser. " +
			"Sign in to see it in your account.",
	})

	KeyOrderGone = key("order.gone", Message{
		ZhHant: "訂單編號可能不正確，或這筆訂單已經不存在。",
		En:     "The number may be wrong, or the order no longer exists.",
	})

	KeyOrderNotYoursShort = key("order.notyours.short", Message{
		ZhHant: "訂單編號可能不正確，或這筆訂單不屬於這個瀏覽器。",
		En:     "The number may be wrong, or this order was not placed from this browser.",
	})

	KeyOrderOpening = key("order.opening", Message{ZhHant: "正在開啟你的訂單…", En: "Opening your order…"})

	KeyOrderOpenLink = key("order.open.link", Message{ZhHant: "開啟訂單", En: "Open your order"})

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
		ZhHant: "有帳號的話，",
		En:     "If you have an account, ",
	})

	KeyFindOrderRefused = key("order.find.refused", Message{
		ZhHant: "查不到符合的訂單。請確認訂單編號和 Email 都和確認信上的一樣。",
		En: "No order matches those details. Check that the number and the address are " +
			"both exactly as they appear in your confirmation email.",
	})

	KeyPlacementGrantFailedTitle = key("order.placement.grantfailed.title", Message{
		ZhHant: "已收到你的訂單，但尚未完成存取",
		En:     "Your order was received, but access could not be set up",
	})

	KeyPlacementGrantFailedBody = key("order.placement.grantfailed.body", Message{
		ZhHant: "我們已收到這筆訂單，但無法在這個瀏覽器上完成存取。請勿再次下單。用確認信裡的訂單編號與 Email 到「查詢訂單」完成存取。",
		En: "We received your order, but could not set up access in this browser. Do not place " +
			"another order. Use the order number and email from your confirmation to find it.",
	})

	KeyFindOrderGrantFailedTitle = key("order.find.grantfailed.title", Message{
		ZhHant: "無法完成查詢",
		En:     "Lookup could not be completed",
	})

	KeyFindOrderGrantFailedBody = key("order.find.grantfailed.body", Message{
		ZhHant: "訂單資料相符，但無法在這個瀏覽器上完成存取。請稍後再試一次查詢。",
		En: "Those details matched an order, but access could not be set up in this browser. " +
			"Try finding your order again.",
	})
)
