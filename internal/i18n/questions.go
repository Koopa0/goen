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
