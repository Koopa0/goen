package i18n

var (
	KeyMailOrderCancelledSubject            = key("mail.order.cancelled.subject", Message{ZhHant: "訂單 %s 已取消", En: "Order %s cancelled"})
	KeyMailOrderCustomerCancelledBody       = key("mail.order.cancelled.customer.body", Message{ZhHant: "你已取消訂單 %s。這筆訂單尚未收款；若有使用購物金，已退回你的購物金餘額。查看訂單：\n%s", En: "You cancelled order %s. It was not charged, and any store credit you applied has been returned to your store credit balance. View your order:\n%s"})
	KeyMailOrderDeadlineCancelledBody       = key("mail.order.cancelled.deadline.body", Message{ZhHant: "訂單 %s 在保留時間內沒有完成付款，已自動取消。這筆訂單沒有收取任何款項；若有使用購物金，已退回你的購物金餘額。查看訂單：\n%s", En: "Order %s was not paid while its stock was held, so it has been cancelled automatically. Nothing was charged, and any store credit you applied has been returned to your store credit balance. View your order:\n%s"})
	KeyMailOrderCustomerCancelledRefundBody = key("mail.order.cancelled.customer.refund.body", Message{ZhHant: "你已取消訂單 %s。若有款項已經到帳，已經或將會全額退還給你；若有使用購物金，已退回你的購物金餘額。查看訂單：\n%s", En: "You cancelled order %s. If a payment reached us, it has been or will be refunded in full, and any store credit you applied has been returned to your store credit balance. View your order:\n%s"})
	KeyMailOrderDeadlineCancelledRefundBody = key("mail.order.cancelled.deadline.refund.body", Message{ZhHant: "訂單 %s 在保留時間內沒有完成付款，已自動取消。若有款項在期限後才到帳，已經或將會全額退還給你；若有使用購物金，已退回你的購物金餘額。查看訂單：\n%s", En: "Order %s was not paid while its stock was held, so it has been cancelled automatically. If a payment reached us after the deadline, it has been or will be refunded in full, and any store credit you applied has been returned to your store credit balance. View your order:\n%s"})
	KeyMailOrderStaffCancelledBody          = key("mail.order.cancelled.staff.body", Message{ZhHant: "商店已取消訂單 %s。請至訂單頁查看付款及退款狀態：\n%s", En: "The shop cancelled order %s. View its payment and refund status on the order page:\n%s"})
	KeyMailOrderStaffCancelledRefundBody    = key("mail.order.cancelled.staff.refund.body", Message{ZhHant: "很抱歉，我們取消了你的訂單 %s。若有款項已經到帳，已經或將會全額退還給你；若有使用購物金，也已退回你的購物金餘額。\n\n查看訂單：\n%s", En: "We are sorry: we have cancelled your order %s. If a payment reached us, it has been or will be refunded in full, and any store credit you applied has been returned to your store credit balance.\n\nView your order:\n%s"})
	KeyMailOrderDeliveredSubject            = key("mail.order.delivered.subject", Message{ZhHant: "訂單 %s 已送達", En: "Order %s has been delivered"})
	KeyMailOrderCollectedSubject            = key("mail.order.collected.subject", Message{ZhHant: "訂單 %s 已取貨", En: "Order %s has been collected"})
	KeyMailOrderDeliveredBody               = key("mail.order.delivered.body", Message{ZhHant: "訂單 %s 已標記為送達。查看訂單：\n%s", En: "Order %s has been marked as delivered. View your order:\n%s"})
	KeyMailRescissionEnds                   = key("mail.order.rescissionends", Message{ZhHant: "依消費者保護法，你可自收到商品的次日起七日內退貨，最後一日為 %s。", En: "Under Taiwan's Consumer Protection Act you may return what you bought within seven days, counted from the day after you receive the goods; the last day is %s."})
	KeyMailOrderCollectedBody               = key("mail.order.collected.body", Message{ZhHant: "訂單 %s 已標記為取貨完成。查看訂單：\n%s", En: "Order %s has been marked as collected. View your order:\n%s"})
)

// The letter for units of a partly shipped order that cannot be sent and have
// been refunded (open_unsent_refund). The wording is the owner's (#1965); the
// refund sentence names only the sources that moved money, so a card-only
// refund does not tell the customer NT$0 went to a balance.
var (
	KeyMailOrderPartRefundedSubject = key("mail.order.partrefunded.subject", Message{
		ZhHant: "訂單 %s：部分商品無法寄出，已退款",
		En:     "Order %s: a refund for items we cannot send",
	})
	// %[1]s the order number, %[2]s the items, %[3]s the refund sentence (or
	// nothing when nothing was paid for them), %[4]s the order page.
	KeyMailOrderPartRefundedBody = key("mail.order.partrefunded.body", Message{
		ZhHant: "很抱歉，你訂單 %[1]s 中的 %[2]s 無法寄出。%[3]s其他商品照常出貨。若少了這件商品，其他商品對你已沒有用處，收到後七天內可以免費退貨，原付運費也會一併退還。\n\n查看訂單：\n%[4]s",
		En:     "Sorry — %[2]s from order %[1]s cannot be sent. %[3]sThe rest of your order ships as normal. If the rest is no use to you without it, you can return it free within seven days of receiving it, and the delivery fee comes back too.\n\nView your order:\n%[4]s",
	})
	// %[1]s the refund, %[2]s the card's part, %[3]s the store credit's part.
	KeyMailOrderPartRefundedCardAndCredit = key("mail.order.partrefunded.cardandcredit", Message{
		ZhHant: "這部分的款項 %[1]s 已退還：信用卡 %[2]s（入帳時間由發卡銀行決定，通常需數個工作天），購物金 %[3]s 已回到你的餘額。",
		En:     "We have refunded %[1]s for it: %[2]s to your card (your issuer decides when it lands, usually a few working days) and %[3]s to your store credit. ",
	})
	// %[1]s the refund, %[2]s the card's part: the same amount.
	KeyMailOrderPartRefundedCard = key("mail.order.partrefunded.card", Message{
		ZhHant: "這部分的款項 %[1]s 已退還：信用卡 %[2]s（入帳時間由發卡銀行決定，通常需數個工作天）。",
		En:     "We have refunded %[1]s for it: %[2]s to your card (your issuer decides when it lands, usually a few working days). ",
	})
	// %[1]s the refund, %[2]s the store credit's part: the same amount.
	KeyMailOrderPartRefundedCredit = key("mail.order.partrefunded.credit", Message{
		ZhHant: "這部分的款項 %[1]s 已退還：購物金 %[2]s 已回到你的餘額。",
		En:     "We have refunded %[1]s for it: %[2]s to your store credit. ",
	})
)
