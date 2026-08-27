package i18n

var (
	KeyNewsletterSent = key("news.sent", Message{
		ZhHant: "確認信已寄出",
		En:     "Check your inbox",
	})

	KeyNewsletterSentBody = key("news.sent.body", Message{
		ZhHant: "請到信箱點一下確認連結,訂閱才會生效。兩天內有效。沒有收到的話,看一下垃圾信件匣。",
		En: "Follow the link we just sent to finish subscribing. It works for two days. " +
			"If it has not arrived, check your spam folder.",
	})

	KeyNewsletterSentMeta = key("news.sent.meta", Message{
		ZhHant: "goen 電子報訂閱確認。",
		En:     "Confirm your goen newsletter subscription.",
	})

	KeyNewsletterConfirmTitle = key("news.confirm.title", Message{
		ZhHant: "確認訂閱 goen 電子報",
		En:     "Confirm your goen newsletter subscription",
	})

	KeyNewsletterConfirmBody = key("news.confirm.body", Message{
		ZhHant: "按下按鈕就完成訂閱。每月一封,任何時候都可以退訂。",
		En:     "One button and you are subscribed. One letter a month, and you can leave any time.",
	})

	KeyNewsletterConfirmSubmit = key("news.confirm.submit", Message{
		ZhHant: "確認訂閱",
		En:     "Confirm subscription",
	})

	KeyNewsletterDone = key("news.done", Message{
		ZhHant: "已訂閱,感謝你的信任",
		En:     "Subscribed — thank you",
	})

	KeyNewsletterDoneBody = key("news.done.body", Message{
		ZhHant: "%s 已經在名單上。每月一封,新品與比價重點,不灌水;退訂連結在剛剛寄出的那封信裡。",
		En: "%s is on the list. One letter a month — new arrivals and what is worth comparing, " +
			"nothing padded. The unsubscribe link is in the email we just sent.",
	})

	KeyNewsletterLeaveTitle = key("news.leave.title", Message{
		ZhHant: "退訂 goen 電子報",
		En:     "Unsubscribe from the goen newsletter",
	})

	KeyNewsletterLeaveBody = key("news.leave.body", Message{
		ZhHant: "按下按鈕就不會再收到電子報。訂單、出貨與退換貨的通知信不受影響。",
		En: "One button and the newsletter stops. Order, dispatch and returns notices are " +
			"not affected.",
	})

	KeyNewsletterLeaveSubmit = key("news.leave.submit", Message{
		ZhHant: "確認退訂",
		En:     "Unsubscribe",
	})

	KeyNewsletterLeft = key("news.left", Message{ZhHant: "已退訂", En: "Unsubscribed"})

	KeyNewsletterLeftBody = key("news.left.body", Message{
		ZhHant: "%s 不會再收到 goen 電子報。訂單相關的通知信不受影響。",
		En:     "%s will not receive the goen newsletter again. Order notices are not affected.",
	})

	KeyNewsletterLinkDead = key("news.link.dead", Message{
		ZhHant: "這個連結無法使用",
		En:     "That link does not work",
	})

	KeyNewsletterConfirmDead = key("news.confirm.dead", Message{
		ZhHant: "連結可能已經用過或超過兩天。請回到頁尾重新填一次 Email。",
		En: "It may have been used already, or be more than two days old. " +
			"Subscribe again from the footer of any page.",
	})

	KeyNewsletterLeaveDead = key("news.leave.dead", Message{
		ZhHant: "連結可能不完整。如果還在收到電子報,寫信到 support@goen.tw,我們幫你處理。",
		En: "The link may be incomplete. If the newsletter keeps arriving, write to " +
			"support@goen.tw and we will take care of it.",
	})

	KeyNewsletterFailed = key("news.failed", Message{ZhHant: "訂閱未完成", En: "Not subscribed"})

	KeyNewsletterRetry = key("news.retry", Message{
		ZhHant: "系統暫時無法處理訂閱,請稍後再試。",
		En:     "We cannot process that right now. Please try again shortly.",
	})

	KeyIssueSubjectRequired = key("valid.issue.subject", Message{
		ZhHant: "請填寫主旨",
		En:     "Write a subject line",
	})

	KeyIssueSubjectTooLong = key("valid.issue.subject.long", Message{
		ZhHant: "主旨請控制在 120 個字以內",
		En:     "Keep the subject under 120 characters",
	})

	KeyIssueBodyRequired = key("valid.issue.body", Message{
		ZhHant: "請填寫內容",
		En:     "Write the letter",
	})

	KeyIssueBodyTooLong = key("valid.issue.body.long", Message{
		ZhHant: "內容請控制在 20000 個字以內",
		En:     "Keep the letter under 20000 characters",
	})

	KeyNewsletter = key("site.newsletter", Message{ZhHant: "電子報", En: "Newsletter"})

	KeyNewsletterNote = key("site.newsletter.note", Message{
		ZhHant: "每月一封,新品與比價重點,不灌水。",
		En:     "One letter a month: new arrivals and what is worth comparing, nothing padded.",
	})

	KeyNewsletterSubmit = key("site.newsletter.submit", Message{ZhHant: "訂閱", En: "Subscribe"})

	KeyNewsletterInlineDone = key("site.newsletter.done", Message{
		ZhHant: "確認信已寄出,請到信箱點一下連結。",
		En:     "Check your inbox and follow the link to finish.",
	})
)
