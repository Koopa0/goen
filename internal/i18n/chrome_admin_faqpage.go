package i18n

var (
	KeyAdminFaqpLead = key("admin.faqp.lead", Message{
		ZhHant: "/faq 讀的就是這一份。改完不用重新部署 —— 這一頁就是那句話的門。",
		En: "/faq reads exactly this list. An edit needs no redeploy — this page is that " +
			"sentence's door.",
	})
	KeyAdminFaqpNoEnglish = key("admin.faqp.noenglish", Message{
		ZhHant: "有 %s 則沒有英文答案,英文訪客讀到的是中文。",
		En:     "%s entries have no English answer — an English visitor reads those in Chinese.",
	})

	KeyAdminFaqpAdd = key("admin.faqp.add", Message{
		ZhHant: "新增問答",
		En:     "Add a question and answer",
	})
	KeyAdminFaqpQuestion            = key("admin.faqp.question", Message{ZhHant: "問題", En: "Question"})
	KeyAdminFaqpAnswer              = key("admin.faqp.answer", Message{ZhHant: "答案", En: "Answer"})
	KeyAdminFaqpCategoryPlaceholder = key("admin.faqp.category.placeholder", Message{
		ZhHant: "訂單",
		En:     "Orders",
	})
	KeyAdminFaqpCategoryHint = key("admin.faqp.category.hint", Message{
		ZhHant: "同一個分類會排在一起,順序是加入的順序。",
		En:     "Entries sharing a category sit together, in the order they were added.",
	})

	KeyAdminFaqpCategoryEn = key("admin.faqp.category.en", Message{
		ZhHant: "分類(英文)",
		En:     "Category (English)",
	})
	KeyAdminFaqpQuestionEn = key("admin.faqp.question.en", Message{
		ZhHant: "問題(英文)",
		En:     "Question (English)",
	})
	KeyAdminFaqpAnswerEn = key("admin.faqp.answer.en", Message{
		ZhHant: "答案(英文)",
		En:     "Answer (English)",
	})
	KeyAdminFaqpEnHint = key("admin.faqp.en.hint", Message{
		ZhHant: "留空的話,英文訪客會讀到中文 —— 讀得懂,但看得出還沒翻。",
		En: "Leave it empty and an English visitor reads the Chinese — legible, but " +
			"visibly untranslated.",
	})

	KeyAdminFaqpCurrent = key("admin.faqp.current", Message{
		ZhHant: "目前的問答",
		En:     "Current questions and answers",
	})
	KeyAdminFaqpSave    = key("admin.faqp.save", Message{ZhHant: "儲存", En: "Save"})
	KeyAdminFaqpUpdated = key("admin.faqp.updated", Message{ZhHant: "最後修改 %s", En: "Last edited %s"})
	KeyAdminFaqpEmpty   = key("admin.faqp.empty", Message{
		ZhHant: "還沒有問答。/faq 會是一張空白的頁面。",
		En:     "No questions and answers yet. /faq is a blank page.",
	})
)
