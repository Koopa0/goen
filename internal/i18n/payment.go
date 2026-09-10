package i18n

var (
	KeyPayEyebrow = key("pay.eyebrow", Message{ZhHant: "完成付款", En: "Complete payment"})

	KeyPayBody = key("pay.body", Message{
		ZhHant: "訂單已成立,商品已為您保留。完成付款後我們會立即安排出貨。",
		En: "The order is placed and the stock is held for you. We pack it as soon as the " +
			"payment goes through.",
	})

	KeyPayCancelled = key("pay.cancelled", Message{
		ZhHant: "付款已取消,訂單仍然保留。您可以再試一次。",
		En:     "The payment was cancelled. The order is still here — you can try again.",
	})

	KeyPayStripeNote = key("pay.stripe", Message{
		ZhHant: "付款由 Stripe 處理,goen 不會接觸到您的卡片資料。",
		En:     "Stripe handles the payment. goen never sees your card details.",
	})

	KeyPayDisabled = key("pay.disabled", Message{
		ZhHant: "這個環境尚未啟用線上付款。訂單已經保留,稍後可以再回到這個頁面。",
		En: "Online payment is not enabled in this environment. The order is held; come " +
			"back to this page later.",
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
		ZhHant: "訂單目前的狀態不接受付款。如果這不符合預期,請透過聯絡我們告訴我們。",
		En: "The order is not in a state that accepts payment. If that is not what you " +
			"expected, get in touch and tell us.",
	})

	KeyLoggedTryAgain = key("error.logged", Message{
		ZhHant: "我們已經記錄這個問題。請稍後再試一次。",
		En:     "We have logged the problem. Please try again shortly.",
	})

	KeyPayViewOrder = key("pay.vieworder", Message{ZhHant: "查看訂單", En: "View order"})

	KeyPayMeta = key("pay.meta", Message{ZhHant: "付款 %s", En: "Pay for %s"})

	KeyShippingAndTax = key("pay.shippingandtax", Message{
		ZhHant: "運費與稅金",
		En:     "Delivery and tax",
	})

	// A reduced order reaches Stripe as one line naming itself; the itemisation
	// lives on goen's own order page, which the confirmation links to.
	KeyPayOrderLine = key("pay.orderline", Message{ZhHant: "訂單 %s", En: "Order %s"})
)
