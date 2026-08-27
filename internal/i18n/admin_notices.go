package i18n

var (
	KeyAdminNoticeOK = key("admin.notice.ok", Message{ZhHant: "已更新。", En: "Saved."})

	KeyAdminNoticeRefused = key("admin.notice.refused", Message{
		ZhHant: "資料庫拒絕了這個變更。可能是狀態流程不允許,或會違反庫存與活動規則。",
		En: "The database refused that change. Either the status move is not a legal one, " +
			"or it would break a stock or campaign rule.",
	})

	KeyAdminNoticeShipped = key("admin.notice.shipped", Message{
		ZhHant: "已出貨。配送資訊與庫存都已記錄。",
		En:     "Dispatched. The delivery details and the stock movement are both recorded.",
	})

	KeyAdminNoticeTooLate = key("admin.notice.toolate", Message{
		ZhHant: "這筆訂單已經出貨,收件資訊改不了了。包裹已經寄出,改紀錄只會讓紀錄和事實對不上。",
		En: "This order has shipped, so the delivery details can no longer be changed. " +
			"The parcel is already on its way; editing the record would only make it disagree with where it went.",
	})

	KeyAdminNoticeNeeds = key("admin.notice.needs", Message{
		ZhHant: "請填寫物流商與查詢編號。",
		En:     "A carrier and a tracking number are both needed.",
	})

	KeyAdminNoticeTooBig = key("admin.notice.toobig", Message{
		ZhHant: "圖片太大了,請用 8 MB 以內的檔案。",
		En:     "That image is too large. Use a file under 8 MB.",
	})

	KeyAdminNoticeNotImage = key("admin.notice.notimage", Message{
		ZhHant: "這個檔案不是可以辨識的圖片。支援 JPEG、PNG、GIF 與 WebP。",
		En:     "That file is not an image goen can decode. JPEG, PNG, GIF and WebP are supported.",
	})

	KeyAdminNoticeUploadFailed = key("admin.notice.uploadfailed", Message{
		ZhHant: "圖片上傳失敗,請再試一次。",
		En:     "The upload did not finish. Please try again.",
	})

	KeyAdminNoticeInUse = key("admin.notice.inuse", Message{
		ZhHant: "還有商品或子分類在用它,先把那些移到別的地方再刪。",
		En:     "Products or child categories still point at it. Move those elsewhere first.",
	})

	KeyAdminNoticeAttachRefused = key("admin.notice.attachrefused", Message{
		ZhHant: "這張圖片已經在這個商品上了。",
		En:     "That image is already on this product.",
	})

	KeyAdminNoticeNoAlt = key("admin.notice.noalt", Message{
		ZhHant: "請填寫圖片說明文字 —— 讀螢幕的人靠它知道圖裡是什麼。",
		En:     "Alt text is required — it is how somebody using a screen reader knows what the picture shows.",
	})

	KeyAdminNoticeNoDiscount = key("admin.notice.nodiscount", Message{
		ZhHant: "這個商品沒有標示原價,無法加入活動。先在商品頁設定原價再試一次。",
		En: "This product has no compare-at price, so nothing on it is marked down and a campaign " +
			"cannot feature it. Set one on the product page and try again.",
	})

	// The DECISION stands: it is committed before any money moves, so that two
	// staff members deciding at once cannot both pay. What is outstanding here
	// is the payment, and saying "the refund failed" without saying the return
	// is already approved would send somebody looking for a decision to retake.
	KeyAdminNoticeRefundFailed = key("admin.notice.refundfailed", Message{
		ZhHant: "這筆退貨已經核准,但退款沒有完成。退款紀錄已經留下,請確認 Stripe 後台後使用退貨列上的「重新退款」—— " +
			"核准本身不需要、也無法重做。",
		En: "This return is approved, but the refund did not complete. Its record has been written " +
			"either way — check the Stripe dashboard, then use Send the refund again on its row. The approval itself " +
			"neither needs nor allows redoing.",
	})

	KeyAdminNoticeReceived = key("admin.notice.received", Message{
		ZhHant: "進貨已入庫,帳本上記的是「進貨」而不是「人工調整」。",
		En:     "Received. The ledger records this as a goods receipt, not as a manual correction.",
	})

	KeyAdminNoticeBadQty = key("admin.notice.badqty", Message{
		ZhHant: "進貨數量要是正整數。要往下修正數字請用「調整」—— 進貨是有東西進來,調整是數字算錯了,帳本分得出這兩件事。",
		En: "A receipt quantity is a positive whole number. To correct a count downward use Adjust — " +
			"a receipt is goods arriving and an adjustment is a number that was wrong, and the ledger keeps them apart.",
	})

	KeyAdminNoticeInspected = key("admin.notice.inspected", Message{
		ZhHant: "驗貨已記錄,可再販售的數量已經入庫。",
		En:     "Inspection recorded. Whatever is sellable again is back on the shelf.",
	})

	KeyAdminNoticeClosed = key("admin.notice.closed", Message{ZhHant: "退貨已結案。", En: "Return closed."})

	KeyAdminNoticeBadCount = key("admin.notice.badcount", Message{
		ZhHant: "數量填寫有問題:入庫數不能超過實際收到的數量,實際收到也不能超過申請退回的數量。",
		En: "Those quantities do not work: what goes back on the shelf cannot exceed what arrived, " +
			"and what arrived cannot exceed what the customer asked to return.",
	})

	KeyAdminNoticeBadParcel = key("admin.notice.badparcel", Message{
		ZhHant: "出貨數量填寫有問題:每一項不能超過還沒出貨的數量,也不能超過這筆訂單保留的庫存。",
		En: "Those quantities do not work: no line can exceed what is still outstanding, or what this " +
			"order is holding in stock.",
	})

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

	// A 折讓 the shop just filed with the 財政部. Confirmed in words rather than
	// left to the documents list: a tax filing is the one thing a staff member
	// should be told happened.
	// The newsletter's three, and the product form's. Each was a redirect
	// answering 303 with a parameter that rendered nothing.
	KeyAdminNoticeSaved = key("admin.notice.saved", Message{
		ZhHant: "草稿已儲存。",
		En:     "The draft has been saved.",
	})

	KeyAdminNoticeSent = key("admin.notice.sent", Message{
		ZhHant: "電子報已送出。",
		En:     "The newsletter has been sent.",
	})

	KeyAdminNoticeAlready = key("admin.notice.already", Message{
		ZhHant: "這期電子報已經寄出過了。",
		En:     "That issue has already been sent.",
	})

	KeyAdminNoticeSpecFailed = key("admin.notice.specfailed", Message{
		ZhHant: "規格表沒有存成功，請確認欄位長度。",
		En:     "The specification was not saved; check the field lengths.",
	})

	KeyAdminNoticeAllowed = key("admin.notice.allowed", Message{
		ZhHant: "折讓已開立。",
		En:     "The credit note has been filed.",
	})

	KeyAdminNoticeBadAmount = key("admin.notice.badamount", Message{
		ZhHant: "折讓金額必須是大於零的整數（元）。",
		En:     "A credit note amount must be a whole number of dollars above zero.",
	})

	// The two refusals a 折讓 has of its own. invoicefailed talks about 統編 and
	// carrier codes, which is right for issuing and sends a staff member to the
	// wrong fields here.
	KeyAdminNoticeAllowTooMuch = key("admin.notice.allowtoomuch", Message{
		ZhHant: "折讓金額超過已退給客人的金額，或超過尚未折讓的部分。",
		En:     "That is more than has gone back to the customer, or more than is left to relieve.",
	})

	KeyAdminNoticeAllowClaimed = key("admin.notice.allowclaimed", Message{
		ZhHant: "這筆退款的折讓已經開立或正在處理中，請先到綠界確認。",
		En:     "A credit note for this refund is already filed or in flight; check ECPay first.",
	})

	// /admin/health's own two.
	KeyAdminNoticeReconciled = key("admin.notice.reconciled", Message{
		ZhHant: "已記錄為處理完成。",
		En:     "Recorded as handled.",
	})

	KeyAdminNoticeNotFlagged = key("admin.notice.notflagged", Message{
		ZhHant: "這筆事件已經處理過了。",
		En:     "That event has already been dealt with.",
	})

	KeyAdminNoticeInvoiceFailed = key("admin.notice.invoicefailed", Message{
		ZhHant: "加值中心拒絕了這次操作,詳細原因在伺服器紀錄裡。常見的是統編格式或載具號碼不正確。",
		En: "The e-invoice provider refused that operation; the reason is in the server log. " +
			"Usually it is a malformed business tax number or carrier code.",
	})

	KeyAdminNoticeCreditGranted = key("admin.notice.credit.granted", Message{
		ZhHant: "已發放。這位顧客目前的餘額是 %s。",
		En:     "Granted. This customer's balance is now %s.",
	})
)
