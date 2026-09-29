package i18n

var (
	KeyMailOrderCancelledSubject      = key("mail.order.cancelled.subject", Message{ZhHant: "訂單 %s 已取消", En: "Order %s cancelled"})
	KeyMailOrderCustomerCancelledBody = key("mail.order.cancelled.customer.body", Message{ZhHant: "您已取消訂單 %s。這筆訂單尚未收款,您使用的購物金已退回帳戶。查看訂單:%s", En: "You cancelled order %s. It was not charged, and any store credit you applied has been returned to your account. View your order: %s"})
	KeyMailOrderStaffCancelledBody    = key("mail.order.cancelled.staff.body", Message{ZhHant: "商店已取消訂單 %s。請至訂單頁查看付款及退款狀態：%s", En: "The shop cancelled order %s. View its payment and refund status on the order page: %s"})
	KeyMailOrderArrivedSubject        = key("mail.order.arrived.subject", Message{ZhHant: "訂單 %s 收貨狀態更新", En: "Receipt update for order %s"})
	KeyMailOrderDeliveredBody         = key("mail.order.delivered.body", Message{ZhHant: "訂單 %s 已標記為送達。查看訂單：%s", En: "Order %s has been marked as delivered. View your order: %s"})
	KeyMailOrderCollectedBody         = key("mail.order.collected.body", Message{ZhHant: "訂單 %s 已標記為取貨完成。查看訂單：%s", En: "Order %s has been marked as collected. View your order: %s"})
)
