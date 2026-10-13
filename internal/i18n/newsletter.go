package i18n

var (
	KeyNewsletterSentBody = key("news.sent.body", Message{
		ZhHant: "如果你填寫的信箱還沒訂閱，請到信箱點一下確認連結，訂閱才會生效。兩天內有效。沒收到請看看垃圾郵件。",
		En:     "If the address you entered is not already subscribed, check its inbox for the confirmation link. It works for two days. If it has not arrived, check your spam folder.",
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
		ZhHant: "按下按鈕就完成訂閱。不定期寄送，任何時候都可以退訂。",
		En:     "One button and you are subscribed. We send occasionally, and you can leave any time.",
	})

	KeyNewsletterConfirmSubmit = key("news.confirm.submit", Message{
		ZhHant: "確認訂閱",
		En:     "Confirm subscription",
	})

	KeyNewsletterDone = key("news.done", Message{
		ZhHant: "已訂閱",
		En:     "Subscribed",
	})

	KeyNewsletterDoneBody = key("news.done.body", Message{
		ZhHant: "你已訂閱 goen 電子報。不定期寄送；退訂連結在剛剛寄出的那封信裡。",
		En:     "You are subscribed to the goen newsletter. We send occasionally. The unsubscribe link is in the email we just sent.",
	})

	KeyNewsletterLeaveTitle = key("news.leave.title", Message{
		ZhHant: "退訂 goen 電子報",
		En:     "Unsubscribe from the goen newsletter",
	})

	KeyNewsletterLeaveBody = key("news.leave.body", Message{
		ZhHant: "按下按鈕就不會再收到電子報。訂單、出貨與退貨的通知信不受影響。",
		En: "One button and the newsletter stops. Order, dispatch and returns notices are " +
			"not affected.",
	})

	KeyNewsletterLeaveSubmit = key("news.leave.submit", Message{
		ZhHant: "確認退訂",
		En:     "Unsubscribe",
	})

	KeyNewsletterLeft = key("news.left", Message{ZhHant: "已退訂", En: "Unsubscribed"})

	KeyNewsletterLeftBody = key("news.left.body", Message{
		ZhHant: "你不會再收到 goen 電子報。訂單相關的通知信不受影響。",
		En:     "You will not receive the goen newsletter again. Order notices are not affected.",
	})

	KeyNewsletterConfirmDead = key("news.confirm.dead", Message{
		ZhHant: "連結可能已經用過或超過兩天。",
		En:     "It may have been used already, or be more than two days old.",
	})

	KeyNewsletterLeaveDead = key("news.leave.dead", Message{
		ZhHant: "這個退訂連結不正確。如果還在收到電子報，請聯絡我們。",
		En:     "This unsubscribe link is not valid. If the newsletter keeps arriving, contact us.",
	})

	KeyNewsletterFailed = key("news.failed", Message{ZhHant: "訂閱未完成", En: "Not subscribed"})

	KeyNewsletterReviewForm = key("news.review.form", Message{
		ZhHant: "請查看頁尾訂閱表單的訊息，再試一次。",
		En:     "Check the message by the newsletter form below, then try again.",
	})

	KeyNewsletterRetry = key("news.retry", Message{
		ZhHant: "系統暫時無法處理訂閱，請稍後再試。",
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
		ZhHant: "不定期寄送，每封都可退訂。訂閱前會先寄確認信。",
		En:     "Sent occasionally, with an unsubscribe link in every letter. A confirmation link comes first.",
	})

	KeyNewsletterSubmit = key("site.newsletter.submit", Message{ZhHant: "訂閱", En: "Subscribe"})

	KeyNewsletterInlineDone = key("site.newsletter.done", Message{
		ZhHant: "如果你填寫的信箱還沒訂閱，請到信箱點一下確認連結。",
		En:     "If the address you entered is not already subscribed, check its inbox for the confirmation link.",
	})
)

var (
	KeyAdminNewsLead = key("admin.news.lead", Message{
		ZhHant: "名單上只有已確認的信箱。從頁尾訂閱的人，點了確認信裡的連結才會加入。",
		En:     "Only confirmed addresses are on this list. Someone who signs up in the footer joins only after following the link in the confirmation email.",
	})

	KeyAdminNewsOnList = key("admin.news.onlist", Message{ZhHant: "名單人數", En: "On the list"})

	KeyAdminNewsAwaiting = key("admin.news.awaiting", Message{
		ZhHant: "等待確認",
		En:     "Awaiting confirmation",
	})

	KeyAdminNewsUnsubscribed = key("admin.news.unsubscribed", Message{ZhHant: "已退訂", En: "Unsubscribed"})

	KeyAdminNewsCompose = key("admin.news.compose", Message{ZhHant: "寫一封", En: "Write a letter"})

	KeyAdminNewsSubject = key("admin.news.subject", Message{ZhHant: "主旨", En: "Subject"})

	KeyAdminNewsBody = key("admin.news.body", Message{ZhHant: "內容", En: "Body"})

	KeyAdminNewsDraftHint = key("admin.news.drafthint", Message{
		ZhHant: "存成草稿，還不會寄出。退訂連結由系統加在每一封的最後，不用自己貼。",
		En: "Saving keeps this as a draft; nothing goes out yet. The unsubscribe link is added to " +
			"the foot of every letter automatically — there is no need to paste one in.",
	})

	KeyAdminNewsSaveDraft = key("admin.news.savedraft", Message{ZhHant: "存成草稿", En: "Save as draft"})

	KeyAdminNewsIssues = key("admin.news.issues", Message{ZhHant: "已寫的", En: "Written so far"})

	KeyAdminNewsEmpty = key("admin.news.empty", Message{ZhHant: "還沒有任何一封。", En: "Nothing written yet."})

	KeyAdminNewsSentMeta = key("admin.news.sentmeta", Message{ZhHant: "%s 寄出 · %s 封", En: "Sent %s · %s copies"})

	KeyAdminNewsSent = key("admin.news.sent", Message{ZhHant: "已寄出", En: "Sent"})

	KeyAdminNewsSend = key("admin.news.send", Message{ZhHant: "寄給名單上的 %s 人", En: "Send to %s on the list"})

	KeyAdminNewsNobody = key("admin.news.nobody", Message{
		ZhHant: "名單上還沒有人",
		En:     "Nobody is on the list yet",
	})

	KeyAdminPageNewsletter = key("admin.page.newsletter", Message{ZhHant: "電子報", En: "Newsletter"})
)

var (
	KeyAdminNoticeSaved = key("admin.notice.saved", Message{
		ZhHant: "草稿已儲存。",
		En:     "The draft has been saved.",
	})

	KeyAdminNoticeSent = key("admin.notice.sent", Message{
		ZhHant: "電子報已送出。",
		En:     "The newsletter has been sent.",
	})

	KeyAdminNoticeAlready = key("admin.notice.already", Message{
		ZhHant: "這期電子報已經寄出過了。",
		En:     "That issue has already been sent.",
	})
)

var KeyNewsletterSentTo = key("news.sent.to", Message{
	ZhHant: "如果 %s 還沒訂閱，請到信箱點一下確認連結，訂閱才會生效。兩天內有效。沒收到請看看垃圾郵件。",
	En:     "If %s is not already subscribed, check its inbox for the confirmation link. It works for two days. If it has not arrived, check your spam folder.",
})
