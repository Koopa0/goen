package i18n

var (
	KeyReturnTitle = key("returns.title", Message{ZhHant: "退貨申請", En: "Return request"})

	KeyReturnSub = key("returns.sub", Message{
		ZhHant: "已經出貨、還沒申請退貨的商品，都可以在這裡申請。",
		En:     "You can request a return here for anything that has shipped and is not already in a return.",
	})

	KeyReturnHistory = key("returns.history", Message{ZhHant: "申請紀錄", En: "Previous requests"})

	KeyReturnSentAt = key("returns.sentat", Message{ZhHant: "%s 送出", En: "Sent %s"})

	KeyReturnResolution = key("returns.resolution", Message{
		ZhHant: "處理說明：%s",
		En:     "Outcome: %s",
	})

	KeyReturnInFlight = key("returns.inflight", Message{
		ZhHant: "這筆訂單已經有一筆還在處理中的退貨申請，處理完成後才能再次申請。",
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
		ZhHant: "退貨數量（最多 %s）",
		En:     "How many (up to %s)",
	})

	KeyFieldReturnReason = key("field.return.reason", Message{ZhHant: "退貨原因", En: "Reason"})

	KeyReturnSubmit = key("returns.submit", Message{ZhHant: "送出申請", En: "Send request"})

	KeyBackToOrder = key("returns.backtoorder", Message{ZhHant: "回到訂單", En: "Back to the order"})

	KeyReturnTooMany = key("returns.toomany", Message{
		ZhHant: "數量超出可退貨的範圍。",
		En:     "That is more than can be returned.",
	})

	KeyReturnQuantityInvalid = key("returns.quantity.invalid", Message{ZhHant: "請填寫零或以上的整數。", En: "Enter a whole number of zero or more."})

	KeyReturnReasonTooLong = key("returns.reason.toolong", Message{ZhHant: "退貨原因最多 500 字。", En: "Keep the optional reason within 500 characters."})

	KeyReturnReasonUnsupportedControls = key("returns.reason.unsupportedcontrols", Message{ZhHant: "請移除退貨原因中不支援的控制字元。", En: "Remove unsupported control characters from the optional reason."})

	KeyReturnAlreadyOpen = key("returns.alreadyopen", Message{
		ZhHant: "這筆訂單已經有一筆還在處理中的退貨申請。",
		En:     "There is already an open request for this order.",
	})

	KeyReturnNothingShort = key("returns.nothing.short", Message{
		ZhHant: "這筆訂單目前沒有可以退貨的商品。",
		En:     "Nothing on this order can be returned.",
	})

	KeyReturnInvalid = key("returns.invalid", Message{
		ZhHant: "請至少選擇一件商品，並確認填寫的資料。",
		En:     "Choose at least one item and check the entered details.",
	})

	KeyReturnAccountErased = key("returns.account.erased", Message{
		ZhHant: "帳號已在送出期間刪除，無法接收這筆退貨的購物金。請聯絡客服協助處理。",
		En:     "The account was deleted while this request was being sent, so it cannot receive the store-credit refund. Contact support for help.",
	})

	KeyReturnMeta = key("returns.meta", Message{ZhHant: "退貨申請 %s", En: "Return request — %s"})

	KeyReturnStateOpen = key("returns.state.open", Message{ZhHant: "已送出，等待處理", En: "Sent, awaiting a decision"})

	KeyReturnStateApproved = key("returns.state.approved", Message{ZhHant: "已同意退貨", En: "Approved"})

	KeyReturnStateRefused = key("returns.state.refused", Message{ZhHant: "未同意退貨", En: "Declined"})

	KeyReturnStateDone = key("returns.state.done", Message{ZhHant: "退貨完成", En: "Completed"})
)

var (
	KeyAdminReturnRequested = key("admin.return.requested", Message{ZhHant: "待處理", En: "Open"})

	KeyAdminReturnApproved = key("admin.return.approved", Message{ZhHant: "已同意", En: "Approved"})

	KeyAdminReturnRejected = key("admin.return.rejected", Message{ZhHant: "未同意", En: "Declined"})

	KeyAdminReturnCompleted = key("admin.return.completed", Message{ZhHant: "已完成", En: "Completed"})

	// KeyAdminReturnCancelledRefunded is what a finished refund before shipment is
	// in the returns queue: the order was cancelled and paid back, and nothing came
	// back to be inspected, so "completed" would read as a return that was.
	KeyAdminReturnCancelledRefunded = key("admin.return.cancelledrefunded", Message{
		ZhHant: "已取消並退款",
		En:     "Cancelled and refunded",
	})

	KeyAdminReturnRefundFailed = key("admin.return.refundfailed", Message{ZhHant: "退款失敗", En: "Refund failed"})

	KeyAdminReturnRefundToResend = key("admin.return.refundtoresend", Message{ZhHant: "待重新退款", En: "Refund to resend"})

	KeyAdminReturnReadyToClose = key("admin.return.readytoclose", Message{ZhHant: "待結案", En: "Ready to close"})

	KeyAdminReturnOnItsWay = key("admin.return.onitsway", Message{ZhHant: "退回中", En: "On its way back"})

	// Consumer Protection Act §19's seven days, a right §19 V makes unwaivable —
	// so the English says "right to cancel" and never "trial period".
	KeyAdminReturnWindowWithin = key("admin.return.window.within", Message{
		ZhHant: "七日猶豫期內",
		En:     "Within the statutory 7-day right to cancel",
	})

	KeyAdminReturnWindowGoodwill = key("admin.return.window.goodwill", Message{
		ZhHant: "送達後第 8–14 天（店家優惠）",
		En:     "Days 8–14 of the shop's voluntary offer",
	})

	KeyAdminReturnWindowAfter = key("admin.return.window.after", Message{
		ZhHant: "已逾猶豫期",
		En:     "Past the statutory 7-day right to cancel",
	})

	KeyAdminReturnWindowUndelivered = key("admin.return.window.undelivered", Message{
		ZhHant: "尚未送達",
		En:     "Not delivered yet, so the window has not started",
	})

	KeyAdminReturnWindowMixed = key("admin.return.window.mixed", Message{
		ZhHant: "品項分屬不同期限",
		En:     "Lines fall in different return windows",
	})

	KeyAdminPageReturns = key("admin.page.returns", Message{ZhHant: "退貨申請", En: "Return requests"})

	KeyAdminRetLead = key("admin.ret.lead", Message{
		ZhHant: "同意退貨會依原付款組成退回：卡款走 Stripe，購物金退回餘額。金額由訂單本身的單價計算。",
		En: "Approving a return pays it back the way it was funded: the card half through Stripe, " +
			"store credit back to the balance. The amount is computed from the order's own unit prices.",
	})

	KeyAdminRetPayoutCard = key("admin.ret.payout.card", Message{
		ZhHant: "卡款 %s 走 Stripe",
		En:     "Card %s refunds through Stripe",
	})

	KeyAdminRetPayoutCredit = key("admin.ret.payout.credit", Message{
		ZhHant: "購物金 %s 退回餘額",
		En:     "Store credit %s returns to the balance",
	})

	KeyAdminRetPayoutSplit = key("admin.ret.payout.split", Message{
		ZhHant: "卡款 %s 走 Stripe，購物金 %s 退回餘額",
		En:     "Card %s through Stripe, store credit %s back to the balance",
	})

	KeyAdminRetEmpty = key("admin.ret.empty", Message{
		ZhHant: "目前沒有退貨申請。",
		En:     "No return requests at the moment.",
	})

	KeyAdminRetUnitsAmount = countKey("admin.ret.unitsamount", "%s 件 · 可退 %s", "%s item · %s refundable", "%s items · %s refundable")

	// The delivery fee goes back under Consumer Protection Act §19 I ("at no cost
	// to the consumer"), not under §19-2, which allocates no costs at all.
	KeyAdminRetMustAccept = key("admin.ret.mustaccept", Message{
		ZhHant: "（依法不得拒絕退貨，退款含原運費）",
		En:     "(the law does not allow this one to be refused, and the refund includes the original delivery fee)",
	})

	KeyAdminRetGoodwillHint = key("admin.ret.goodwillhint", Message{
		ZhHant: "未使用且包裝與配件齊全才是政策內受理。請先逐項評估這三項：有未知就不能核准或拒絕；三項皆符合才記政策內權利。",
		En:     "Unused and complete with box and accessories is the advertised offer. Assess those three facts first: unknown cannot be approved or declined, and only all-met records a policy entitlement.",
	})

	KeyAdminRetLateHint = key("admin.ret.latehint", Message{
		ZhHant: "已逾 14 天。同意是人工例外，不是政策內權利。",
		En:     "Past 14 days. Approval is a staff exception, not a policy entitlement.",
	})

	KeyAdminRetReceivedRestocked = key("admin.ret.receivedrestocked", Message{
		ZhHant: "收到 %s · 入庫 %s",
		En:     "%s received · %s back in stock",
	})

	KeyAdminRetShortfall = key("admin.ret.shortfall", Message{ZhHant: "（短少）", En: "(short)"})

	KeyAdminRetScrapped = key("admin.ret.scrapped", Message{ZhHant: "（未入庫）", En: "(not restocked)"})

	KeyAdminRetResolution = key("admin.ret.resolution", Message{ZhHant: "處理說明", En: "Resolution note"})

	// KeyAdminRequiredMark follows the label of a field the form will not accept
	// empty, because the browser says so only after the button is pressed.
	KeyAdminRequiredMark = key("admin.field.required", Message{ZhHant: "必填", En: "Required"})

	KeyAdminRetApprove = key("admin.ret.approve", Message{ZhHant: "同意並退款", En: "Approve and refund"})

	KeyAdminRetReject = key("admin.ret.reject", Message{ZhHant: "不同意", En: "Decline"})

	KeyAdminRetException = key("admin.ret.exception", Message{
		ZhHant: "以人工例外核准",
		En:     "Approve as a staff exception",
	})

	KeyAdminRetAssessHint = key("admin.ret.assesshint", Message{
		ZhHant: "決策前先逐項記下未使用、原包裝齊全、配件齊全。預設是未知；未知不是不符合，也不能當成符合。",
		En:     "Before deciding, record unused, original packaging, and accessories per line. Unknown is the default: it is not a failure, and it is not a pass.",
	})

	KeyAdminRetAssessBasis = key("admin.ret.assessbasis", Message{
		ZhHant: "評估依據",
		En:     "What this assessment is based on",
	})

	KeyAdminRetFactUnused = key("admin.ret.fact.unused", Message{ZhHant: "未使用", En: "Unused"})

	KeyAdminRetFactPackaging = key("admin.ret.fact.packaging", Message{
		ZhHant: "原包裝齊全",
		En:     "Original packaging complete",
	})

	KeyAdminRetFactAccessories = key("admin.ret.fact.accessories", Message{
		ZhHant: "配件齊全",
		En:     "Accessories complete",
	})

	KeyAdminRetFactUnknown = key("admin.ret.fact.unknown", Message{ZhHant: "未知", En: "Unknown"})

	KeyAdminRetFactMet = key("admin.ret.fact.met", Message{ZhHant: "符合", En: "Met"})

	KeyAdminRetFactUnmet = key("admin.ret.fact.unmet", Message{ZhHant: "不符合", En: "Not met"})

	KeyAdminRetAssessButton = key("admin.ret.assessbutton", Message{
		ZhHant: "儲存資格評估",
		En:     "Save the eligibility assessment",
	})

	KeyAdminRetAssessedAt = key("admin.ret.assessedat", Message{
		ZhHant: "評估版本 %s · %s",
		En:     "Assessment version %s · %s",
	})

	KeyAdminRetErrStatutoryReject = key("admin.ret.err.statutoryreject", Message{
		ZhHant: "七日內的有效申請不能因未填原因、拆封或第 8–14 天條件而拒絕。",
		En:     "A valid request inside seven days cannot be refused for a missing reason, for opening the parcel, or for the days 8–14 conditions.",
	})

	KeyAdminRetErrIncomplete = key("admin.ret.err.incomplete", Message{
		ZhHant: "還有未觀察的品項。補齊三項評估後才能核准或拒絕；未知不算符合，也不算不符合。",
		En:     "Some facts are still unknown. Finish the three observations before approving or declining; unknown is neither a pass nor a failure.",
	})

	KeyAdminRetErrNeedException = key("admin.ret.err.needexception", Message{
		ZhHant: "這筆不能記成政策內權利。若要核准，請明確選擇人工例外。",
		En:     "This cannot be recorded as a policy entitlement. To pay it, choose a staff exception.",
	})

	KeyAdminRetErrUnmetApprove = key("admin.ret.err.unmetapprove", Message{
		ZhHant: "已有不符合的觀察，不能記成第 8–14 天政策內權利。拒絕須引用該事實，或改選人工例外。",
		En:     "An unmet observation cannot be recorded as the days 8–14 policy entitlement. Decline by citing that fact, or approve as a staff exception.",
	})

	KeyAdminRetErrNoUnmet = key("admin.ret.err.nounmet", Message{
		ZhHant: "沒有已確認不符合的事實可以引用，因此不能拒絕。",
		En:     "There is no confirmed unmet fact to cite, so this cannot be declined.",
	})

	KeyAdminRetErrUseApprove = key("admin.ret.err.useapprove", Message{
		ZhHant: "條件已符合政策，請用「同意並退款」而不是人工例外。",
		En:     "The advertised conditions are met, so use Approve and refund rather than a staff exception.",
	})

	KeyAdminRetErrStale = key("admin.ret.err.stale", Message{
		ZhHant: "評估已被其他人更新。請重新讀取後再決定。",
		En:     "Someone else updated the assessment. Reload and decide again.",
	})

	KeyAdminRetErrBasis = key("admin.ret.err.basis", Message{
		ZhHant: "請寫下這次評估依據，且不要超過 500 字。",
		En:     "Say what this assessment is based on, in at most 500 characters.",
	})

	KeyAdminRetErrExceptionReason = key("admin.ret.err.exceptionreason", Message{
		ZhHant: "人工例外必須寫下核准理由，空白或只有空白字元不能付款。",
		En:     "A staff exception needs a recorded reason; blank or whitespace cannot pay.",
	})

	KeyAdminRetRetryPayout = key("admin.ret.retrypayout", Message{
		ZhHant: "重新退款",
		En:     "Send the refund again",
	})

	KeyAdminRetPayoutOutstanding = key("admin.ret.payoutoutstanding", Message{
		ZhHant: "這筆退貨已核准，但款項尚未退回。重新送出會沿用同一個退款識別，不會重複付款。",
		En:     "This return is approved, but its money has not gone back. Sending it again uses the same refund key, so it cannot pay twice.",
	})

	KeyAdminRetPayoutStranded = key("admin.ret.payoutstranded", Message{
		ZhHant: "付款服務已明確拒絕這筆退款，無法從這裡重送。請在 Stripe 手動退款，並查看系統健康頁。",
		En:     "The provider refused this refund outright, so it cannot be re-sent here. Refund it manually in Stripe and check the health page.",
	})

	KeyAdminRetInspectHint = key("admin.ret.inspecthint", Message{
		ZhHant: "收到退回的商品後，逐項填寫實際收到與可再販售的數量。",
		En: "Once the returned goods arrive, fill in per line how many actually came back and how " +
			"many of them can be sold again.",
	})

	KeyAdminRetReceivedQty = key("admin.ret.receivedqty", Message{ZhHant: "實際收到", En: "Actually received"})

	KeyAdminRetRestockedQty = key("admin.ret.restockedqty", Message{
		ZhHant: "可再販售（入庫）",
		En:     "Sellable again (back in stock)",
	})

	KeyAdminRetNoVariant = key("admin.ret.novariant", Message{
		ZhHant: "這個品項已無對應規格，無法入庫。",
		En:     "This item no longer has a variant to go back into, so nothing can be restocked.",
	})

	KeyAdminRetNote = key("admin.ret.note", Message{ZhHant: "驗貨說明", En: "Inspection note"})

	KeyAdminRetNotePlaceholder = key("admin.ret.noteplaceholder", Message{
		ZhHant: "例如：外盒破損、配件缺少",
		En:     "For example: box damaged, accessories missing",
	})

	KeyAdminRetInspectButton = key("admin.ret.inspectbutton", Message{
		ZhHant: "記錄驗貨並入庫",
		En:     "Record the inspection and restock",
	})

	KeyAdminRetRestockedUnits = countKey("admin.ret.restockedunits",
		"驗貨已完成，共 %s 件回到庫存。",
		"Inspection finished — %s unit went back into stock.",
		"Inspection finished — %s units went back into stock.")

	KeyAdminRetCloseNote = key("admin.ret.closenote", Message{ZhHant: "結案說明", En: "Closing note"})

	KeyAdminRetCloseButton = key("admin.ret.closebutton", Message{ZhHant: "結案", En: "Close the return"})
)

var (
	// The DECISION stands: it is committed before any money moves, so that two
	// staff members deciding at once cannot both pay. What is outstanding here
	// is the payment alone.
	KeyAdminNoticeRefundFailed = key("admin.notice.refundfailed", Message{
		ZhHant: "這筆退貨已核准，但退款沒有完成。請到 Stripe 後台確認後，按退貨列上的「重新退款」；不需要重新核准。",
		En:     "This return is approved, but the refund did not complete. Check the Stripe dashboard, then press “Send the refund again” on its row. The approval stands.",
	})

	// The order page's own sentence for a refund before shipment: Resume on
	// that page is the retry, and the returns queue only links back to it.
	KeyAdminNoticeRefundRetry = key("admin.notice.refundretry", Message{
		ZhHant: "退款沒有完成。請到 Stripe 後台確認這筆款項，再按「繼續退款」。",
		En:     "The refund did not complete. Check the payment in the Stripe dashboard, then press “Resume the refund”.",
	})

	KeyAdminNoticeCancelRetry = key("admin.notice.cancelretry", Message{
		ZhHant: "退款已完成，但訂單還沒取消。請按「繼續退款」完成取消。",
		En:     "The refund went through, but the order is not cancelled yet. Press “Resume the refund” to finish.",
	})

	KeyAdminNoticeRefundPending = key("admin.notice.refundpending", Message{
		ZhHant: "退款已記錄，但 Stripe 尚未完成。請確認 Stripe 後台，再按「繼續退款」。",
		En:     "The refund is recorded but Stripe has not settled it. Check the Stripe dashboard, then press Resume the refund.",
	})

	KeyAdminNoticeRefunded = key("admin.notice.refunded", Message{
		ZhHant: "已全額退款，訂單已取消，保留的庫存已釋出。",
		En:     "Refunded in full. The order is cancelled and the stock it held is released.",
	})

	KeyAdminNoticePaidCancel = key("admin.notice.paidcancel", Message{
		ZhHant: "這筆訂單已付款，不能直接取消。請使用「出貨前退款並取消」。",
		En:     "This order is paid and cannot be cancelled directly. Use Refund and cancel before shipment.",
	})

	KeyAdminNoticeRefundShipped = key("admin.notice.refundshipped", Message{
		ZhHant: "這張訂單已經出貨，不能退款取消。請到退貨頁，用退貨處理。",
		En:     "This order has already shipped, so it cannot be refunded and cancelled here. Handle it as a return on the Returns page.",
	})

	KeyAdminNoticeRefundHasReturn = key("admin.notice.refundhasreturn", Message{
		ZhHant: "這張訂單已有退貨申請，不能再退款取消。請到退貨頁，處理那筆退貨。",
		En:     "This order already has a return, so it cannot be refunded and cancelled here. Handle that return on the Returns page.",
	})

	KeyAdminNoticeRefundCancelled = key("admin.notice.refundcancelled", Message{
		ZhHant: "這張訂單已經取消，這次沒有退款。請重新整理，在訂單紀錄確認款項有沒有退回顧客。",
		En:     "This order is already cancelled, so nothing was refunded this time. Reload the page and check Order history to see whether the customer was paid back.",
	})

	KeyAdminNoticeRefundUnpaid = key("admin.notice.refundunpaid", Message{
		ZhHant: "這張訂單還沒有付款，沒有款項可以退。要取消這張訂單，請用上方的狀態選單。",
		En:     "This order has not been paid, so there is nothing to refund. To cancel it, use the status menu above.",
	})

	KeyAdminNoticeRefundChanged = key("admin.notice.refundchanged", Message{
		ZhHant: "這張訂單剛被改過，這次沒有退款。請重新整理，看目前的狀態再決定。",
		En:     "This order was just changed by someone else, so nothing was refunded. Reload the page and decide from its current state.",
	})

	KeyAdminNoticeRefundPicking = key("admin.notice.refundpicking", Message{
		ZhHant: "這張訂單剛開始備貨，這次沒有取消。請重新整理，再按一次「出貨前退款並取消」。",
		En:     "Packing has just started on this order, so it was not cancelled. Reload the page and press Refund and cancel before shipment again.",
	})

	KeyAdminNoticeRefundMismatch = key("admin.notice.refundmismatch", Message{
		ZhHant: "這筆退款沒辦法照紀錄完成：金額和付款紀錄對不上，或顧客的帳號已刪除。請先到 Stripe 後台確認款項有沒有退出，再聯絡負責系統的人；先不要再按。",
		En:     "This refund cannot be completed as recorded: its amounts do not match the payment records, or the customer's account was deleted. Check the Stripe dashboard to see whether any money went out, then contact whoever runs the system. Do not press it again yet.",
	})

	KeyAdminNoticeRefundUnsure = key("admin.notice.refundunsure", Message{
		ZhHant: "退款沒有完成，目前不確定款項有沒有退出。請重新整理，到訂單紀錄和 Stripe 後台確認；仍然不行，請聯絡負責系統的人。",
		En:     "The refund did not finish, and it is not certain whether any money went out. Reload the page and check Order history and the Stripe dashboard; if it still fails, contact whoever runs the system.",
	})

	KeyAdminNoticeInspected = key("admin.notice.inspected", Message{
		ZhHant: "驗貨已記錄，可再販售的數量已經入庫。",
		En:     "Inspection recorded. Whatever is sellable again is back on the shelf.",
	})

	KeyAdminNoticeClosed = key("admin.notice.closed", Message{ZhHant: "退貨已結案。", En: "Return closed."})

	KeyAdminNoticeBadCount = key("admin.notice.badcount", Message{
		ZhHant: "數量填寫有問題：入庫數不能超過實際收到的數量，實際收到也不能超過申請退回的數量。",
		En: "Those quantities do not work: what goes back on the shelf cannot exceed what arrived, " +
			"and what arrived cannot exceed what the customer asked to return.",
	})

	KeyAdminNoticeAssessed = key("admin.notice.assessed", Message{
		ZhHant: "資格評估已記錄。核准與拒絕會凍結這個版本。",
		En:     "Eligibility assessment recorded. Approving or declining will freeze this version.",
	})
)

var (
	KeyAdminRetConfirmApprove     = key("admin.ret.confirm.approve", Message{ZhHant: "確認同意並退款", En: "Confirm approval and refund"})
	KeyAdminRetConfirmReject      = key("admin.ret.confirm.reject", Message{ZhHant: "確認拒絕退貨", En: "Confirm rejection"})
	KeyAdminRetConfirmException   = key("admin.ret.confirm.exception", Message{ZhHant: "確認例外同意並退款", En: "Confirm exception and refund"})
	KeyAdminRetConfirmRetry       = key("admin.ret.confirm.retry", Message{ZhHant: "確認重試退款", En: "Confirm refund retry"})
	KeyAdminRetConfirmOrder       = key("admin.ret.confirm.order", Message{ZhHant: "訂單", En: "Order"})
	KeyAdminRetConfirmReason      = key("admin.ret.confirm.reason", Message{ZhHant: "顧客退貨原因", En: "Customer return reason"})
	KeyAdminRetConfirmAmount      = key("admin.ret.confirm.amount", Message{ZhHant: "本次退貨金額", En: "Return amount"})
	KeyAdminRefundConfirmAmount   = key("admin.refund.confirm.amount", Message{ZhHant: "取消退款金額", En: "Refund on cancellation"})
	KeyAdminRetConfirmBack        = key("admin.ret.confirm.back", Message{ZhHant: "返回退貨清單", En: "Back to returns"})
	KeyAdminRetErrRejectionReason = key("admin.ret.err.rejectionreason", Message{ZhHant: "請填寫拒絕退貨的原因。", En: "Enter a reason for rejecting this return."})
)

var (
	KeyAdminRefundTitle     = key("admin.refund.title", Message{ZhHant: "出貨前退款並取消", En: "Refund and cancel before shipment"})
	KeyAdminRefundResume    = key("admin.refund.resume", Message{ZhHant: "繼續退款", En: "Resume the refund"})
	KeyAdminRefundErrReason = key("admin.refund.err.reason", Message{ZhHant: "請填寫退款原因，最多 300 字。", En: "Enter a reason for the refund, 300 characters at most."})

	KeyAdminRefundReasonHint = key("admin.refund.reason.hint", Message{
		ZhHant: "寫下顧客如何要求或同意取消，例如電話或 Email；這段說明是作廢發票的同意紀錄。",
		En: "Say how the customer asked for or agreed to the cancellation, such as by phone or email; " +
			"this note is the record of their consent to voiding the invoice.",
	})

	KeyAdminRefundHint = key("admin.refund.hint", Message{
		ZhHant: "已付款的訂單不能直接取消。這會全額退回信用卡與購物金並收回點數；退款入帳、發票作廢或折讓後，訂單才會取消並釋出庫存。",
		En: "A paid order is not cancelled directly. This refunds the card and store credit in full and " +
			"takes back the points; the order is cancelled and its stock released once the refund has " +
			"landed and the invoice is voided or credited.",
	})

	KeyAdminRefundCreditHint = key("admin.refund.credit.hint", Message{
		ZhHant: "這筆訂單以購物金全額付款，還沒開始備貨。確認後訂單立即取消，購物金退回顧客的餘額，保留的庫存釋出，發票排入作廢。",
		En: "Store credit paid this order in full and packing has not started. Confirming cancels it at once, " +
			"returns the store credit to the customer's balance, releases its stock and queues its invoice to be voided.",
	})

	KeyAdminRefundCreditReturn = key("admin.refund.credit.return", Message{
		ZhHant: "購物金 %s 退回顧客的餘額，訂單立即取消，發票排入作廢。",
		En:     "Store credit of %s goes back to the customer's balance, the order is cancelled at once and its invoice is queued to be voided.",
	})

	KeyAdminRefundCreditCancel = key("admin.refund.credit.cancel", Message{ZhHant: "取消訂單並退回購物金", En: "Cancel the order and return the store credit"})

	KeyAdminRefundOpen = key("admin.refund.open", Message{
		ZhHant: "這筆訂單正在出貨前退款，已不能出貨。退款入帳、發票作廢或折讓後，按「繼續退款」取消訂單。",
		En: "This order is being refunded before shipment and can no longer ship. Once the refund has " +
			"landed and the invoice is voided or credited, press Resume the refund to cancel it.",
	})
)
