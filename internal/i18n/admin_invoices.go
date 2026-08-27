package i18n

var (
	KeyAdminQueueAllowance = key("admin.queue.allowance", Message{
		ZhHant: "開立折讓單",
		En:     "File a credit note",
	})

	KeyAdminQueueAllowanceAmount = key("admin.queue.allowance.amount", Message{
		ZhHant: "折讓金額(元)",
		En:     "Allowance amount (NT$)",
	})

	// A void is for an invoice that should not exist; an allowance is for one
	// that should exist for less. Saying which is which is the whole hint.
	KeyAdminQueueAllowanceHint = key("admin.queue.allowance.hint", Message{
		ZhHant: "退款之後,發票上仍記著原本的銷售額。折讓單是向財政部沖銷退掉的那一部分 —— " +
			"預設帶入已經退回的金額。整張都不該存在時請用作廢。",
		En: "After a refund the invoice still records the whole sale. A credit note relieves " +
			"the refunded part with the tax authority; the amount defaults to what has gone " +
			"back. Use a void instead when the invoice should not exist at all.",
	})

	KeyAdminQueueNoInvoicing = key("admin.queue.noinvoicing", Message{
		ZhHant: "尚未設定加值中心,無法開立發票。設定 GOEN_ECPAY_MERCHANT_ID 後才會開放。",
		En: "No e-invoice provider is configured, so nothing can be issued. Setting " +
			"GOEN_ECPAY_MERCHANT_ID is what turns this on.",
	})

	KeyAdminQueueVoided = key("admin.queue.voided", Message{ZhHant: "(已作廢)", En: "(voided)"})

	// A CLAIM, not a document: the provider was asked and did not answer, so
	// nothing may be at the 加值中心 under it and somebody has to check.
	KeyAdminQueuePending = key("admin.queue.pendingdoc", Message{
		ZhHant: "(尚未開立，請到綠界確認)",
		En:     "(not filed — check ECPay)",
	})

	KeyAdminQueueRandomCode = key("admin.queue.randomcode", Message{
		ZhHant: "隨機碼 %s",
		En:     "Random code %s",
	})

	KeyAdminQueueIssue = key("admin.queue.issue", Message{
		ZhHant: "開立發票",
		En:     "Issue the invoice",
	})

	KeyAdminQueueIssueHint = key("admin.queue.issue.hint", Message{
		ZhHant: "依結帳時選的 %s 開立,金額為訂單總計。",
		En:     "Issued against the %s chosen at checkout, for the order total.",
	})

	KeyAdminQueueVoidReason = key("admin.queue.void.reason", Message{
		ZhHant: "作廢原因",
		En:     "Reason for voiding",
	})

	KeyAdminQueueVoid = key("admin.queue.void", Message{
		ZhHant: "作廢發票",
		En:     "Void the invoice",
	})

	KeyAdminQueueVoidHint = key("admin.queue.void.hint", Message{
		ZhHant: "發票不能修改,只能作廢後重開。原因會一併申報。",
		En: "A tax invoice cannot be edited — the only correction is to void it and issue a new " +
			"one. The reason is filed along with it.",
	})

	KeyAdminQueueNotCommitted = key("admin.queue.notcommitted", Message{
		ZhHant: "訂單成立後才能開立發票。",
		En:     "An invoice can only be issued once the order is committed.",
	})

	KeyAdminDocAllowance = key("admin.doc.allowance", Message{ZhHant: "折讓", En: "Credit note"})

	KeyAdminDocInvoice = key("admin.doc.invoice", Message{ZhHant: "統一發票", En: "Tax invoice"})
)
