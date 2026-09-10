package i18n

var (
	KeySectionQA = key("pdp.qa", Message{ZhHant: "問與答", En: "Questions & answers"})

	KeyQuestionPosted = key("pdp.qa.posted", Message{
		ZhHant: "問題已經送出了。我們回覆之後會出現在這裡。",
		En:     "Your question is posted. Our answer will appear here.",
	})

	KeyQuestionRefused = key("pdp.qa.refused", Message{
		ZhHant: "問題沒有送出 —— 請確認內容不是空的,而且在 300 字以內。",
		En:     "That question was not posted — check it is not empty and under 300 characters.",
	})

	KeyStaffAnswer = key("pdp.qa.staff", Message{ZhHant: "官方回覆", En: "From goen"})

	KeyNoAnswerYet = key("pdp.qa.noanswer", Message{
		ZhHant: "還沒有人回答。",
		En:     "No answer yet.",
	})

	KeyNoQuestionsYet = key("pdp.qa.none", Message{
		ZhHant: "還沒有人問過這個商品。",
		En:     "Nobody has asked about this yet.",
	})

	KeyAskLabel = key("field.question", Message{ZhHant: "想問什麼?", En: "What would you like to know?"})

	KeyAskPlaceholder = key("field.question.placeholder", Message{
		ZhHant: "例如:這個型號支援哪些快充協定?",
		En:     "For example: which fast-charge standards does this model support?",
	})

	KeyAskNeedsSignIn = key("pdp.qa.signin", Message{
		ZhHant: "需要登入才能發問。回答會公開顯示。",
		En:     "Sign in to ask. Answers are shown publicly.",
	})

	KeyAskSubmit = key("pdp.qa.submit", Message{ZhHant: "送出問題", En: "Ask"})

	KeyErasedAccount = key("pdp.qa.erased", Message{ZhHant: "已刪除的帳號", En: "Deleted account"})
)

var (
	KeyAdminPageQuestions = key("admin.page.questions", Message{ZhHant: "顧客提問", En: "Customer questions"})

	KeyAdminQuestionsLead = key("admin.questions.lead", Message{
		ZhHant: "等最久的排在最前面 —— 問了三天沒人回的比今天早上剛問的更急。回覆會標示「官方回覆」,並排在該問題的最上面。",
		En: "The longest wait comes first — a question nobody answered for three days is more urgent " +
			"than one asked this morning. Your reply is marked as the shop's and sorts above the rest.",
	})

	KeyAdminQuestionsWaiting = key("admin.questions.waiting", Message{
		ZhHant: "%s 則待回覆",
		En:     "%s awaiting an answer",
	})

	KeyAdminQuestionsEmpty = key("admin.questions.empty", Message{ZhHant: "還沒有人提問", En: "Nobody has asked anything yet"})

	KeyAdminQuestionAnswers = key("admin.question.answers", Message{ZhHant: "%s 則回覆", En: "%s replies"})

	KeyAdminQuestionReply = key("admin.question.reply", Message{ZhHant: "回覆", En: "Reply"})

	KeyAdminQuestionHint = key("admin.question.hint", Message{
		ZhHant: "以 goen 的名義回覆",
		En:     "Reply as goen",
	})

	KeyAdminQuestionSend = key("admin.question.send", Message{ZhHant: "送出官方回覆", En: "Post the shop's reply"})

	KeyAdminQuestionHide = key("admin.question.hide", Message{ZhHant: "隱藏這則提問", En: "Hide this question"})

	KeyAdminQAnswered = key("admin.question.answered", Message{ZhHant: "已回覆", En: "Answered"})

	KeyAdminQCustomerOnly = key("admin.question.customeronly", Message{
		ZhHant: "只有顧客回覆",
		En:     "Only customers replied",
	})

	KeyAdminQWaiting = key("admin.question.waiting", Message{ZhHant: "待回覆", En: "Awaiting an answer"})
)
