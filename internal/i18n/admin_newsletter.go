package i18n

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
