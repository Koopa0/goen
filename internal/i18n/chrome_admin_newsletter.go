package i18n

// The words /admin/newsletter writes: the list's three figures, the composer,
// and one issue per row.
//
// The lead is the page's own statement of what double opt-in MEANS — the footer
// form collects a REQUEST and nothing joins the list until somebody answers a
// letter — so it is carried across in full. A staff member who cannot read
// Chinese and reads only "Newsletter" above three numbers has no way to know
// that the shop cannot add an address to this list at all.

var (
	// The heading's lead. 後台 and 電子報 above it are admin.eyebrow and
	// admin.page.newsletter.
	KeyAdminNewsLead = key("admin.news.lead", Message{
		ZhHant: "名單上只有自己確認過的信箱 —— 頁尾送出的只是「請求」,點過信裡的連結才會進名單。",
		En: "Only mailboxes that confirmed themselves are on this list — the footer form sends a " +
			"request, and nothing joins the list until somebody follows the link in the email.",
	})

	// The three figures a person running a newsletter asks for.
	//
	// 已退訂 is also news.left, where it is the storefront's confirmation that
	// YOU have left. Here it is a label over a count of other people, and the two
	// are free to be worded differently the day either is rewritten — the
	// column-versus-title split admin.col.* already draws.
	KeyAdminNewsOnList   = key("admin.news.onlist", Message{ZhHant: "名單人數", En: "On the list"})
	KeyAdminNewsAwaiting = key("admin.news.awaiting", Message{
		ZhHant: "等待確認",
		En:     "Awaiting confirmation",
	})
	KeyAdminNewsUnsubscribed = key("admin.news.unsubscribed", Message{ZhHant: "已退訂", En: "Unsubscribed"})

	// The composer. 內容 here is a letter's body, which is why it is not
	// admin.col.body — that column heads a table of messages a customer wrote,
	// and English calls the two different things.
	KeyAdminNewsCompose = key("admin.news.compose", Message{ZhHant: "寫一封", En: "Write a letter"})
	KeyAdminNewsSubject = key("admin.news.subject", Message{ZhHant: "主旨", En: "Subject"})
	KeyAdminNewsBody    = key("admin.news.body", Message{ZhHant: "內容", En: "Body"})
	// Says both halves: this button does not send, and the unsubscribe link is
	// not something to type in. It cannot be — the token is per RECIPIENT, so
	// there is no one link a person could paste, and somebody who did not know
	// that would write the letter around a link that does not exist.
	KeyAdminNewsDraftHint = key("admin.news.drafthint", Message{
		ZhHant: "存成草稿,還不會寄出。退訂連結由系統加在每一封的最後,不用自己貼。",
		En: "Saving keeps this as a draft; nothing goes out yet. The unsubscribe link is added to " +
			"the foot of every letter automatically — there is no need to paste one in.",
	})
	KeyAdminNewsSaveDraft = key("admin.news.savedraft", Message{ZhHant: "存成草稿", En: "Save as draft"})

	// What has been written, and what became of it.
	KeyAdminNewsIssues = key("admin.news.issues", Message{ZhHant: "已寫的", En: "Written so far"})
	KeyAdminNewsEmpty  = key("admin.news.empty", Message{ZhHant: "還沒有任何一封。", En: "Nothing written yet."})
	// The date and the number of copies, in one message: which order they read in
	// is not the same in both languages.
	KeyAdminNewsSentMeta = key("admin.news.sentmeta", Message{ZhHant: "%s 寄出 · %s 封", En: "Sent %s · %s copies"})
	KeyAdminNewsSent     = key("admin.news.sent", Message{ZhHant: "已寄出", En: "Sent"})
	// The one irreversible button in the back office, so it names the number it
	// is about to write to rather than saying only 寄出.
	KeyAdminNewsSend   = key("admin.news.send", Message{ZhHant: "寄給名單上的 %s 人", En: "Send to %s on the list"})
	KeyAdminNewsNobody = key("admin.news.nobody", Message{
		ZhHant: "名單上還沒有人",
		En:     "Nobody is on the list yet",
	})
)
