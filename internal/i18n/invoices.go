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

	// The endpoint goen calls is ECPay's paper-allowance one; goen records the
	// allowance as filed and has no step that collects the buyer's agreement.
	KeyAdminQueueAllowanceOnline = key("admin.queue.allowance.online", Message{
		ZhHant: "綠界會以 Email 請顧客確認這筆折讓，顧客在 72 小時內點選同意後才成立；綠界記錄這次同意，goen 也保存一份。",
		En: "ECPay e-mails the customer to agree to this credit note, which takes effect only if they " +
			"agree within 72 hours; ECPay records the agreement and goen keeps a copy.",
	})

	KeyAdminQueueAllowanceAwaiting = key("admin.queue.allowance.awaiting", Message{
		ZhHant: "已寄出折讓確認信，等待顧客在 %s 前確認。",
		En:     "The credit note was e-mailed to the customer to agree to by %s.",
	})

	KeyAdminQueueAllowanceUnconfirmed = key("admin.queue.allowance.unconfirmed", Message{
		ZhHant: "顧客未在 72 小時內確認折讓。",
		En:     "The customer did not agree to the credit note within 72 hours.",
	})

	KeyAdminQueueAllowanceAmountHeld = key("admin.queue.allowance.amountheld", Message{
		ZhHant: "綠界表示先前未確認的折讓仍保留這筆金額，這次重寄沒有開立任何折讓。",
		En: "ECPay says an earlier credit note the customer never agreed to still holds this amount, " +
			"so the resend filed nothing.",
	})

	KeyAdminQueueNoInvoicing = key("admin.queue.noinvoicing", Message{
		ZhHant: "尚未啟用電子發票，這裡無法開立發票。",
		En:     "E-invoicing is not set up for this shop, so no invoice can be issued from here.",
	})

	KeyAdminQueueVoided = key("admin.queue.voided", Message{ZhHant: "（已作廢）", En: "(voided)"})

	KeyAdminQueueRandomCode = key("admin.queue.randomcode", Message{
		ZhHant: "隨機碼 %s",
		En:     "Random code %s",
	})

	KeyAdminQueueIssue = key("admin.queue.issue", Message{
		ZhHant: "開立發票",
		En:     "Issue the invoice",
	})

	KeyAdminQueueIssueHint = key("admin.queue.issue.hint", Message{
		ZhHant: "依結帳時選的 %s 開立，金額為訂單總計。",
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
		ZhHant: "發票不能修改，只能作廢後重開。原因會一併申報。",
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
		ZhHant: "發票已作廢。要重開的話，現在可以再開一張。",
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

	KeyAdminNoticeAllowSent = key("admin.notice.allowsent", Message{
		ZhHant: "已寄出折讓確認信給顧客，顧客同意後折讓才成立。",
		En:     "The credit note was e-mailed to the customer; it takes effect once they agree.",
	})

	// The two refusals a 折讓 has of its own. invoicefailed talks about 統編 and
	// mobile barcodes, which a 折讓 form does not collect.
	KeyAdminNoticeAllowTooMuch = key("admin.notice.allowtoomuch", Message{
		ZhHant: "目前沒有尚未折讓的整數元退款；可能已由另一個請求完成。",
		En:     "No whole-dollar refunded amount remains unrelieved; another request may have completed it.",
	})

	KeyAdminNoticeAllowClaimed = key("admin.notice.allowclaimed", Message{
		ZhHant: "這筆退款的折讓已經開立或正在處理中，請先到綠界確認。",
		En:     "A credit note for this refund is already filed or in flight; check ECPay first.",
	})

	// A void form collects a reason, not a 統編. The Issue sentence would send
	// staff to edit checkout tax ids that are not on this page.
	KeyAdminNoticeVoidReason = key("admin.notice.voidreason", Message{
		ZhHant: "作廢需要填寫原因。",
		En:     "A void needs a reason.",
	})

	KeyAdminNoticeVoidFailed = key("admin.notice.voidfailed", Message{
		ZhHant: "加值中心拒絕了這次作廢，詳細原因在伺服器紀錄裡。請先到綠界確認。",
		En: "The e-invoice provider refused the void; the reason is in the server log. " +
			"Check ECPay first.",
	})

	// Shown when the cancellation could not correct the invoice itself: the
	// void window has passed, ECPay refused or has not answered the void, or no
	// 加值中心 is configured.
	KeyAdminNoticeCancelInvoice = key("admin.notice.cancelinvoice", Message{
		ZhHant: "退款已完成，但發票仍有未沖回的金額，或開立、作廢、折讓的結果尚未確認。請在本頁發票區作廢或開立折讓，完成後按「繼續退款」取消訂單。",
		En: "The refund has landed, but an invoice still has an unrelieved amount or an issue, void or " +
			"allowance is unresolved. Void it or file an allowance in this page's invoice section, then " +
			"press Resume the refund to cancel the order.",
	})

	KeyAdminNoticeAllowFailed = key("admin.notice.allowfailed", Message{
		ZhHant: "加值中心拒絕了這次折讓，詳細原因在伺服器紀錄裡。請先到綠界確認。",
		En: "The e-invoice provider refused the credit note; the reason is in the server log. " +
			"Check ECPay first.",
	})

	KeyAdminNoticeInvoiceFailed = key("admin.notice.invoicefailed", Message{
		ZhHant: "加值中心拒絕了這次操作，詳細原因在伺服器紀錄裡。常見的是統編格式或載具號碼不正確。",
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
	KeyAdminInvoiceCarrierMember = key("admin.carrier.member", Message{ZhHant: "會員載具", En: "Member carrier"})

	KeyAdminInvoiceCarrierMobileBarcode = key("admin.carrier.mobile", Message{
		ZhHant: "手機條碼載具 %s",
		En:     "Mobile barcode carrier %s",
	})

	KeyAdminInvoiceDonate = key("admin.invoice.donate", Message{
		ZhHant: "捐贈發票，愛心碼 %s",
		En:     "Donated invoice, donation code %s",
	})

	KeyAdminInvoiceTaxID = key("admin.carrier.taxid", Message{
		ZhHant: "公司統編 %s",
		En:     "Company tax number %s",
	})
)
