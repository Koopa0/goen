package i18n

// The refund of units a partly shipped order will not send (#1965): the order
// page's section, its confirmation, its notices and the returns queue's word.
var (
	KeyAdminUnsentTitle = key("admin.unsent.title", Message{ZhHant: "退還無法寄出的商品", En: "Refund items that cannot be sent"})

	KeyAdminUnsentHint = key("admin.unsent.hint", Message{
		ZhHant: "已寄出包裹的訂單若有商品無法寄出，這會依原付款方式退還那些商品的款項（扣除分攤的折扣，不含運費），並收回對應的點數；款項退回後，這些商品保留的庫存會釋出或報廢，其他商品照常出貨。",
		En: "When an order that has sent a parcel has items that cannot be sent, this refunds those items the way " +
			"the customer paid (less their share of any discount, without delivery) and takes back their points; once " +
			"the money is back, the stock held for them is released or written off, and the rest ships as normal.",
	})

	// %[1]s the line, %[2]s how many of it have not been sent.
	KeyAdminUnsentQty = key("admin.unsent.qty", Message{ZhHant: "%[1]s：要退款的數量（尚未寄出 %[2]s）", En: "%[1]s: units to refund (%[2]s not sent)"})

	KeyAdminUnsentReason       = key("admin.unsent.reason", Message{ZhHant: "無法寄出的原因", En: "Why it cannot be sent"})
	KeyAdminUnsentReasonChoose = key("admin.unsent.reason.choose", Message{ZhHant: "請選擇原因", En: "Choose a reason"})

	KeyUnsentReasonDamaged          = key("unsent.reason.damaged", Message{ZhHant: "商品損壞", En: "Damaged"})
	KeyUnsentReasonLost             = key("unsent.reason.lost", Message{ZhHant: "商品遺失", En: "Lost"})
	KeyUnsentReasonSupplierFailed   = key("unsent.reason.supplierfailed", Message{ZhHant: "供應商無法供貨", En: "The supplier could not supply it"})
	KeyUnsentReasonCustomerWithdrew = key("unsent.reason.customerwithdrew", Message{ZhHant: "顧客不要這件商品了", En: "The customer withdrew it"})

	// %s the units of the refund, as a list.
	KeyAdminUnsentOpen = key("admin.unsent.open", Message{
		ZhHant: "這筆訂單有一筆未寄出商品的退款還沒完成：%s。款項退回後，按「繼續退款」釋出或報廢保留的庫存並結案。",
		En:     "A refund of unsent items is unfinished on this order: %s. Once the money is back, press Resume the refund to release or write off the stock held for them and close it.",
	})

	KeyAdminUnsentConfirmAmount = key("admin.unsent.confirm.amount", Message{ZhHant: "未寄出商品退款金額", En: "Refund for unsent items"})
	KeyAdminUnsentConfirmItems  = key("admin.unsent.confirm.items", Message{ZhHant: "退款商品", En: "Items refunded"})

	KeyAdminUnsentStockWrittenOff = key("admin.unsent.stock.writtenoff", Message{
		ZhHant: "這些商品視為報廢，不會回到可售庫存。",
		En:     "These units are written off and do not go back on sale.",
	})
	KeyAdminUnsentStockBack = key("admin.unsent.stock.back", Message{
		ZhHant: "這些商品回到可售庫存。",
		En:     "These units go back on sale.",
	})
	KeyAdminUnsentDeliveryKept = key("admin.unsent.delivery", Message{
		ZhHant: "運費不退：已有包裹寄出。顧客之後若退回其餘商品，運費會隨那筆退貨退還。",
		En:     "Delivery is not refunded: a parcel has gone out. If the customer later returns the rest, delivery is refunded with that return.",
	})

	KeyAdminUnsentNoteHint = key("admin.unsent.note.hint", Message{
		ZhHant: "寫下商品為什麼無法寄出，以及顧客如何得知，例如電話或 Email。這段說明留在退貨與稽核紀錄，不會寄給顧客。",
		En: "Say why the items cannot be sent and how the customer was told, such as by phone or email. " +
			"The note stays in the return and audit records; it is not sent to the customer.",
	})

	KeyAdminNoticeUnsentRefunded = key("admin.notice.unsentrefunded", Message{
		ZhHant: "已退還無法寄出商品的款項，保留的庫存已釋出或報廢，並已寄信通知顧客。",
		En:     "The unsent items are refunded, the stock held for them is released or written off, and the customer has been e-mailed.",
	})
	KeyAdminNoticeUnsentAllowance = key("admin.notice.unsentallowance", Message{
		ZhHant: "款項已退還並結案，但折讓沒有開立成功。請在下方發票區塊開立折讓。",
		En:     "The refund went through and is closed, but the allowance could not be filed. File it in the invoice section below.",
	})
	KeyAdminNoticeUnsentDiffers = key("admin.notice.unsentdiffers", Message{
		ZhHant: "這筆訂單已有一筆未寄出商品的退款還沒完成，內容和這次填的不同，這次沒有退款。請重新整理，先按「繼續退款」完成那一筆。",
		En:     "An unfinished refund of unsent items on this order names other items, so nothing was refunded this time. Reload the page and press Resume the refund to finish that one first.",
	})
	KeyAdminNoticeUnsentBounds = key("admin.notice.unsentbounds", Message{
		ZhHant: "數量填寫有問題：至少選一件，每一項也不能超過還沒寄出的數量。這次沒有退款。",
		En:     "Those quantities do not work: choose at least one unit, and no line can exceed what has not been sent. Nothing was refunded.",
	})
	KeyAdminNoticeUnsentReason = key("admin.notice.unsentreason", Message{
		ZhHant: "請選擇商品無法寄出的原因。這次沒有退款。",
		En:     "Choose why the items cannot be sent. Nothing was refunded.",
	})
	KeyAdminNoticeUnsentIneligible = key("admin.notice.unsentineligible", Message{
		ZhHant: "這筆訂單現在不能退還未寄出的商品：它必須已付款、至少寄出一個包裹，而且還沒完成或取消。這次沒有退款，請重新整理。",
		En:     "This order cannot refund unsent items now: it must be paid, have sent at least one parcel, and be neither completed nor cancelled. Nothing was refunded; reload the page.",
	})
	KeyAdminNoticeUnsentUndecided = key("admin.notice.unsentundecided", Message{
		ZhHant: "這筆訂單有一件退貨申請還沒決定。請先到退貨頁處理，再退還未寄出的商品。這次沒有退款。",
		En:     "This order has a return request nobody has decided yet. Decide it on the Returns page first, then refund the unsent items. Nothing was refunded.",
	})
	KeyAdminNoticeUnsentHold = key("admin.notice.unsenthold", Message{
		ZhHant: "款項已退還，但保留的庫存和這筆退款對不上，所以還沒結案。請聯絡負責系統的人；先不要再按。",
		En:     "The money is back, but the stock held for this order does not match the refund, so it is not closed yet. Contact whoever runs the system, and do not press it again yet.",
	})

	KeyAdminOrderNextUnsent = key("admin.order.next.unsent", Message{
		ZhHant: "未寄出商品的退款還沒完成，請在退款區按「繼續退款」。",
		En:     "The refund of unsent items is unfinished; choose “Resume the refund” in its section.",
	})

	KeyAdminReturnUnsentRefunded = key("admin.return.unsentrefunded", Message{ZhHant: "未寄出，已退款", En: "Not sent, refunded"})
)
