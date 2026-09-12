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

	KeyReturnInvalid = key("returns.invalid", Message{
		ZhHant: "請至少選擇一件商品,並確認填寫的資料。",
		En:     "Choose at least one item and check the entered details.",
	})

	KeyReturnAccountErased = key("returns.account.erased", Message{
		ZhHant: "帳號已在送出期間刪除,無法接收這筆退貨的購物金。請聯絡客服協助處理。",
		En:     "The account was deleted while this request was being sent, so it cannot receive the store-credit refund. Contact support for help.",
	})

	KeyReturnMeta = key("returns.meta", Message{ZhHant: "退貨申請 %s", En: "Return request — %s"})

	KeyReturnStateOpen = key("returns.state.open", Message{ZhHant: "已送出,等待處理", En: "Sent, awaiting a decision"})

	KeyReturnStateApproved = key("returns.state.approved", Message{ZhHant: "已同意退貨", En: "Approved"})

	KeyReturnStateRefused = key("returns.state.refused", Message{ZhHant: "未同意退貨", En: "Declined"})

	KeyReturnStateDone = key("returns.state.done", Message{ZhHant: "退貨完成", En: "Completed"})
)

var (
	KeyAdminReturnRequested = key("admin.return.requested", Message{ZhHant: "待處理", En: "Open"})

	KeyAdminReturnApproved = key("admin.return.approved", Message{ZhHant: "已同意", En: "Approved"})

	KeyAdminReturnRejected = key("admin.return.rejected", Message{ZhHant: "未同意", En: "Declined"})

	KeyAdminReturnCompleted = key("admin.return.completed", Message{ZhHant: "已完成", En: "Completed"})

	// Consumer Protection Act §19's seven days, a right §19 V makes unwaivable —
	// so the English says "right to cancel" and never "trial period".
	KeyAdminReturnWindowWithin = key("admin.return.window.within", Message{
		ZhHant: "七日鑑賞期內",
		En:     "Within the statutory 7-day right to cancel",
	})

	KeyAdminReturnWindowAfter = key("admin.return.window.after", Message{
		ZhHant: "已逾鑑賞期",
		En:     "Past the statutory 7-day right to cancel",
	})

	KeyAdminReturnWindowUndelivered = key("admin.return.window.undelivered", Message{
		ZhHant: "尚未送達",
		En:     "Not delivered yet, so the window has not started",
	})

	KeyAdminPageReturns = key("admin.page.returns", Message{ZhHant: "退貨申請", En: "Return requests"})

	KeyAdminRetLead = key("admin.ret.lead", Message{
		ZhHant: "同意退貨會依原付款組成退回:卡款走 Stripe,店儲退回額度。金額由訂單本身的單價計算。",
		En: "Approving a return pays it back the way it was funded: the card half through Stripe, " +
			"store credit back to the balance. The amount is computed from the order's own unit prices.",
	})

	KeyAdminRetPayoutCard = key("admin.ret.payout.card", Message{
		ZhHant: "卡款 %s 走 Stripe",
		En:     "Card %s refunds through Stripe",
	})

	KeyAdminRetPayoutCredit = key("admin.ret.payout.credit", Message{
		ZhHant: "店儲 %s 退回額度",
		En:     "Store credit %s returns to the balance",
	})

	KeyAdminRetPayoutSplit = key("admin.ret.payout.split", Message{
		ZhHant: "卡款 %s 走 Stripe,店儲 %s 退回額度",
		En:     "Card %s through Stripe, store credit %s back to the balance",
	})

	KeyAdminRetEmpty = key("admin.ret.empty", Message{
		ZhHant: "目前沒有退貨申請。",
		En:     "No return requests at the moment.",
	})

	KeyAdminRetUnitsAmount = key("admin.ret.unitsamount", Message{
		ZhHant: "%s 件 · 可退 %s",
		En:     "%s items · %s refundable",
	})

	// The delivery fee goes back under Consumer Protection Act §19 I ("at no cost
	// to the consumer"), not under §19-2, which allocates no costs at all.
	KeyAdminRetMustAccept = key("admin.ret.mustaccept", Message{
		ZhHant: "(依法不得拒絕退貨,退款含原運費)",
		En:     "(the law does not allow this one to be refused, and the refund includes the original delivery fee)",
	})

	KeyAdminRetReceivedRestocked = key("admin.ret.receivedrestocked", Message{
		ZhHant: "收到 %s · 入庫 %s",
		En:     "%s received · %s back in stock",
	})

	KeyAdminRetShortfall = key("admin.ret.shortfall", Message{ZhHant: "(短少)", En: "(short)"})

	KeyAdminRetScrapped = key("admin.ret.scrapped", Message{ZhHant: "(未入庫)", En: "(not restocked)"})

	KeyAdminRetResolution = key("admin.ret.resolution", Message{ZhHant: "處理說明", En: "Resolution note"})

	KeyAdminRetApprove = key("admin.ret.approve", Message{ZhHant: "同意並退款", En: "Approve and refund"})

	KeyAdminRetReject = key("admin.ret.reject", Message{ZhHant: "不同意", En: "Decline"})

	KeyAdminRetRetryPayout = key("admin.ret.retrypayout", Message{
		ZhHant: "重新退款",
		En:     "Send the refund again",
	})

	KeyAdminRetPayoutOutstanding = key("admin.ret.payoutoutstanding", Message{
		ZhHant: "這筆退貨已核准,但款項尚未退回。重新送出會沿用同一個退款識別,不會重複付款。",
		En:     "This return is approved, but its money has not gone back. Sending it again uses the same refund key, so it cannot pay twice.",
	})

	KeyAdminRetPayoutStranded = key("admin.ret.payoutstranded", Message{
		ZhHant: "付款服務已明確拒絕這筆退款,無法從這裡重送。請在 Stripe 手動退款,並查看系統健康頁。",
		En:     "The provider refused this refund outright, so it cannot be re-sent here. Refund it manually in Stripe and check the health page.",
	})

	KeyAdminRetInspectHint = key("admin.ret.inspecthint", Message{
		ZhHant: "收到退回的商品後,逐項填寫實際收到與可再販售的數量。",
		En: "Once the returned goods arrive, fill in per line how many actually came back and how " +
			"many of them can be sold again.",
	})

	KeyAdminRetReceivedQty = key("admin.ret.receivedqty", Message{ZhHant: "實際收到", En: "Actually received"})

	KeyAdminRetRestockedQty = key("admin.ret.restockedqty", Message{
		ZhHant: "可再販售(入庫)",
		En:     "Sellable again (back in stock)",
	})

	KeyAdminRetNoVariant = key("admin.ret.novariant", Message{
		ZhHant: "這個品項已無對應規格,無法入庫。",
		En:     "This item no longer has a variant to go back into, so nothing can be restocked.",
	})

	KeyAdminRetNote = key("admin.ret.note", Message{ZhHant: "驗貨說明", En: "Inspection note"})

	KeyAdminRetNotePlaceholder = key("admin.ret.noteplaceholder", Message{
		ZhHant: "例如:外盒破損、配件缺少",
		En:     "For example: box damaged, accessories missing",
	})

	KeyAdminRetInspectButton = key("admin.ret.inspectbutton", Message{
		ZhHant: "記錄驗貨並入庫",
		En:     "Record the inspection and restock",
	})

	KeyAdminRetRestockedUnits = key("admin.ret.restockedunits", Message{
		ZhHant: "驗貨已完成,共 %s 件回到庫存。",
		En:     "Inspection finished — %s units went back into stock.",
	})

	KeyAdminRetCloseNote = key("admin.ret.closenote", Message{ZhHant: "結案說明", En: "Closing note"})

	KeyAdminRetCloseButton = key("admin.ret.closebutton", Message{ZhHant: "結案", En: "Close the return"})
)

var (
	// The DECISION stands: it is committed before any money moves, so that two
	// staff members deciding at once cannot both pay. What is outstanding here
	// is the payment alone.
	KeyAdminNoticeRefundFailed = key("admin.notice.refundfailed", Message{
		ZhHant: "這筆退貨已經核准,但退款沒有完成。退款紀錄已經留下,請確認 Stripe 後台後使用退貨列上的「重新退款」—— " +
			"核准本身不需要、也無法重做。",
		En: "This return is approved, but the refund did not complete. Its record has been written " +
			"either way — check the Stripe dashboard, then use Send the refund again on its row. The approval itself " +
			"neither needs nor allows redoing.",
	})

	KeyAdminNoticeInspected = key("admin.notice.inspected", Message{
		ZhHant: "驗貨已記錄,可再販售的數量已經入庫。",
		En:     "Inspection recorded. Whatever is sellable again is back on the shelf.",
	})

	KeyAdminNoticeClosed = key("admin.notice.closed", Message{ZhHant: "退貨已結案。", En: "Return closed."})

	KeyAdminNoticeBadCount = key("admin.notice.badcount", Message{
		ZhHant: "數量填寫有問題:入庫數不能超過實際收到的數量,實際收到也不能超過申請退回的數量。",
		En: "Those quantities do not work: what goes back on the shelf cannot exceed what arrived, " +
			"and what arrived cannot exceed what the customer asked to return.",
	})
)
