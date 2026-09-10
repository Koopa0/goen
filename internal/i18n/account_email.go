package i18n

var (
	KeyVerifyTitle = key("verify.title", Message{
		ZhHant: "確認電子郵件",
		En:     "Confirm your email address",
	})

	KeyVerifyBody = key("verify.body", Message{
		ZhHant: "按下按鈕就完成確認。如果這是更換信箱,確認之後才會生效。",
		En: "One button and it is confirmed. If this is a change of address, it takes " +
			"effect only once you do.",
	})

	KeyVerifySubmit = key("verify.submit", Message{ZhHant: "確認", En: "Confirm"})

	KeyVerifyDone = key("verify.done", Message{ZhHant: "信箱已確認", En: "Address confirmed"})

	KeyVerifyDoneBody = key("verify.done.body", Message{
		ZhHant: "%s 已經確認完成,之後的通知信都會寄到這裡。",
		En:     "%s is confirmed. Everything we send you goes there from now on.",
	})

	KeyVerifyDeadTitle = key("verify.dead", Message{
		ZhHant: "這個連結無法使用",
		En:     "That link does not work",
	})

	KeyVerifyDeadBody = key("verify.dead.body", Message{
		ZhHant: "連結可能已經用過或超過兩天。請到會員中心重新寄一次。",
		En: "It may have been used already, or be more than two days old. Ask for another " +
			"from your account page.",
	})

	KeyVerifyTakenTitle = key("verify.taken", Message{
		ZhHant: "這個信箱已經有人使用",
		En:     "That address is already in use",
	})

	KeyVerifyTakenBody = key("verify.taken.body", Message{
		ZhHant: "在你確認之前,這個信箱已經被另一個帳號註冊了。你的帳號和原本的信箱沒有改變。",
		En: "Another account registered that address before you confirmed. Your account " +
			"and its current address are unchanged.",
	})

	KeyEmailSection = key("account.email", Message{ZhHant: "電子郵件", En: "Email address"})

	KeyEmailVerified = key("account.email.verified", Message{
		ZhHant: "已確認",
		En:     "Confirmed",
	})

	KeyEmailUnverified = key("account.email.unverified", Message{
		ZhHant: "尚未確認",
		En:     "Not confirmed",
	})

	KeyEmailUnverifiedHint = key("account.email.unverified.hint", Message{
		ZhHant: "沒有確認過的信箱,我們無法確定通知信寄得到 —— 打錯一個字,你就什麼都收不到。",
		En: "Without a confirmed address we cannot tell whether anything reaches you. One " +
			"mistyped letter and nothing does.",
	})

	KeyEmailResend = key("account.email.resend", Message{ZhHant: "重新寄確認信", En: "Send it again"})

	KeyEmailPending = key("account.email.pending", Message{
		ZhHant: "等待確認:%s",
		En:     "Waiting to be confirmed: %s",
	})

	KeyEmailChange = key("account.email.change", Message{ZhHant: "更換信箱", En: "Change your address"})

	KeyEmailChangeHint = key("account.email.change.hint", Message{
		ZhHant: "確認信會寄到新信箱。點過連結才會生效,在那之前原本的信箱照常收信。",
		En: "We send a letter to the new address. It takes effect only when you follow the " +
			"link — until then the old address keeps receiving.",
	})

	KeyFieldNewEmail = key("field.email.new", Message{ZhHant: "新的電子郵件", En: "New email address"})

	KeyEmailSent = key("account.notice.email.sent", Message{
		ZhHant: "確認信已寄出,請到信箱點一下連結。",
		En:     "Confirmation sent. Follow the link in it to finish.",
	})

	KeyEmailTakenNotice = key("account.notice.email.taken", Message{
		ZhHant: "這個信箱已經有人使用。",
		En:     "That address is already in use.",
	})

	KeyEmailInvalidNotice = key("account.notice.email.invalid", Message{
		ZhHant: "信箱格式看起來不正確。",
		En:     "That does not look like an email address.",
	})
)
