package i18n

var (
	KeyAdminErasedShort = key("admin.erased.short", Message{ZhHant: "(已刪除)", En: "(deleted)"})

	KeyAdminPageCredit = key("admin.page.credit", Message{ZhHant: "商店額度", En: "Store credit"})

	KeyAdminCreditLead = key("admin.credit.lead", Message{
		ZhHant: "發放的額度會在該會員下次結帳時自動折抵。金額以「元」為單位。",
		En: "Credit granted here is spent automatically at that customer's next checkout. " +
			"Amounts are in whole New Taiwan dollars.",
	})

	KeyAdminCreditEmail = key("admin.credit.email", Message{ZhHant: "會員 Email", En: "Customer email"})

	KeyAdminCreditAmount = key("admin.credit.amount", Message{ZhHant: "金額(元)", En: "Amount (NT$)"})

	KeyAdminCreditReason = key("admin.credit.reason", Message{ZhHant: "事由", En: "Reason"})

	KeyAdminCreditGrant = key("admin.credit.grant", Message{ZhHant: "發放額度", En: "Grant credit"})

	KeyAdminCreditRecent = key("admin.credit.recent", Message{ZhHant: "最近的異動", En: "Recent postings"})

	KeyAdminCreditEmpty = key("admin.credit.empty", Message{
		ZhHant: "還沒有任何額度異動。",
		En:     "No credit postings yet.",
	})
)
