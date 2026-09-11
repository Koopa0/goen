package i18n

// Transactional email. Each body is one message with %s holes, so the shape of
// a letter stays with its words.

var (
	KeyMailGreeting = key("mail.greeting", Message{ZhHant: "%s 您好,", En: "Hello %s,"})
	KeyMailNoReply  = key("mail.noreply", Message{
		ZhHant: "這封信是系統自動發送的,請勿直接回覆。",
		En:     "This message was sent automatically. Please do not reply to it.",
	})

	KeyMailPlacedSubject = key("mail.placed.subject", Message{
		ZhHant: "訂單 %s 已成立",
		En:     "Order %s received",
	})
	KeyMailPlacedBody = key("mail.placed.body", Message{
		ZhHant: "我們已經收到您的訂單 %s。\n\n應付金額:%s\n\n查看訂單與付款:\n%s",
		En:     "We have your order %s.\n\nAmount due: %s\n\nView it and pay:\n%s",
	})
	// KeyMailPlacedFundedBody is the confirmation when store credit or a
	// coupon has already brought the amount owed to zero. The pay CTA would
	// send the customer to a page that has nothing to collect.
	KeyMailPlacedFundedBody = key("mail.placed.funded.body", Message{
		ZhHant: "我們已經收到您的訂單 %s。\n\n查看訂單:\n%s",
		En:     "We have your order %s.\n\nView the order:\n%s",
	})

	// KeyMailStatutoryDisclosure carries Consumer Protection Act §18's disclosure.
	// Under §19 III the seven-day window runs from the day after it is finally
	// provided and survives four months, so an order sent without it carries that tail.
	KeyMailStatutoryDisclosure = key("mail.disclosure", Message{
		ZhHant: "───────────────\n" +
			"依消費者保護法第 18 條應告知事項\n\n" +
			"賣方:%s\n" +
			"聯絡方式:%s\n\n" +
			"解除契約(鑑賞期):您可於收受商品之次日起七日內,以退回商品或書面通知的方式解除契約," +
			"無須說明理由,也不負擔任何費用。在期限內交運商品或發出通知即生效力。\n" +
			"行使方式:於訂單頁面申請退貨,或以上述聯絡方式通知我們。\n\n" +
			"排除解除權之商品:本店目前沒有任何商品排除七日解除權。\n\n" +
			"消費申訴:請以上述聯絡方式與我們聯繫;亦可向消費者保護團體、" +
			"直轄市或縣(市)政府消費者服務中心申訴。",
		En: "───────────────\n" +
			"Information required by Article 18 of Taiwan's Consumer Protection Act\n\n" +
			"Seller: %s\n" +
			"Contact: %s\n\n" +
			"Cancelling (the seven-day right): you may cancel within seven days, " +
			"counted from the day AFTER the goods reach you, by returning them or by " +
			"telling us in writing. You need give no reason and it costs you nothing. " +
			"Sending the goods or the notice inside those seven days is enough.\n" +
			"How: request a return on your order page, or contact us at the address above.\n\n" +
			"Goods excluded from the right: none in this shop.\n\n" +
			"Complaints: contact us at the address above. You may also complain to a " +
			"consumer protection group or to your local government's consumer service centre.",
	})

	KeyMailPaidSubject = key("mail.paid.subject", Message{
		ZhHant: "訂單 %s 付款完成",
		En:     "Payment received for order %s",
	})
	KeyMailPaidBody = key("mail.paid.body", Message{
		ZhHant: "我們已經收到您訂單 %s 的付款。\n\n付款金額:%s\n\n我們會盡快安排出貨,出貨時會再寄一封通知信給您。\n\n查看訂單:\n%s",
		En: "We have received payment for order %s.\n\nAmount paid: %s\n\nWe will pack it " +
			"shortly, and send another note when it ships.\n\nView the order:\n%s",
	})

	KeyMailShippedSubject = key("mail.shipped.subject", Message{
		ZhHant: "訂單 %s 已出貨",
		En:     "Order %s has shipped",
	})
	KeyMailShippedBody = key("mail.shipped.body", Message{
		ZhHant: "您的訂單 %s 已經出貨了。\n\n物流:%s\n查詢編號:%s\n\n查看訂單:\n%s",
		En:     "Your order %s is on its way.\n\nCarrier: %s\nTracking number: %s\n\nView the order:\n%s",
	})

	// KeyMailRestockSubject heads the restock notice; goen reserves no stock for one.
	KeyMailRestockSubject = key("mail.restock.subject", Message{
		ZhHant: "「%s」補貨通知",
		En:     "%s is back in stock",
	})
	// The second hole is the queued SKU. A multi-variant product would
	// otherwise only name the parent, and the recipient could not tell
	// which requested variant is back.
	KeyMailRestockBody = key("mail.restock.body", Message{
		ZhHant: "您關注的商品「%s」（%s）補貨了。\n\n%s\n\n數量有限,先買到的先出貨 —— 這封通知不會為您保留庫存。",
		En: "%s (%s), which you asked about, is back in stock.\n\n%s\n\nQuantities are limited " +
			"and it is first come, first served — this notice does not reserve one for you.",
	})
	KeyMailPaidCard = key("mail.paid.card", Message{ZhHant: "付款方式:%s", En: "Paid with: %s"})
	KeyMailHello    = key("mail.hello", Message{ZhHant: "您好,", En: "Hello,"})

	KeyMailResetSubject = key("mail.reset.subject", Message{
		ZhHant: "重設 goen 的密碼",
		En:     "Reset your goen password",
	})
	KeyMailResetBody = key("mail.reset.body", Message{
		ZhHant: "你要求重設 goen 的密碼。\n\n點下面的連結設定新密碼,一小時內有效:\n%s\n\n設定完成後,其他裝置上的登入都會結束 —— 包含目前正在使用的。\n\n如果這不是你要求的,不用理會這封信,密碼不會有任何改變。",
		En: "You asked to reset your goen password.\n\nFollow this link to set a new one. " +
			"It works for one hour:\n%s\n\nSetting it will end every other signed-in " +
			"session, including the one you are using now.\n\nIf you did not ask for this, " +
			"ignore this message — nothing about your password changes.",
	})

	KeyMailNewsConfirmSubject = key("mail.news.confirm.subject", Message{
		ZhHant: "確認訂閱 goen 電子報",
		En:     "Confirm your goen newsletter subscription",
	})
	KeyMailNewsConfirmBody = key("mail.news.confirm.body", Message{
		ZhHant: "有人用這個信箱訂閱了 goen 電子報。\n\n如果是您,請點下面的連結完成訂閱,兩天內有效:\n%s\n\n如果不是您,不用理會這封信 —— 沒有點下連結,這個信箱就不會收到電子報。",
		En: "Somebody used this address to subscribe to the goen newsletter.\n\nIf that was " +
			"you, follow this link to finish. It works for two days:\n%s\n\nIf it was not, " +
			"ignore this message — without that link, this address gets nothing.",
	})
	KeyMailNewsWelcomeSubject = key("mail.news.welcome.subject", Message{
		ZhHant: "已訂閱 goen 電子報",
		En:     "You are subscribed to the goen newsletter",
	})
	KeyMailNewsWelcomeBody = key("mail.news.welcome.body", Message{
		ZhHant: "訂閱完成,謝謝您。\n\n每月一封,新品與比價重點,不灌水。\n\n任何時候想退訂,用這個連結:\n%s\n\n這個連結不會過期,請留著這封信。每一封電子報的頁尾也都會附上。",
		En: "Subscribed — thank you.\n\nOne letter a month: new arrivals and what is worth " +
			"comparing, nothing padded.\n\nTo leave at any time, use this link:\n%s\n\nIt " +
			"does not expire, so keep this message. Every newsletter carries it at the foot " +
			"as well.",
	})

	// KeyMailIssueFooter is the one translated part of an issue; the shop authors the rest.
	KeyMailIssueFooter = key("mail.issue.footer", Message{
		ZhHant: "不想再收到電子報?用這個連結退訂:\n%s",
		En:     "Not interested any more? Unsubscribe here:\n%s",
	})

	KeyMailVerifySubject = key("mail.verify.subject", Message{
		ZhHant: "確認你的 goen 電子郵件",
		En:     "Confirm your goen email address",
	})
	KeyMailVerifyBody = key("mail.verify.body", Message{
		ZhHant: "請確認 %s 是你的信箱。\n\n點下面的連結完成確認,兩天內有效:\n%s\n\n如果這不是你要求的,不用理會這封信 —— 你的帳號和目前的信箱都不會改變。",
		En: "Please confirm that %s is your address.\n\nFollow this link to finish. It " +
			"works for two days:\n%s\n\nIf you did not ask for this, ignore this " +
			"message — nothing about your account or its current address changes.",
	})
)
