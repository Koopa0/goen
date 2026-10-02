package i18n

// The back-office order page's payment and refund section.
var (
	KeyAdminPayTitle = key("admin.pay.title", Message{ZhHant: "付款與退款", En: "Payment and refunds"})

	KeyAdminPayMethodCard = key("admin.pay.method.card", Message{ZhHant: "信用卡(Stripe)", En: "Card (Stripe)"})

	KeyAdminPayMethodCredit = key("admin.pay.method.credit", Message{ZhHant: "購物金全額折抵", En: "Paid in full with store credit"})

	KeyAdminPayMethodFree = key("admin.pay.method.free", Message{ZhHant: "全額折扣，無須付款", En: "Nothing to pay after the discount"})

	KeyAdminPayNone = key("admin.pay.none", Message{ZhHant: "尚未收款", En: "No payment received yet"})

	KeyAdminPayReceived = key("admin.pay.received", Message{ZhHant: "已收款 %s · %s", En: "Received %s · %s"})

	KeyAdminPayRefundCard = key("admin.pay.refund.card", Message{ZhHant: "退回信用卡", En: "Refunded to card"})

	KeyAdminPayRefundCredit = key("admin.pay.refund.credit", Message{ZhHant: "退回商店額度", En: "Refunded to store credit"})

	KeyAdminPayReason = key("admin.pay.reason", Message{ZhHant: "原因:%s", En: "Reason: %s"})

	KeyAdminPayBy = key("admin.pay.by", Message{ZhHant: "經手:%s", En: "By %s"})
)
