package i18n

var (
	KeyAdminPageOrder      = key("admin.page.order", Message{ZhHant: "訂單 %s", En: "Order %s"})
	KeyAdminPageProducts   = key("admin.page.products", Message{ZhHant: "商品", En: "Products"})
	KeyAdminPageNewProduct = key("admin.page.product.new", Message{
		ZhHant: "新增商品",
		En:     "Add a product",
	})
	KeyAdminPageReturns    = key("admin.page.returns", Message{ZhHant: "退貨申請", En: "Return requests"})
	KeyAdminPageCredit     = key("admin.page.credit", Message{ZhHant: "商店額度", En: "Store credit"})
	KeyAdminPageCoupons    = key("admin.page.coupons", Message{ZhHant: "折扣碼", En: "Coupons"})
	KeyAdminPageCampaigns  = key("admin.page.campaigns", Message{ZhHant: "限時活動", En: "Campaigns"})
	KeyAdminPageAudit      = key("admin.page.audit", Message{ZhHant: "操作紀錄", En: "Activity log"})
	KeyAdminPageShipping   = key("admin.page.shipping", Message{ZhHant: "配送與運費", En: "Delivery and fees"})
	KeyAdminPageFAQ        = key("admin.page.faq", Message{ZhHant: "常見問題", En: "FAQ"})
	KeyAdminPageHero       = key("admin.page.hero", Message{ZhHant: "首頁主視覺", En: "Home hero"})
	KeyAdminPageTaxonomy   = key("admin.page.taxonomy", Message{ZhHant: "品牌與分類", En: "Brands and categories"})
	KeyAdminPageReports    = key("admin.page.reports", Message{ZhHant: "報表", En: "Reports"})
	KeyAdminPageQuestions  = key("admin.page.questions", Message{ZhHant: "顧客提問", En: "Customer questions"})
	KeyAdminPageHealth     = key("admin.page.health", Message{ZhHant: "背景作業", En: "Background work"})
	KeyAdminPageTiers      = key("admin.page.tiers", Message{ZhHant: "會員等級", En: "Membership tiers"})
	KeyAdminPageReviews    = key("admin.page.reviews", Message{ZhHant: "顧客評價", En: "Customer reviews"})
	KeyAdminPageMessages   = key("admin.page.messages", Message{ZhHant: "聯絡訊息", En: "Contact messages"})
	KeyAdminPageNewsletter = key("admin.page.newsletter", Message{ZhHant: "電子報", En: "Newsletter"})
	KeyAdminPageCustomers  = key("admin.page.customers", Message{ZhHant: "顧客", En: "Customers"})
	KeyAdminPageWarranty   = key("admin.page.warranty", Message{ZhHant: "保固查詢", En: "Warranty lookup"})
	KeyAdminPageStaff      = key("admin.page.staff", Message{
		ZhHant: "人員與兩階段驗證",
		En:     "Staff and two-factor",
	})
	KeyAdminPageTwoFactor = key("admin.page.twofactor", Message{ZhHant: "兩階段驗證", En: "Two-factor"})
	KeyAdminPageDashboard = key("admin.page.dashboard", Message{ZhHant: "後台", En: "Back office"})
	KeyAdminPageOrderList = key("admin.page.orderlist", Message{ZhHant: "訂單管理", En: "Order management"})
	KeyAdminPageStockList = key("admin.page.stocklist", Message{ZhHant: "庫存管理", En: "Stock management"})

	KeyAdminNotFoundTitle = key("admin.notfound.title", Message{ZhHant: "找不到頁面", En: "Page not found"})
	KeyAdminNotFoundHead  = key("admin.notfound.head", Message{ZhHant: "找不到這個頁面", En: "No such page"})
	KeyAdminNotFoundBody  = key("admin.notfound.body", Message{
		ZhHant: "這個網址目前沒有對應的內容。",
		En:     "Nothing lives at this address.",
	})
	KeyAdminNoOrderTitle = key("admin.noorder.title", Message{ZhHant: "找不到訂單", En: "Order not found"})
	KeyAdminNoOrderHead  = key("admin.noorder.head", Message{ZhHant: "找不到這筆訂單", En: "No such order"})
	KeyAdminNoOrderBody  = key("admin.noorder.body", Message{
		ZhHant: "訂單編號不存在。",
		En:     "That order number does not exist.",
	})
	KeyAdminErrorTitle = key("admin.error.title", Message{ZhHant: "暫時無法處理", En: "Temporarily unavailable"})
	KeyAdminErrorBody  = key("admin.error.body", Message{ZhHant: "請稍後再試。", En: "Please try again shortly."})
	KeyAdminFaultTitle = key("admin.fault.title", Message{ZhHant: "發生錯誤", En: "Something went wrong"})
	KeyAdminFaultHead  = key("admin.fault.head", Message{ZhHant: "系統發生錯誤", En: "A system error"})
	KeyAdminFaultBody  = key("admin.fault.body", Message{
		ZhHant: "請稍後再試一次。",
		En:     "Please try again in a moment.",
	})

	KeyAdminBadForm = key("admin.badform", Message{
		ZhHant: "400 表單無法解析",
		En:     "400 that form could not be read",
	})
)

var (
	KeyAdminNoticeOK      = key("admin.notice.ok", Message{ZhHant: "已更新。", En: "Saved."})
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
		ZhHant: "這筆退貨已經核准,但退款沒有完成。退款紀錄已經留下,請確認 Stripe 後台再處理一次 —— " +
			"核准本身不需要、也無法重做。",
		En: "This return is approved, but the refund did not complete. Its record has been written " +
			"either way — check the Stripe dashboard before running it again. The approval itself " +
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
	KeyAdminNoticeClosed   = key("admin.notice.closed", Message{ZhHant: "退貨已結案。", En: "Return closed."})
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
	KeyAdminNoticeVoided   = key("admin.notice.voided", Message{
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

var (
	KeyTOTPWrongCode = key("twofactor.wrongcode", Message{
		ZhHant: "驗證碼不正確,或是已經用過了。請看驗證器上目前的那一組。",
		En: "That code is wrong, or it has already been used. " +
			"Use the one your authenticator is showing now.",
	})
	KeyTOTPWrongSecret = key("twofactor.wrongsecret", Message{
		ZhHant: "驗證碼不正確。請確認驗證器裡的祕密字串和畫面上的一致。",
		En:     "That code is wrong. Check that the secret in your authenticator matches the one on screen.",
	})
	KeyTOTPNoKey = key("twofactor.nokey", Message{
		ZhHant: "這個環境沒有設定加密金鑰,無法啟用兩階段驗證。",
		En:     "This deployment has no encryption key set, so two-factor cannot be enabled.",
	})
	KeyTOTPNoKeyNotice = key("twofactor.nokey.notice", Message{
		ZhHant: "GOEN_TOTP_KEY 沒有設定,兩階段驗證目前無法啟用。",
		En:     "GOEN_TOTP_KEY is not set, so two-factor cannot be enabled here.",
	})
	KeyTOTPAlreadyEnrolled = key("twofactor.enrolled", Message{
		ZhHant: "這個帳號已經完成兩階段驗證設定。要換一支手機,請另一位管理者先在 /admin/staff 移除,再重新設定。",
		En: "This account already has two-factor set up. To move to a new phone, ask another " +
			"administrator to remove it at /admin/staff first, then enrol again.",
	})
	// A success the admin has to relay, not a refusal. The address already had
	// an account that had never proved the mailbox, so whatever password it
	// carried is gone — otherwise promoting it would hand the back office to
	// whoever registered the address first.
	KeyStaffCredentialCleared = key("staff.cleared", Message{
		ZhHant: "已加入。這個地址原本就有一個尚未驗證的帳號,舊密碼與登入狀態都已清除 —— " +
			"請對方用「忘記密碼」設定新密碼,那是唯一能證明信箱是他的路徑。",
		En: "Added. That address already had an account which had never proved the mailbox, " +
			"so its old password and sign-ins were cleared — ask them to set a password through " +
			"“Forgot password”, which is the one path that proves the mailbox is theirs.",
	})
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
	KeyStaffSelf = key("staff.self", Message{
		ZhHant: "不能對自己的帳號做這件事 —— 解除自己的兩階段驗證等於沒有第二因素," +
			"移除自己的權限會把商店鎖在門外。請另一位管理員操作。",
		En: "You cannot do this to your own account — dropping your own second factor leaves you " +
			"without one, and revoking your own access locks the shop out. Ask another administrator.",
	})
	KeyStaffLastAdmin = key("staff.lastadmin", Message{
		ZhHant: "這是最後一位管理員。移除之後就沒有人能再新增管理員了。",
		En:     "This is the last administrator. Remove them and nobody is left who can add one back.",
	})
	KeyStaffInvalid = key("staff.invalid", Message{
		ZhHant: "資料不完整,或這個帳號沒有可以解除的兩階段驗證。",
		En:     "Something is missing, or that account has no two-factor credential to remove.",
	})
)
