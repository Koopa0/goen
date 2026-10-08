package i18n

// Transactional email. Each body is one message with %s holes, so the shape of
// a letter stays with its words.

var (
	KeyMailGreeting = key("mail.greeting", Message{ZhHant: "%s 你好，", En: "Hello %s,"})
	KeyMailNoReply  = key("mail.noreply", Message{
		ZhHant: "這封信是系統自動發送的，請勿直接回覆。",
		En:     "This message was sent automatically. Please do not reply to it.",
	})

	KeyMailPlacedSubject = key("mail.placed.subject", Message{
		ZhHant: "已收到訂單 %s",
		En:     "Order %s received",
	})
	KeyMailPlacedBody = key("mail.placed.body", Message{
		ZhHant: "我們已經收到你的訂單 %s。還沒付款的話，請盡快完成；商品只保留一段時間，逾時訂單會自動取消。\n\n應付金額：%s\n\n查看訂單與付款：\n%s",
		En:     "We have your order %s. If you have not paid yet, please do so soon: the items are held for a limited time, and an unpaid order is then cancelled automatically.\n\nAmount due: %s\n\nView it and pay:\n%s",
	})
	// KeyMailPlacedFundedBody is the confirmation when store credit or a
	// coupon has already brought the amount owed to zero. The pay CTA would
	// send the customer to a page that has nothing to collect.
	KeyMailPlacedFundedBody = key("mail.placed.funded.body", Message{
		ZhHant: "我們已經收到你的訂單 %s。\n\n查看訂單：\n%s",
		En:     "We have your order %s.\n\nView the order:\n%s",
	})
	KeyMailPlacedReceived = key("mail.placed.received", Message{ZhHant: "我們已經收到你的訂單 %s。", En: "We have your order %s."})
	KeyMailPlacedLines    = key("mail.placed.lines", Message{ZhHant: "下單時的訂單內容：", En: "Order details at placement:"})
	KeyMailPlacedLine     = key("mail.placed.line", Message{ZhHant: "商品編號：%s；%s × %s = %s", En: "SKU: %s; %s × %s = %s"})
	KeyMailPlacedTotals   = key("mail.placed.totals", Message{ZhHant: "下單時的金額：", En: "Amounts at placement:"})
	KeyMailPlacedFact     = key("mail.placed.fact", Message{ZhHant: "%s：%s", En: "%s: %s"})
	KeyMailPlacedNote     = key("mail.placed.note", Message{ZhHant: "備註", En: "Your note"})
	KeyMailPlacedDelivery = key("mail.placed.delivery", Message{ZhHant: "下單時的配送資料：", En: "Delivery details at placement:"})
	KeyMailPlacedFunding  = key("mail.placed.funding", Message{ZhHant: "下單時的付款方式：", En: "Payment arrangement at placement:"})
	KeyMailPlacedCard     = key("mail.placed.card", Message{ZhHant: "信用卡：尚須支付 %s。", En: "Credit card: %s remains payable."})
	KeyMailPlacedMixed    = key("mail.placed.mixed", Message{ZhHant: "購物金已折抵 %s；信用卡尚須支付 %s。", En: "Store credit applied: %s. Credit card: %s remains payable."})
	KeyMailPlacedCredit   = key("mail.placed.credit", Message{ZhHant: "已使用購物金支付 %s。", En: "Paid with store credit: %s."})
	KeyMailPlacedZero     = key("mail.placed.zero", Message{ZhHant: "無須付款。", En: "No payment required."})
	KeyMailPlacedDeadline = key("mail.placed.deadline", Message{ZhHant: "請在 %s 前開始付款。商品保留到 %s，逾時未付款，會自動取消訂單。時間以台灣時間為準。", En: "Start paying by %s. Your items are reserved until %s and the order is cancelled if it is still unpaid then. Times are Taiwan time."})
	KeyMailPlacedExpired  = key("mail.placed.expired", Message{ZhHant: "開始付款的期限已過，請先查看訂單的目前狀態，再確認能否付款。原開始付款期限：%s；原商品保留期限：%s。時間以台灣時間為準。", En: "The deadline to start paying has passed. Check the order's current status before attempting payment. Original deadline to start paying: %s; your items were reserved until %s. Times are Taiwan time."})
	KeyMailPlacedCurrent  = key("mail.placed.current", Message{ZhHant: "查看訂單的目前狀態：\n%s", En: "Check the order's current status:\n%s"})

	// KeyMailStatutoryDisclosure carries Consumer Protection Act §18's disclosure.
	// Under §19 III the seven-day window runs from the day after it is finally
	// provided and survives four months, so an order sent without it carries that tail.
	KeyMailStatutoryDisclosure = key("mail.disclosure", Message{
		ZhHant: "───────────────\n" +
			"依消費者保護法第 18 條應告知事項\n\n" +
			"賣方：%s\n" +
			"聯絡方式：%s\n\n" +
			"解除契約（猶豫期）：你可自收到商品的次日起七日內，以退回商品或書面通知的方式解除契約，" +
			"無須說明理由，也不負擔任何費用。在期限內交運商品或發出通知即生效力。\n" +
			"行使方式：於訂單頁面申請退貨，或以上述聯絡方式通知我們。\n\n" +
			"排除解除權之商品：本店目前沒有任何商品排除七日解除權。\n\n" +
			"消費申訴：請以上述聯絡方式與我們聯繫；亦可向消費者保護團體、" +
			"直轄市或縣（市）政府消費者服務中心申訴。",
		En: "───────────────\n" +
			"Information required by Article 18 of Taiwan's Consumer Protection Act\n\n" +
			"Seller: %s\n" +
			"Contact: %s\n\n" +
			"Cancelling (the seven-day right): you may cancel within seven days, " +
			"counted from the day after you receive the goods, by returning them or by " +
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
		ZhHant: "我們已經收到你訂單 %s 的付款。\n\n付款金額：%s\n\n我們會盡快安排出貨，出貨時會再寄一封通知信給你。\n\n查看訂單：\n%s",
		En: "We have received payment for order %s.\n\nAmount paid: %s\n\nWe will pack it " +
			"shortly, and send another note when it ships.\n\nView the order:\n%s",
	})

	KeyMailShippedSubject = key("mail.shipped.subject", Message{
		ZhHant: "訂單 %s 已出貨",
		En:     "Order %s has shipped",
	})
	KeyMailShippedBody = key("mail.shipped.body", Message{
		ZhHant: "你的訂單 %s 已經出貨了。\n\n物流：%s\n查詢編號：%s\n\n查看訂單：\n%s",
		En:     "Your order %s is on its way.\n\nCarrier: %s\nTracking number: %s\n\nView the order:\n%s",
	})

	// KeyMailShippedPickupBody is the dispatch notice for a convenience-store
	// pickup order, which has no address to be "on its way" to.
	KeyMailShippedPickupBody = key("mail.shipped.pickup.body", Message{
		ZhHant: "你的訂單 %s 已經出貨，將送到你選擇的超商門市取貨。\n\n物流：%s\n查詢編號：%s\n\n查看訂單：\n%s",
		En:     "Your order %s has shipped to the convenience store you chose, where you collect it.\n\nCarrier: %s\nTracking number: %s\n\nView the order:\n%s",
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
		ZhHant: "你關注的商品「%s」（%s）補貨了。\n\n%s\n\n數量有限，先買到的先出貨 —— 這封通知不會為你保留庫存。",
		En: "%s (%s), which you asked about, is back in stock.\n\n%s\n\nQuantities are limited " +
			"and it is first come, first served — this notice does not reserve one for you.",
	})
	KeyMailPaidCard = key("mail.paid.card", Message{ZhHant: "付款方式：%s", En: "Paid with: %s"})
	KeyMailHello    = key("mail.hello", Message{ZhHant: "你好，", En: "Hello,"})

	KeyMailResetSubject = key("mail.reset.subject", Message{
		ZhHant: "重設 goen 的密碼",
		En:     "Reset your goen password",
	})
	KeyMailResetBody = key("mail.reset.body", Message{
		ZhHant: "你要求重設 goen 的密碼。\n\n點下面的連結設定新密碼，一小時內有效：\n%s\n\n設定完成後，其他裝置上的登入都會結束 —— 包含目前正在使用的。\n\n如果這不是你要求的，不用理會這封信，密碼不會有任何改變。",
		En: "You asked to reset your goen password.\n\nFollow this link to set a new one. " +
			"It works for one hour:\n%s\n\nSetting it will end every other signed-in " +
			"session, including the one you are using now.\n\nIf you did not ask for this, " +
			"ignore this message — nothing about your password changes.",
	})

	KeyMailAccountExistsSubject = key("mail.exists.subject", Message{
		ZhHant: "你已經有 goen 帳號了",
		En:     "You already have a goen account",
	})
	KeyMailAccountExistsBody = key("mail.exists.body", Message{
		ZhHant: "有人用這個信箱在 goen 註冊，但這個信箱已經有帳號了，所以沒有建立新的帳號。\n\n如果是你，直接登入就可以：\n%s\n\n忘記密碼的話，在這裡重設：\n%s\n\n如果這不是你，不用理會這封信 —— 你的帳號沒有任何改變。",
		En: "Somebody tried to create a goen account with this address. It already has " +
			"one, so no new account was made.\n\nIf that was you, sign in here:\n%s\n\n" +
			"If you have forgotten the password, reset it here:\n%s\n\nIf it was not " +
			"you, ignore this message — nothing about your account has changed.",
	})
	KeyMailAddressInUseSubject = key("mail.inuse.subject", Message{
		ZhHant: "有人要求使用你的 goen 信箱",
		En:     "Somebody asked to use your goen address",
	})
	KeyMailAddressInUseBody = key("mail.inuse.body", Message{
		ZhHant: "有人要求把另一個 goen 帳號的信箱改成這個信箱。這個信箱已經屬於你的帳號，所以沒有任何帳號被改動。\n\n如果是你，直接用這個信箱登入就可以：\n%s\n\n忘記密碼的話，在這裡重設：\n%s\n\n如果這不是你，不用理會這封信 —— 你的帳號沒有任何改變。",
		En: "Somebody asked to move another goen account to this address. It already " +
			"belongs to your account, so no account was changed.\n\nIf that was you, sign " +
			"in with this address here:\n%s\n\nIf you have forgotten the password, reset " +
			"it here:\n%s\n\nIf it was not you, ignore this message — nothing about your " +
			"account has changed.",
	})

	KeyMailNewsConfirmSubject = key("mail.news.confirm.subject", Message{
		ZhHant: "確認訂閱 goen 電子報",
		En:     "Confirm your goen newsletter subscription",
	})
	KeyMailNewsConfirmBody = key("mail.news.confirm.body", Message{
		ZhHant: "有人用這個信箱訂閱了 goen 電子報。\n\n如果是你，請點下面的連結完成訂閱，兩天內有效：\n%s\n\n如果不是你，不用理會這封信 —— 沒有點下連結，這個信箱就不會收到電子報。",
		En: "Somebody used this address to subscribe to the goen newsletter.\n\nIf that was " +
			"you, follow this link to finish. It works for two days:\n%s\n\nIf it was not, " +
			"ignore this message — without that link, this address gets nothing.",
	})
	KeyMailNewsWelcomeSubject = key("mail.news.welcome.subject", Message{
		ZhHant: "已訂閱 goen 電子報",
		En:     "You are subscribed to the goen newsletter",
	})
	KeyMailNewsWelcomeBody = key("mail.news.welcome.body", Message{
		ZhHant: "訂閱完成。\n\n不定期寄送。\n\n任何時候想退訂，用這個連結：\n%s\n\n這個連結不會過期，請留著這封信。每一封電子報的頁尾也都會附上。",
		En: "You are subscribed.\n\nWe send occasionally.\n\nTo leave at any time, use this " +
			"link:\n%s\n\nIt does not expire, so keep this message. Every newsletter " +
			"carries it at the foot as well.",
	})

	// KeyMailIssueFooter is the one translated part of an issue; the shop authors the rest.
	KeyMailIssueFooter = key("mail.issue.footer", Message{
		ZhHant: "不想再收到電子報？用這個連結退訂：\n%s",
		En:     "Not interested any more? Unsubscribe here:\n%s",
	})

	KeyMailRegisterSubject = key("mail.register.subject", Message{
		ZhHant: "完成 goen 註冊",
		En:     "Finish creating your goen account",
	})
	KeyMailRegisterBody = key("mail.register.body", Message{
		ZhHant: "有人用這個信箱在 goen 註冊了帳號。\n\n如果是你，點下面的連結，輸入你註冊時設定的密碼，就完成註冊，兩天內有效：\n%s\n\n如果不是你，不用理會這封信 —— 沒有那組密碼，這個帳號就無法完成註冊。想用這個信箱在 goen 購物，請用「忘記密碼」重新設定一組，帳號就是你的。",
		En: "Somebody registered a goen account with this address.\n\nIf that was you, " +
			"follow this link and enter the password you chose to finish. It works for " +
			"two days:\n%s\n\nIf it was not you, ignore this message — without that " +
			"password the account cannot be finished. To use this address at goen " +
			"yourself, choose a new password on the forgotten-password page and the " +
			"account is yours.",
	})

	KeyMailVerifySubject = key("mail.verify.subject", Message{
		ZhHant: "你要求把 goen 帳號的信箱改成這個嗎？",
		En:     "Did you ask to use this address for your goen account?",
	})
	KeyMailVerifyBody = key("mail.verify.body", Message{
		ZhHant: "有人要求把一個 goen 帳號的信箱改成 %s。\n\n如果是你，請先登入提出要求的那個帳號，再點下面的連結確認，兩天內有效：\n%s\n\n如果不是你，請不要點這個連結，直接忽略這封信 —— 只有提出要求的帳號登入後確認，信箱才會改變；你自己的帳號不會有任何改變。",
		En: "Somebody asked to move a goen account to %s.\n\nIf that was you, sign in to " +
			"that account and follow this link to confirm. It works for two days:\n%s\n\n" +
			"If it was not you, do not follow the link; ignore this message. The address " +
			"moves only when the account that asked confirms it, signed in, and nothing " +
			"about any account of yours changes.",
	})
)
