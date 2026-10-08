package i18n

var (
	KeyPaySandbox = key("pay.sandbox", Message{
		ZhHant: "這是 Stripe 測試付款，不會收取真實款項。請勿輸入真實卡號；請使用測試卡 4242 4242 4242 4242、任意未來到期日與任意 3 位數安全碼。",
		En:     "This is a Stripe test payment; no real money is charged. Do not enter a real card. Use test card 4242 4242 4242 4242, any future expiry date and any three-digit CVC.",
	})

	KeyPayBody = key("pay.body", Message{
		ZhHant: "訂單已送出，商品已為你保留。完成付款後我們會立即安排出貨。",
		En: "The order is placed and the stock is held for you. We pack it as soon as the " +
			"payment goes through.",
	})

	KeyPayCancelled = key("pay.cancelled", Message{
		ZhHant: "付款已取消，訂單仍然保留。你可以再試一次。",
		En:     "The payment was cancelled. The order is still here — you can try again.",
	})

	KeyPayStripeNote = key("pay.stripe", Message{
		ZhHant: "付款由 Stripe 處理，goen 不會接觸到你的卡片資料。",
		En:     "Stripe handles the payment. goen never sees your card details.",
	})

	KeyPayDisabled = key("pay.disabled", Message{
		ZhHant: "這個環境尚未啟用線上付款，稍後可以再回到這個頁面付款。",
		En:     "Online payment is not enabled in this environment; come back to this page later to pay.",
	})

	KeyPayOffTitle = key("pay.off.title", Message{
		ZhHant: "尚未開放付款",
		En:     "Payment not enabled",
	})

	KeyPayOffHeading = key("pay.off.heading", Message{
		ZhHant: "金流尚未啟用",
		En:     "Online payment is not switched on",
	})

	KeyPayRefusedTitle = key("pay.refused.title", Message{
		ZhHant: "這筆訂單無法付款",
		En:     "This order cannot be paid for",
	})

	KeyPayRefusedBody = key("pay.refused.body", Message{
		ZhHant: "訂單目前的狀態不接受付款。如果這不符合預期，請透過聯絡我們告訴我們。",
		En: "The order is not in a state that accepts payment. If that is not what you " +
			"expected, get in touch and tell us.",
	})

	KeyLoggedTryAgain = key("error.logged", Message{
		ZhHant: "我們已經記錄這個問題。請稍後再試一次。",
		En:     "We have logged the problem. Please try again shortly.",
	})

	KeyPayProcessingTitle = key("pay.processing.title", Message{
		ZhHant: "付款處理中",
		En:     "Payment processing",
	})

	KeyPayProcessingBody = key("pay.processing.body", Message{
		ZhHant: "我們正在確認你的付款結果，請稍候。若已扣款完成，訂單將會自動更新。",
		En:     "Your payment is being processed. The order will update automatically once confirmed.",
	})

	KeyOrderPaymentChecking            = key("order.payment.checking", Message{ZhHant: "我們正在確認付款結果。此頁每 %d 秒更新，最多 %d 次；更新結束後會顯示最新訂單狀態。", En: "We are checking the payment result. This page refreshes every %d seconds, up to %d times, then shows the latest order state."})
	KeyOrderCheckPayment               = key("order.payment.check", Message{ZhHant: "立即更新", En: "Check now"})
	KeyOrderStopChecking               = key("order.payment.stop", Message{ZhHant: "停止自動更新", En: "Stop automatic updates"})
	KeyOrderPaymentConfirmationPending = key("order.payment.confirmationpending", Message{ZhHant: "我們還在確認付款結果，確認後會寄信通知你，請不要重複付款。", En: "We are still confirming your payment and will email you; please do not pay again."})

	KeyPayViewOrder = key("pay.vieworder", Message{ZhHant: "查看訂單", En: "View order"})

	KeyPayWindowClosedTitle = key("pay.windowclosed.title", Message{ZhHant: "付款期限已過", En: "The payment window has closed"})
	KeyPayWindowClosedBody  = key("pay.windowclosed.body", Message{ZhHant: "這筆訂單已無法開始付款，會在商品保留結束時自動取消，不會收取任何款項；若有使用購物金，取消時會退回你的購物金餘額。", En: "This order can no longer be paid. It is cancelled automatically when the hold on the items ends: nothing is charged, and any store credit you applied goes back to your store credit balance."})

	// The stock hold in two lines: the deadline to start paying and then the hold's end, or the hold's end alone
	// once no deadline is named.
	KeyPayDeadline     = key("pay.deadline", Message{ZhHant: "請在 %s 前開始付款", En: "Start paying by %s"})
	KeyPayReservedNote = key("pay.reserved.note", Message{ZhHant: "商品保留到 %s，逾時未付款，會自動取消訂單。時間以台灣時間為準。", En: "Your items are reserved until %s and the order is cancelled if it is still unpaid then. Times are Taiwan time."})
	KeyPayReserved     = key("pay.reserved", Message{ZhHant: "商品保留到 %s", En: "Your items are reserved until %s"})
	KeyPayReservedTail = key("pay.reserved.tail", Message{ZhHant: "逾時未付款，會自動取消訂單。時間以台灣時間為準。", En: "The order is cancelled if it is still unpaid then. Times are Taiwan time."})

	KeyPayFactAmountDue = key("pay.fact.amountdue", Message{ZhHant: "應付金額", En: "Amount due"})
	KeyPayFactPlaced    = key("pay.fact.placed", Message{ZhHant: "送出", En: "Placed"})
	KeyPayFactCancelled = key("pay.fact.cancelled", Message{ZhHant: "自動取消", En: "Cancelled automatically"})
	KeyPayFactLapsed    = key("pay.fact.lapsed", Message{ZhHant: "庫存保留結束時仍未付款", En: "Still unpaid when the reservation ended"})
	KeyPayFactCharged   = key("pay.fact.charged", Message{ZhHant: "收取金額", En: "Amount charged"})

	KeyPayMeta = key("pay.meta", Message{ZhHant: "付款 %s", En: "Pay for %s"})

	KeyShippingAndTax = key("pay.shippingandtax", Message{
		ZhHant: "運費與稅金",
		En:     "Delivery and tax",
	})

	// A reduced order reaches Stripe as one line naming itself; the itemisation
	// lives on goen's own order page, which the confirmation links to.
	KeyPayOrderLine = key("pay.orderline", Message{ZhHant: "訂單 %s", En: "Order %s"})
)
