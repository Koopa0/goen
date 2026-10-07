package i18n

var (
	KeyAdminCreditReasonOrderSpend    = key("admin.credit.reason.order_spend", Message{ZhHant: "訂單折抵", En: "Applied to an order"})
	KeyAdminCreditReasonOrderReversed = key("admin.credit.reason.order_reversed", Message{ZhHant: "訂單取消，購物金退回", En: "Order cancelled, credit returned"})
	KeyAdminCreditReasonReturnPayout  = key("admin.credit.reason.return_payout", Message{ZhHant: "退貨退回購物金", En: "Credit for a return"})
	KeyAdminCreditReasonPoints        = key("admin.credit.reason.points", Message{ZhHant: "點數兌換", En: "Points redeemed"})
	KeyAdminCreditAmountError         = key("admin.credit.amount_error", Message{ZhHant: "請輸入 1 至 100,000 元的整數金額。", En: "Enter a whole-dollar amount from NT$1 to NT$100,000."})
	KeyAdminCreditReasonError         = key("admin.credit.reason_error", Message{ZhHant: "請填寫 1 至 200 字的發放事由。", En: "Enter a reason between 1 and 200 characters."})
	KeyAdminCreditReview              = key("admin.credit.review", Message{ZhHant: "核對顧客與金額", En: "Review recipient and amount"})
	KeyAdminCreditConfirm             = key("admin.credit.confirm", Message{ZhHant: "確認發放購物金", En: "Confirm credit grant"})
	KeyAdminCreditCustomer            = key("admin.credit.customer", Message{ZhHant: "收取購物金的顧客", En: "Credit recipient"})
	KeyAdminCreditBalance             = key("admin.credit.balance", Message{ZhHant: "目前餘額", En: "Current balance"})
	KeyAdminCreditEdit                = key("admin.credit.edit", Message{ZhHant: "返回修改", En: "Back to edit"})
	KeyAdminCreditUnknown             = key("admin.credit.unknown", Message{ZhHant: "找不到使用這個電子郵件的會員，請核對後再試。", En: "No customer has that email. Check the address and try again."})

	KeyAdminErasedShort = key("admin.erased.short", Message{ZhHant: "（已刪除）", En: "(deleted)"})

	KeyAdminPageCredit = key("admin.page.credit", Message{ZhHant: "購物金", En: "Store credit"})

	KeyAdminCreditLead = key("admin.credit.lead", Message{
		ZhHant: "發放的購物金會在該會員下次結帳時自動折抵。金額以「元」為單位。",
		En: "Credit granted here is spent automatically at that customer's next checkout. " +
			"Amounts are in whole New Taiwan dollars.",
	})

	KeyAdminCreditEmail = key("admin.credit.email", Message{ZhHant: "會員電子郵件", En: "Customer email"})

	KeyAdminCreditAmount = key("admin.credit.amount", Message{ZhHant: "金額（元）", En: "Amount (NT$)"})

	KeyAdminCreditReason = key("admin.credit.reason", Message{ZhHant: "事由", En: "Reason"})

	KeyAdminCreditGrant = key("admin.credit.grant", Message{ZhHant: "發放購物金", En: "Grant credit"})

	KeyAdminCreditRecent = key("admin.credit.recent", Message{ZhHant: "最近的異動", En: "Recent postings"})

	KeyAdminCreditEmpty = key("admin.credit.empty", Message{
		ZhHant: "還沒有任何購物金異動。",
		En:     "No credit postings yet.",
	})
)

var (
	KeyAdminNoticeCreditGranted = key("admin.notice.credit.granted", Message{
		ZhHant: "已發放。這位顧客目前的餘額是 %s。",
		En:     "Granted. This customer's balance is now %s.",
	})
)
