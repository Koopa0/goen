package i18n

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
