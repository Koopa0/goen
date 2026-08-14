package i18n

// /admin/returns — the return queue: decide, receive the parcel, inspect, close.
//
// This is the one back-office screen built to inform a decision the law has
// already made. 消保法 §19 I gives seven days from RECEIPT and §19 V voids any
// agreement to the contrary, so a request inside the window is not the shop's to
// refuse on window grounds — and the English has to say that rather than
// paraphrase it, because a staff member who reads "trial period" would decide
// the way shop policy suggests. The window LABELS themselves are
// KeyAdminReturnWindow* in chrome_admin.go, next to the rest of the queue's
// vocabulary; what is here is the page around them.

var (
	// The heading's lead. It names the two facts somebody has to know before
	// pressing 同意: the money leaves immediately, and the figure is not theirs
	// to type.
	KeyAdminRetLead = key("admin.ret.lead", Message{
		ZhHant: "同意退貨會立刻透過 Stripe 退款,金額由訂單本身的單價計算。",
		En: "Approving a return refunds it through Stripe immediately, for an amount computed from " +
			"the order's own unit prices.",
	})
	KeyAdminRetEmpty = key("admin.ret.empty", Message{
		ZhHant: "目前沒有退貨申請。",
		En:     "No return requests at the moment.",
	})

	// One row: how much is coming back and what it is worth.
	KeyAdminRetUnitsAmount = key("admin.ret.unitsamount", Message{
		ZhHant: "%s 件 · 可退 %s",
		En:     "%s items · %s refundable",
	})
	// Printed beside 七日鑑賞期內, and it is the part a decision turns on.
	//
	// §19 I's 「不負擔任何費用」 is what puts the original delivery fee inside the
	// refund — not §19-2, which is about the trader's duty to collect and carries
	// no cost-allocation sentence at all. The English states the obligation
	// rather than describing the request, because "this one is statutory" leaves
	// somebody to work out what follows from it.
	KeyAdminRetMustAccept = key("admin.ret.mustaccept", Message{
		ZhHant: "(依法不得拒絕退貨,退款含原運費)",
		En:     "(the law does not allow this one to be refused, and the refund includes the original delivery fee)",
	})

	// What was found once the parcel was opened. Absent until somebody looks:
	// "not inspected yet" and "inspected, nothing arrived" are different facts.
	KeyAdminRetReceivedRestocked = key("admin.ret.receivedrestocked", Message{
		ZhHant: "收到 %s · 入庫 %s",
		En:     "%s received · %s back in stock",
	})
	// Two flags rather than two numbers to compare, because each sends a staff
	// member somewhere: a shortfall is a conversation with the customer, a
	// scrapped unit is not.
	KeyAdminRetShortfall = key("admin.ret.shortfall", Message{ZhHant: "(短少)", En: "(short)"})
	KeyAdminRetScrapped  = key("admin.ret.scrapped", Message{ZhHant: "(未入庫)", En: "(not restocked)"})

	// The decision form.
	KeyAdminRetResolution = key("admin.ret.resolution", Message{ZhHant: "處理說明", En: "Resolution note"})
	KeyAdminRetApprove    = key("admin.ret.approve", Message{ZhHant: "同意並退款", En: "Approve and refund"})
	KeyAdminRetReject     = key("admin.ret.reject", Message{ZhHant: "不同意", En: "Decline"})

	// The inspection form — one form for the parcel, one set of fields per line.
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
	// 規格 is a VARIANT here, not a spec row. order_lines.variant_id is nullable,
	// so a line outlives the variant it was sold from, and the restock control is
	// absent rather than present and refused by the write.
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

	// Closing. return_requests_completed_is_inspected is what makes 'completed'
	// mean something, so the figure is stated at the moment somebody agrees the
	// return is finished — "three came back, two went on the shelf" is what they
	// are agreeing to.
	KeyAdminRetRestockedUnits = key("admin.ret.restockedunits", Message{
		ZhHant: "驗貨已完成,共 %s 件回到庫存。",
		En:     "Inspection finished — %s units went back into stock.",
	})
	KeyAdminRetCloseNote   = key("admin.ret.closenote", Message{ZhHant: "結案說明", En: "Closing note"})
	KeyAdminRetCloseButton = key("admin.ret.closebutton", Message{ZhHant: "結案", En: "Close the return"})
)
