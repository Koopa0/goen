package i18n

var (
	KeySectionQA = key("pdp.qa", Message{ZhHant: "問與答", En: "Questions & answers"})

	KeyQuestionPosted = key("pdp.qa.posted", Message{
		ZhHant: "問題已經送出了。我們回覆之後會出現在這裡。",
		En:     "Your question is posted. Our answer will appear here.",
	})

	KeyQuestionRefused = key("pdp.qa.refused", Message{
		ZhHant: "問題沒有送出。請確認內容不是空的，並在 300 字以內。",
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

	KeyAskLabel = key("field.question", Message{ZhHant: "想問什麼？", En: "What would you like to know?"})

	KeyAskPlaceholder = key("field.question.placeholder", Message{
		ZhHant: "例如：這個型號支援哪些快充協定？",
		En:     "For example: which fast-charge standards does this model support?",
	})

	KeySignInToAsk = key("pdp.qa.signin.rest", Message{
		ZhHant: "後可以發問。",
		En:     " to ask a question.",
	})

	KeyAskSubmit = key("pdp.qa.submit", Message{ZhHant: "送出問題", En: "Ask"})

	KeyErasedAccount = key("pdp.qa.erased", Message{ZhHant: "已刪除的帳號", En: "Deleted account"})
)

var (
	KeyAdminQuestionsHiddenEmpty = key("admin.questions.hiddenempty", Message{ZhHant: "沒有隱藏的提問", En: "No hidden questions"})
	KeyAdminQHidden              = key("admin.question.hidden", Message{ZhHant: "已隱藏", En: "Hidden"})
	KeyAdminQuestionShow         = key("admin.question.show", Message{ZhHant: "恢復顯示這則提問", En: "Show this question again"})
	KeyAdminQuestionsHidden      = key("admin.questions.hidden", Message{ZhHant: "查看隱藏的提問", En: "View hidden questions"})
	KeyAdminQuestionsVisible     = key("admin.questions.visible", Message{ZhHant: "查看顯示中的提問", En: "View visible questions"})
	KeyAdminPageQuestions        = key("admin.page.questions", Message{ZhHant: "顧客提問", En: "Customer questions"})

	KeyAdminQuestionsLead = key("admin.questions.lead", Message{
		ZhHant: "還沒有官方回覆的提問排在前面，其中等最久的排第一。你的回覆會標示「官方回覆」，排在該提問的其他回答之上。",
		En:     "Questions without a shop reply come first, the longest wait at the top. Your reply is labelled “From goen” and sits above the other answers.",
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

	KeyAdminQuestionBodyError = key("admin.question.bodyerror", Message{
		ZhHant: "回覆不能留白，最多 1000 字。",
		En:     "A reply cannot be empty and may be at most 1,000 characters.",
	})

	KeyAdminQuestionSend = key("admin.question.send", Message{ZhHant: "送出官方回覆", En: "Post the shop's reply"})

	KeyAdminAnswerHide = key("admin.answer.hide", Message{ZhHant: "撤下這則回覆", En: "Withdraw this answer"})

	KeyAdminQuestionHide = key("admin.question.hide", Message{ZhHant: "隱藏這則提問", En: "Hide this question"})

	KeyAdminQAnswered = key("admin.question.answered", Message{ZhHant: "已回覆", En: "Answered"})

	KeyAdminQCustomerOnly = key("admin.question.customeronly", Message{
		ZhHant: "只有顧客回覆",
		En:     "Only customers replied",
	})

	KeyAdminQWaiting = key("admin.question.waiting", Message{ZhHant: "待回覆", En: "Awaiting an answer"})
)
