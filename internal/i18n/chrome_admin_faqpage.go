package i18n

// The words /admin/faq writes.
//
// The FAQ editor is the door CLAUDE.md promised when it said support could
// answer a recurring question without a deploy, and it is the one back-office
// page whose whole subject is translation: every entry carries a Chinese half
// the shop must fill and an English half it may leave empty, and the page has to
// say what happens when it does.

var (
	// The heading and the sentence under it. The lead is carried across in full:
	// it says WHY the page exists, which is the half a summary would drop.
	KeyAdminFaqpLead = key("admin.faqp.lead", Message{
		ZhHant: "/faq 讀的就是這一份。改完不用重新部署 —— 這一頁就是那句話的門。",
		En: "/faq reads exactly this list. An edit needs no redeploy — this page is that " +
			"sentence's door.",
	})
	// Counted rather than listed, because /faq groups by category and a gap in a
	// long list is easy to miss. It names the CONSEQUENCE, not the number alone:
	// the English columns fall back to the Chinese, so an untranslated entry is
	// a page that still renders and still fails its reader.
	KeyAdminFaqpNoEnglish = key("admin.faqp.noenglish", Message{
		ZhHant: "有 %s 則沒有英文答案,英文訪客讀到的是中文。",
		En:     "%s entries have no English answer — an English visitor reads those in Chinese.",
	})

	// The add form.
	KeyAdminFaqpAdd = key("admin.faqp.add", Message{
		ZhHant: "新增問答",
		En:     "Add a question and answer",
	})
	KeyAdminFaqpQuestion = key("admin.faqp.question", Message{ZhHant: "問題", En: "Question"})
	KeyAdminFaqpAnswer   = key("admin.faqp.answer", Message{ZhHant: "答案", En: "Answer"})
	// An EXAMPLE of a category, not a value the shop typed — it is compiled into
	// the binary, so it is goen's to say in both languages. What the shop then
	// types into the field stays whatever it types.
	KeyAdminFaqpCategoryPlaceholder = key("admin.faqp.category.placeholder", Message{
		ZhHant: "訂單",
		En:     "Orders",
	})
	KeyAdminFaqpCategoryHint = key("admin.faqp.category.hint", Message{
		ZhHant: "同一個分類會排在一起,順序是加入的順序。",
		En:     "Entries sharing a category sit together, in the order they were added.",
	})

	// The English half of an entry. Each column is separately optional and falls
	// back to the Chinese, which is why the hint states the outcome rather than
	// calling the field optional: an English visitor is served something either
	// way, and only one of the two ways was intended.
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

	// The list beside the form, where each row is its own edit form.
	KeyAdminFaqpCurrent = key("admin.faqp.current", Message{
		ZhHant: "目前的問答",
		En:     "Current questions and answers",
	})
	// Its own key rather than the account page's Save: every back-office
	// template reads from the admin catalogue, and the two surfaces are free to
	// word a button differently without either being wrong.
	KeyAdminFaqpSave    = key("admin.faqp.save", Message{ZhHant: "儲存", En: "Save"})
	KeyAdminFaqpUpdated = key("admin.faqp.updated", Message{ZhHant: "最後修改 %s", En: "Last edited %s"})
	KeyAdminFaqpEmpty   = key("admin.faqp.empty", Message{
		ZhHant: "還沒有問答。/faq 會是一張空白的頁面。",
		En:     "No questions and answers yet. /faq is a blank page.",
	})
)
