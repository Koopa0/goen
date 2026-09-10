package i18n

var (
	KeyAdminQueueAllowance = key("admin.queue.allowance", Message{
		ZhHant: "開立折讓單",
		En:     "File a credit note",
	})

	// A void is for an invoice that should not exist; a 折讓 is for one that
	// should exist for less.
	KeyAdminQueueAllowanceHint = key("admin.queue.allowance.hint", Message{
		ZhHant: "系統會依已實際退回且尚未折讓的金額開立 %s；金額不能由表單更改。" +
			"整張發票都不該存在時請用作廢。",
		En: "After a refund the invoice still records the whole sale. A credit note relieves " +
			"the authoritative unrelieved amount, %s; the form cannot override it. Use a void " +
			"instead when the invoice should not exist at all.",
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

var (
	KeyAdminNoticeInvoiced = key("admin.notice.invoiced", Message{ZhHant: "發票已開立。", En: "Invoice issued."})

	KeyAdminNoticeVoided = key("admin.notice.voided", Message{
		ZhHant: "發票已作廢。要重開的話,現在可以再開一張。",
		En:     "Invoice voided. A replacement can be issued now.",
	})

	KeyAdminNoticeHasInvoice = key("admin.notice.hasinvoice", Message{
		ZhHant: "這筆訂單已經有一張有效的發票了。要換一張就先作廢。",
		En:     "This order already has an active invoice. Void it first to issue another.",
	})

	KeyAdminNoticeNoInvoice = key("admin.notice.noinvoice", Message{
		ZhHant: "這筆訂單沒有可以作廢的發票。",
		En:     "This order has no invoice to void.",
	})

	// A 折讓 the shop has just filed with the 財政部.
	KeyAdminNoticeAllowed = key("admin.notice.allowed", Message{
		ZhHant: "折讓已開立。",
		En:     "The credit note has been filed.",
	})

	// The two refusals a 折讓 has of its own. invoicefailed talks about 統編 and
	// carrier codes, which are the wrong fields here.
	KeyAdminNoticeAllowTooMuch = key("admin.notice.allowtoomuch", Message{
		ZhHant: "目前沒有尚未折讓的整數元退款；可能已由另一個請求完成。",
		En:     "No whole-dollar refunded amount remains unrelieved; another request may have completed it.",
	})

	KeyAdminNoticeAllowClaimed = key("admin.notice.allowclaimed", Message{
		ZhHant: "這筆退款的折讓已經開立或正在處理中，請先到綠界確認。",
		En:     "A credit note for this refund is already filed or in flight; check ECPay first.",
	})

	KeyAdminNoticeInvoiceFailed = key("admin.notice.invoicefailed", Message{
		ZhHant: "加值中心拒絕了這次操作,詳細原因在伺服器紀錄裡。常見的是統編格式或載具號碼不正確。",
		En: "The e-invoice provider refused that operation; the reason is in the server log. " +
			"Usually it is a malformed business tax number or carrier code.",
	})

	KeyAdminNoticeInvoicePending = key("admin.notice.invoicepending", Message{
		ZhHant: "操作已安全記錄，正在與加值中心核對；若未自動完成，健康頁會顯示原因。",
		En: "The operation was recorded safely and is being reconciled with ECPay. " +
			"The health page will show it if automatic recovery cannot finish.",
	})
)

var (
	KeyAdminCarrierMember = key("admin.carrier.member", Message{ZhHant: "會員載具", En: "Member carrier"})

	KeyAdminCarrierMobile = key("admin.carrier.mobile", Message{
		ZhHant: "手機條碼載具 %s",
		En:     "Mobile barcode carrier %s",
	})

	KeyAdminCarrierTaxID = key("admin.carrier.taxid", Message{
		ZhHant: "公司統編 %s",
		En:     "Company tax number %s",
	})
)
