package i18n

var (
	KeyAdminNoticeCancelInvoice = key("admin.notice.cancelinvoice", Message{
		ZhHant: "尚無法取消訂單：發票仍有未沖回金額，或開立、作廢、折讓結果尚未確認。請先在本頁發票區確認結果並完成適用的更正，再取消訂單。",
		En:     "This order cannot be cancelled while an invoice has an unrelieved amount or an issue, void, or allowance remains unresolved. Confirm the result and complete the appropriate correction in this page's invoice section, then cancel the order.",
	})
	KeyAdminNoticeInvoiceCancelled = key("admin.notice.invoicecancelled", Message{
		ZhHant: "此訂單已取消，無法再開立發票。",
		En:     "This order is cancelled and cannot receive a new invoice.",
	})
)
