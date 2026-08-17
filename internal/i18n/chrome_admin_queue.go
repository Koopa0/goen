package i18n

var (
	KeyAdminQueueNavLabel = key("admin.queue.nav", Message{
		ZhHant: "後台導覽",
		En:     "Back-office navigation",
	})
	KeyAdminQueueOverview  = key("admin.queue.overview", Message{ZhHant: "總覽", En: "Overview"})
	KeyAdminQueueOrders    = key("admin.queue.orders", Message{ZhHant: "訂單", En: "Orders"})
	KeyAdminQueueStock     = key("admin.queue.stock", Message{ZhHant: "庫存", En: "Stock"})
	KeyAdminQueueReturns   = key("admin.queue.returns", Message{ZhHant: "退貨", En: "Returns"})
	KeyAdminQueueWarranty  = key("admin.queue.warranty", Message{ZhHant: "保固", En: "Warranty"})
	KeyAdminQueueQuestions = key("admin.queue.questions", Message{ZhHant: "提問", En: "Questions"})
	KeyAdminQueueReviews   = key("admin.queue.reviews", Message{ZhHant: "評價", En: "Reviews"})
	KeyAdminQueueTaxonomy  = key("admin.queue.taxonomy", Message{
		ZhHant: "品牌分類",
		En:     "Brands and categories",
	})
	KeyAdminQueueHomePage  = key("admin.queue.homepage", Message{ZhHant: "首頁", En: "Home page"})
	KeyAdminQueueCampaigns = key("admin.queue.campaigns", Message{ZhHant: "活動", En: "Campaigns"})
	KeyAdminQueueCredit    = key("admin.queue.credit", Message{ZhHant: "額度", En: "Credit"})
	KeyAdminQueueShipping  = key("admin.queue.shipping", Message{ZhHant: "運費", En: "Delivery fees"})
	KeyAdminQueueStaff     = key("admin.queue.staff", Message{ZhHant: "人員", En: "Staff"})
)

var (
	KeyAdminQueueDashboardTitle = key("admin.queue.dashboard.title", Message{
		ZhHant: "後台總覽",
		En:     "Back-office overview",
	})
	KeyAdminQueueStatPending = key("admin.queue.stat.pending", Message{
		ZhHant: "待付款訂單",
		En:     "Orders awaiting payment",
	})
	KeyAdminQueueStatLowStock = key("admin.queue.stat.lowstock", Message{
		ZhHant: "低庫存品項",
		En:     "Low-stock items",
	})
	KeyAdminQueueStatActive = key("admin.queue.stat.active", Message{
		ZhHant: "上架商品",
		En:     "Published products",
	})
	KeyAdminQueueStatMessages = key("admin.queue.stat.messages", Message{
		ZhHant: "待回覆訊息",
		En:     "Messages awaiting a reply",
	})
	KeyAdminQueueRestockHead = key("admin.queue.restock", Message{
		ZhHant: "需要補貨",
		En:     "Needs restocking",
	})
	KeyAdminQueueSellableHint = key("admin.queue.sellable", Message{
		ZhHant: "「可售」是庫存減去安全庫存,也就是資料庫實際允許賣出的數量。",
		En: "Sellable is stock minus safety stock — the number the database will actually " +
			"let the shop sell.",
	})
	KeyAdminQueueAllLowStock = key("admin.queue.alllow", Message{
		ZhHant: "查看全部低庫存",
		En:     "See every low-stock item",
	})
)

var (
	KeyAdminQueueSearchLabel = key("admin.queue.search.label", Message{
		ZhHant: "搜尋訂單",
		En:     "Search orders",
	})
	KeyAdminQueueSearchPlaceholder = key("admin.queue.search.placeholder", Message{
		ZhHant: "訂單編號、收件人姓名或 Email",
		En:     "Order number, recipient name or email",
	})
	KeyAdminQueueClear      = key("admin.queue.clear", Message{ZhHant: "清除", En: "Clear"})
	KeyAdminQueueSearchNote = key("admin.queue.search.note", Message{
		ZhHant: "搜尋「%s」 —— 編號是完整比對,姓名和 Email 從開頭比對。搜尋時不套用上面的狀態篩選。",
		En: "Searching for %q — an order number matches exactly, a name or email address from " +
			"the start. A search does not apply the status filter above.",
	})
	KeyAdminQueueSearchShort = key("admin.queue.search.short", Message{
		ZhHant: "搜尋字串太短,至少要兩個字。下面是一般的訂單列表。",
		En: "That search is too short — two characters at least. What follows is the ordinary " +
			"order queue.",
	})
	KeyAdminQueueEmpty = key("admin.queue.empty", Message{
		ZhHant: "這個狀態沒有訂單",
		En:     "No orders in this state",
	})
	KeyAdminQueueCommitted = key("admin.queue.committed", Message{ZhHant: "已成交", En: "Committed"})
)

var (
	KeyAdminQueueItems    = key("admin.queue.items", Message{ZhHant: "商品", En: "Items"})
	KeyAdminQueueDelivery = key("admin.queue.delivery", Message{
		ZhHant: "收件資訊",
		En:     "Delivery details",
	})
	KeyAdminQueueAddress      = key("admin.queue.address", Message{ZhHant: "地址", En: "Address"})
	KeyAdminQueueDeliveryHint = key("admin.queue.delivery.hint", Message{
		ZhHant: "出貨之後就改不了 —— 那時候包裹已經寄出,改紀錄只會讓紀錄和事實對不上。" +
			"操作紀錄只會記下「改了收件資訊」,不會記下地址本身:顧客刪除帳號時清不到操作紀錄。",
		En: "This can no longer be changed once the parcel has gone — it is already on its way, " +
			"and editing the record would only make the record disagree with where it went. " +
			"The activity log records that the delivery details changed and never the address " +
			"itself: erasing a customer's account cannot reach the activity log.",
	})
	KeyAdminQueueDeliverySave = key("admin.queue.delivery.save", Message{
		ZhHant: "更新收件資訊",
		En:     "Update the delivery details",
	})
	KeyAdminQueueDispatch   = key("admin.queue.dispatch", Message{ZhHant: "出貨", En: "Dispatch"})
	KeyAdminQueueDelivered  = key("admin.queue.delivered", Message{ZhHant: "%s 送達", En: "Delivered %s"})
	KeyAdminQueueDispatched = key("admin.queue.dispatched", Message{
		ZhHant: "%s 出貨",
		En:     "Dispatched %s",
	})
	KeyAdminQueueTimeline = key("admin.queue.timeline", Message{
		ZhHant: "訂單紀錄",
		En:     "Order history",
	})
	KeyAdminQueueCustomerNote = key("admin.queue.customernote", Message{
		ZhHant: "顧客備註",
		En:     "Customer's note",
	})
	KeyAdminQueueStaffNote = key("admin.queue.staffnote", Message{
		ZhHant: "內部備註",
		En:     "Internal note",
	})
	KeyAdminQueueStaffNoteHint = key("admin.queue.staffnote.hint", Message{
		ZhHant: "只有後台看得到。顧客刪除帳號時不會被清除,所以不要放顧客寫的內容。",
		En: "Only the back office sees this. It is not cleared when a customer erases their " +
			"account, so do not put anything the customer wrote in it.",
	})
	KeyAdminQueueNoteSave = key("admin.queue.note.save", Message{
		ZhHant: "儲存備註",
		En:     "Save the note",
	})
)

var (
	KeyAdminQueueTracking = key("admin.queue.tracking", Message{
		ZhHant: "查詢編號",
		En:     "Tracking number",
	})
	KeyAdminQueueRemaining = key("admin.queue.remaining", Message{
		ZhHant: "%s(剩 %s)",
		En:     "%s (%s left)",
	})
	KeyAdminQueueShortHold = key("admin.queue.shorthold", Message{
		ZhHant: "這一項只保留了 %s 件,超過的部分無法出貨。",
		En: "Only %s of this line are still held in stock, and anything beyond that cannot be " +
			"dispatched.",
	})
	KeyAdminQueueDispatchHint = key("admin.queue.dispatch.hint", Message{
		ZhHant: "出貨會同時記錄配送資訊、扣除保留的庫存,並寫入訂單紀錄。數量可以少於剩餘,剩下的之後再出一次。",
		En: "Dispatching records the delivery details, consumes the stock this order is holding " +
			"and writes to the order history, all in one go. A quantity may be lower than what " +
			"is left, and the rest goes out as another parcel later.",
	})
	KeyAdminQueueNextStatus = key("admin.queue.nextstatus", Message{
		ZhHant: "變更狀態",
		En:     "Change the status",
	})
	KeyAdminQueueStatusSave = key("admin.queue.status.save", Message{
		ZhHant: "更新狀態",
		En:     "Update the status",
	})
	KeyAdminQueueNextHint = key("admin.queue.next.hint", Message{
		ZhHant: "只列出資料庫允許的下一步。未付款的訂單無法進入備貨。",
		En: "Only the next steps the database will accept are listed. An unpaid order cannot " +
			"move into picking.",
	})
	KeyAdminQueueFinal = key("admin.queue.final", Message{
		ZhHant: "這筆訂單已經是最終狀態。",
		En:     "This order is already in a final state.",
	})
)

var (
	KeyAdminQueueNoInvoicing = key("admin.queue.noinvoicing", Message{
		ZhHant: "尚未設定加值中心,無法開立發票。設定 GOEN_ECPAY_MERCHANT_ID 後才會開放。",
		En: "No e-invoice provider is configured, so nothing can be issued. Setting " +
			"GOEN_ECPAY_MERCHANT_ID is what turns this on.",
	})
	KeyAdminQueueVoided     = key("admin.queue.voided", Message{ZhHant: "(已作廢)", En: "(voided)"})
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
)

var (
	KeyAdminQueueStockLead = key("admin.queue.stock.lead", Message{
		ZhHant: "庫存只能透過調整寫入,每次調整都會留下一筆異動紀錄。",
		En: "Stock can only be written through an adjustment, and every adjustment leaves a " +
			"movement in the ledger.",
	})
	KeyAdminQueueLowOnly = key("admin.queue.lowonly", Message{
		ZhHant: "僅低庫存",
		En:     "Low stock only",
	})
	KeyAdminQueueNoVariants = key("admin.queue.novariants", Message{
		ZhHant: "沒有符合的品項",
		En:     "No items match",
	})
	KeyAdminQueueColStock = key("admin.queue.col.stock", Message{
		ZhHant: "庫存 / 安全 / 可售",
		En:     "Stock / safety / sellable",
	})
	KeyAdminQueueColPrice    = key("admin.queue.col.price", Message{ZhHant: "價格", En: "Price"})
	KeyAdminQueueViewProduct = key("admin.queue.viewproduct", Message{
		ZhHant: "看商品頁",
		En:     "See the product page",
	})
	KeyAdminQueueVariantOff = key("admin.queue.variantoff", Message{
		ZhHant: "已停用",
		En:     "Switched off",
	})
	KeyAdminQueuePrice        = key("admin.queue.price", Message{ZhHant: "售價", En: "Selling price"})
	KeyAdminQueueComparePrice = key("admin.queue.compareprice", Message{
		ZhHant: "原價",
		En:     "Compare-at price",
	})
	KeyAdminQueuePriceSave = key("admin.queue.price.save", Message{
		ZhHant: "改價",
		En:     "Change the price",
	})
	KeyAdminQueueAdjust    = key("admin.queue.adjust", Message{ZhHant: "調整", En: "Adjust"})
	KeyAdminQueueAdjustQty = key("admin.queue.adjust.qty", Message{
		ZhHant: "調整數量",
		En:     "Adjustment quantity",
	})
)
