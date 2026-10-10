package i18n

var (
	KeyPolicyPaymentHold = key("policy.payment.hold", Message{
		ZhHant: "下單後請在 %[2]s 分鐘內開始付款，並在 %[1]s 分鐘內完成。逾時未付款的訂單會自動取消：商品回到架上，不會收取任何款項，使用的購物金也會退回。",
		En:     "Start the payment within %[2]s minutes of ordering and finish it within %[1]s. An order still unpaid after that is cancelled automatically: the goods go back on the shelf, nothing is charged, and any store credit you applied is returned.",
	})

	KeyPolicyWarrantyRegistration = key("policy.warranty.registration", Message{
		ZhHant: "登入後，在該商品的訂單頁登錄保固；登錄後送修時不需要再找收據。",
		En:     "Sign in and register the unit from the order it came on — once it is registered you will not need the receipt to claim.",
	})

	KeyPolicyEyebrow = key("policy.eyebrow", Message{ZhHant: "政策", En: "Policy"})

	KeyPolicyHelp = key("policy.help", Message{ZhHant: "說明", En: "Help"})

	KeyPolicyMore = key("policy.more", Message{ZhHant: "還有問題？", En: "Still have a question? "})

	KeyPolicyMoreFAQ = key("policy.more.faq", Message{ZhHant: "，或看看", En: ", or read the "})

	KeyPolicyMoreEnd = key("policy.more.end", Message{ZhHant: "。", En: "."})

	KeyPolicyContactLink = key("policy.contact", Message{ZhHant: "聯絡我們", En: "Get in touch"})

	KeyPolicyFAQLink = key("policy.faq", Message{ZhHant: "常見問題", En: "FAQ"})
)
