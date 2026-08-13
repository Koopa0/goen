package i18n

// The words the back office's TEMPLATES write.
//
// Page headings, the sentence under each one, empty states and button labels.
// They are here rather than beside the view-model keys because they answer a
// different question: a view model computes a fact about a row, and these
// explain the PAGE — why it is ordered the way it is, what a control will do,
// and what an empty one means.
//
// The explanatory line under each heading is translated in full rather than
// summarised. Those sentences are where this back office says why a queue is
// oldest-first or why hiding a review also takes it out of the score, and an
// English-reading staff member who loses them loses the reasoning, not
// decoration.

var (
	// Every admin page carries it above the heading.
	KeyAdminEyebrow = key("admin.eyebrow", Message{ZhHant: "後台", En: "Back office"})

	// /admin/audit.
	KeyAdminAuditLead = key("admin.audit.lead", Message{
		ZhHant: "誰在什麼時候做了什麼。這份紀錄只能新增,寫進去就改不了也刪不掉。",
		En: "Who did what, and when. This record is append-only: nothing written here " +
			"can be changed or removed.",
	})
	KeyAdminAuditEmpty = key("admin.audit.empty", Message{
		ZhHant: "還沒有任何紀錄。",
		En:     "Nothing recorded yet.",
	})

	// /admin/messages. Oldest first, and the line says why.
	KeyAdminMessagesLead = key("admin.messages.lead", Message{
		ZhHant: "等最久的排在最前面 —— 三天前寫信的人比今天早上寫的更急,最新排在前面剛好把他埋掉。",
		En: "The longest wait comes first — somebody who wrote three days ago is more urgent " +
			"than somebody who wrote this morning, and newest-first would bury them exactly then.",
	})
	KeyAdminMessagesOpen  = key("admin.messages.open", Message{ZhHant: "待處理 %s", En: "%s open"})
	KeyAdminMessagesEmpty = key("admin.messages.empty", Message{
		ZhHant: "沒有任何聯絡訊息。",
		En:     "No contact messages.",
	})
	KeyAdminMessagesOrder = key("admin.messages.order", Message{ZhHant: "訂單", En: "Order"})

	// /admin/reviews. Visible on posting, hidden by exception — and hiding takes
	// the review out of the SCORE, which is the half that matters.
	KeyAdminReviewsLead = key("admin.reviews.lead", Message{
		ZhHant: "評價寫完就顯示,不先審 —— 每一則都要人核准的評價頁,讀起來就是廣告。隱藏是例外,而且會把那一則從評分裡一起拿掉。",
		En: "A review appears as soon as it is written, with no queue in front of it — a review page " +
			"where every entry was approved by the shop reads as advertising. Hiding is the exception, " +
			"and it takes that review out of the rating as well as off the page.",
	})
	KeyAdminReviewsEmpty = key("admin.reviews.empty", Message{ZhHant: "還沒有任何評價。", En: "No reviews yet."})
	KeyAdminReviewStars  = key("admin.review.stars", Message{ZhHant: "%s 分", En: "%s out of 5"})
	KeyAdminReviewBought = key("admin.review.bought", Message{ZhHant: "· 已購買", En: "· verified purchase"})
	KeyAdminReviewHidden = key("admin.review.hidden", Message{
		ZhHant: "· 已隱藏(不計入評分)",
		En:     "· hidden (not counted in the rating)",
	})

	// /admin/credit.
	KeyAdminCreditLead = key("admin.credit.lead", Message{
		ZhHant: "發放的額度會在該會員下次結帳時自動折抵。金額以「元」為單位。",
		En: "Credit granted here is spent automatically at that customer's next checkout. " +
			"Amounts are in whole New Taiwan dollars.",
	})
	KeyAdminCreditEmail = key("admin.credit.email", Message{ZhHant: "會員 Email", En: "Customer email"})
	// Dollars, not cents. A form that asks for cents is a form that eventually
	// gives somebody a hundred times too much.
	KeyAdminCreditAmount = key("admin.credit.amount", Message{ZhHant: "金額(元)", En: "Amount (NT$)"})
	KeyAdminCreditReason = key("admin.credit.reason", Message{ZhHant: "事由", En: "Reason"})
	KeyAdminCreditGrant  = key("admin.credit.grant", Message{ZhHant: "發放額度", En: "Grant credit"})
	KeyAdminCreditRecent = key("admin.credit.recent", Message{ZhHant: "最近的異動", En: "Recent postings"})
	KeyAdminCreditEmpty  = key("admin.credit.empty", Message{
		ZhHant: "還沒有任何額度異動。",
		En:     "No credit postings yet.",
	})

	// /admin/questions. Oldest first for the same reason /admin/messages is, and
	// 官方回覆 sorts first because burying it under three customer replies is
	// the same as not having answered.
	KeyAdminQuestionsLead = key("admin.questions.lead", Message{
		ZhHant: "等最久的排在最前面 —— 問了三天沒人回的比今天早上剛問的更急。回覆會標示「官方回覆」,並排在該問題的最上面。",
		En: "The longest wait comes first — a question nobody answered for three days is more urgent " +
			"than one asked this morning. Your reply is marked as the shop's and sorts above the rest.",
	})
	KeyAdminQuestionsWaiting = key("admin.questions.waiting", Message{
		ZhHant: "%s 則待回覆",
		En:     "%s awaiting an answer",
	})
	KeyAdminQuestionsEmpty  = key("admin.questions.empty", Message{ZhHant: "還沒有人提問", En: "Nobody has asked anything yet"})
	KeyAdminQuestionAnswers = key("admin.question.answers", Message{ZhHant: "%s 則回覆", En: "%s replies"})
	KeyAdminQuestionReply   = key("admin.question.reply", Message{ZhHant: "回覆", En: "Reply"})
	KeyAdminQuestionHint    = key("admin.question.hint", Message{
		ZhHant: "以 goen 的名義回覆",
		En:     "Reply as goen",
	})
	KeyAdminQuestionSend = key("admin.question.send", Message{ZhHant: "送出官方回覆", En: "Post the shop's reply"})
	KeyAdminQuestionHide = key("admin.question.hide", Message{ZhHant: "隱藏這則提問", En: "Hide this question"})
)
