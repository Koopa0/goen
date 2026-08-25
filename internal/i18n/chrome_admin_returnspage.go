package i18n

var (
	KeyAdminRetLead = key("admin.ret.lead", Message{
		ZhHant: "同意退貨會立刻透過 Stripe 退款,金額由訂單本身的單價計算。",
		En: "Approving a return refunds it through Stripe immediately, for an amount computed from " +
			"the order's own unit prices.",
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
	KeyAdminRetScrapped  = key("admin.ret.scrapped", Message{ZhHant: "(未入庫)", En: "(not restocked)"})

	KeyAdminRetResolution  = key("admin.ret.resolution", Message{ZhHant: "處理說明", En: "Resolution note"})
	KeyAdminRetApprove     = key("admin.ret.approve", Message{ZhHant: "同意並退款", En: "Approve and refund"})
	KeyAdminRetReject      = key("admin.ret.reject", Message{ZhHant: "不同意", En: "Decline"})
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
	KeyAdminRetReceivedQty  = key("admin.ret.receivedqty", Message{ZhHant: "實際收到", En: "Actually received"})
	KeyAdminRetRestockedQty = key("admin.ret.restockedqty", Message{
		ZhHant: "可再販售(入庫)",
		En:     "Sellable again (back in stock)",
	})
	KeyAdminRetNoVariant = key("admin.ret.novariant", Message{
		ZhHant: "這個品項已無對應規格,無法入庫。",
		En:     "This item no longer has a variant to go back into, so nothing can be restocked.",
	})
	KeyAdminRetNote            = key("admin.ret.note", Message{ZhHant: "驗貨說明", En: "Inspection note"})
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
	KeyAdminRetCloseNote   = key("admin.ret.closenote", Message{ZhHant: "結案說明", En: "Closing note"})
	KeyAdminRetCloseButton = key("admin.ret.closebutton", Message{ZhHant: "結案", En: "Close the return"})
)
