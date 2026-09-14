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
		ZhHant: "已訂閱",
		En:     "Subscribed",
	})

	KeyNewsletterDoneBody = key("news.done.body", Message{
		ZhHant: "%s 已經在名單上。每月一封;退訂連結在剛剛寄出的那封信裡。",
		En: "%s is on the list. One letter a month. The unsubscribe link is in the email " +
			"we just sent.",
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
		ZhHant: "每月一封。訂閱前會先寄確認信。",
		En:     "One letter a month. We send a confirmation link first.",
	})

	KeyNewsletterSubmit = key("site.newsletter.submit", Message{ZhHant: "訂閱", En: "Subscribe"})

	KeyNewsletterInlineDone = key("site.newsletter.done", Message{
		ZhHant: "確認信已寄出,請到信箱點一下連結。",
		En:     "Check your inbox and follow the link to finish.",
	})
)

var (
	KeyAdminNewsLead = key("admin.news.lead", Message{
		ZhHant: "名單上只有自己確認過的信箱 —— 頁尾送出的只是「請求」,點過信裡的連結才會進名單。",
		En: "Only mailboxes that confirmed themselves are on this list — the footer form sends a " +
			"request, and nothing joins the list until somebody follows the link in the email.",
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
		ZhHant: "存成草稿,還不會寄出。退訂連結由系統加在每一封的最後,不用自己貼。",
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
