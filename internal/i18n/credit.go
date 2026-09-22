package i18n

var (
	KeyAdminCreditReview   = key("admin.credit.review", Message{ZhHant: "核對顧客與金額", En: "Review recipient and amount"})
	KeyAdminCreditConfirm  = key("admin.credit.confirm", Message{ZhHant: "確認發放額度", En: "Confirm credit grant"})
	KeyAdminCreditCustomer = key("admin.credit.customer", Message{ZhHant: "收取額度的顧客", En: "Credit recipient"})
	KeyAdminCreditBalance  = key("admin.credit.balance", Message{ZhHant: "目前餘額", En: "Current balance"})
	KeyAdminCreditEdit     = key("admin.credit.edit", Message{ZhHant: "返回修改", En: "Back to edit"})
	KeyAdminCreditUnknown  = key("admin.credit.unknown", Message{ZhHant: "找不到這個 Email 的會員，請核對後再試。", En: "No customer has that email. Check the address and try again."})

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

var (
	KeyAdminNoticeCreditGranted = key("admin.notice.credit.granted", Message{
		ZhHant: "已發放。這位顧客目前的餘額是 %s。",
		En:     "Granted. This customer's balance is now %s.",
	})
)
