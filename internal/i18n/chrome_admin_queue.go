package i18n

// The order queue, one order in full, and the stock list — the three surfaces
// in internal/ui/pages/admin.templ.
//
// It also holds the back office's own NAVIGATION, which lives in that file and
// renders above every /admin page. Ten of those links carry the same word as the
// page they point at and use that page's title key rather than a second copy;
// the rest are here because the nav is deliberately shorter than the page it
// leads to (訂單 against 訂單管理, 庫存 against 庫存管理), and the test for
// splitting a word into two keys is the one chrome_admin_shared.go states: does
// English pull them apart. Where it does not — 訂單 in the nav, in the crumb and
// as the queue's own heading are one word for one thing — it is one key.

var (
	// The back office's own navigation, rendered on every /admin page.
	//
	// A nav item is not the tab title: 訂單 leads to a page titled 訂單管理, and
	// a row of twenty-four links is read by scanning rather than by reading.
	// Where the two coincide exactly the page title is reused instead.
	KeyAdminQueueNavLabel = key("admin.queue.nav", Message{
		ZhHant: "後台導覽",
		En:     "Back-office navigation",
	})
	KeyAdminQueueOverview = key("admin.queue.overview", Message{ZhHant: "總覽", En: "Overview"})
	// Three uses, one word: the nav link, the breadcrumb back to the queue, and
	// the queue's own heading.
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
	// 運費 in the nav names the SECTION that sets delivery charges; 運費 in an
	// order's summary is one figure on one order, and reuses buy.shipping.
	KeyAdminQueueShipping = key("admin.queue.shipping", Message{ZhHant: "運費", En: "Delivery fees"})
	KeyAdminQueueStaff    = key("admin.queue.staff", Message{ZhHant: "人員", En: "Staff"})
)

var (
	// The dashboard: five figures and the restock panel under them.
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
	// Says what the third column of the table under it is, because 可售 is the
	// only one of the three the database computes rather than stores.
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
	// Searching the queue. One box, and what it matches depends on what it looks
	// like — which is why the note below it says so rather than leaving somebody
	// to guess why a surname found nothing.
	KeyAdminQueueSearchLabel = key("admin.queue.search.label", Message{
		ZhHant: "搜尋訂單",
		En:     "Search orders",
	})
	KeyAdminQueueSearchPlaceholder = key("admin.queue.search.placeholder", Message{
		ZhHant: "訂單編號、收件人姓名或 Email",
		En:     "Order number, recipient name or email",
	})
	KeyAdminQueueClear = key("admin.queue.clear", Message{ZhHant: "清除", En: "Clear"})
	// A search IGNORES the status tab, and that is a decision rather than an
	// oversight: somebody on the phone wants that order, not that order if it
	// happens to be in the tab they had open.
	KeyAdminQueueSearchNote = key("admin.queue.search.note", Message{
		ZhHant: "搜尋「%s」 —— 編號是完整比對,姓名和 Email 從開頭比對。搜尋時不套用上面的狀態篩選。",
		En: "Searching for %q — an order number matches exactly, a name or email address from " +
			"the start. A search does not apply the status filter above.",
	})
	// Below the floor the page says so instead of quietly showing the queue,
	// which is a page that looks like an answer and is not.
	KeyAdminQueueSearchShort = key("admin.queue.search.short", Message{
		ZhHant: "搜尋字串太短,至少要兩個字。下面是一般的訂單列表。",
		En: "That search is too short — two characters at least. What follows is the ordinary " +
			"order queue.",
	})
	KeyAdminQueueEmpty = key("admin.queue.empty", Message{
		ZhHant: "這個狀態沒有訂單",
		En:     "No orders in this state",
	})
	// committed_orders, not "paid": a fully store-credited order owes nothing,
	// has no payment row at all, and is still the shop taking the work on. The
	// status badge beside it already says where the order is.
	KeyAdminQueueCommitted = key("admin.queue.committed", Message{ZhHant: "已成交", En: "Committed"})
)

var (
	// One order. 商品 here heads the LINES of this order rather than the
	// catalogue, which is where English pulls it away from admin.page.products.
	KeyAdminQueueItems    = key("admin.queue.items", Message{ZhHant: "商品", En: "Items"})
	KeyAdminQueueDelivery = key("admin.queue.delivery", Message{
		ZhHant: "收件資訊",
		En:     "Delivery details",
	})
	// The whole destination as one line. The street FIELD of the correction form
	// is field.street, because those are the address and one part of it.
	KeyAdminQueueAddress = key("admin.queue.address", Message{ZhHant: "地址", En: "Address"})
	// Both halves are on the form itself rather than left to fail at the write:
	// the cut-off is in the UPDATE's own WHERE clause, and the audit row names
	// the fields that changed and never their values.
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
	// One word for the parcels heading and for the button that creates one.
	KeyAdminQueueDispatch   = key("admin.queue.dispatch", Message{ZhHant: "出貨", En: "Dispatch"})
	KeyAdminQueueDelivered  = key("admin.queue.delivered", Message{ZhHant: "%s 送達", En: "Delivered %s"})
	KeyAdminQueueDispatched = key("admin.queue.dispatched", Message{
		ZhHant: "%s 出貨",
		En:     "Dispatched %s",
	})
	// The back office's timeline answers WHO, which is the whole reason it is a
	// different query from the customer's.
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
	// Dispatching a parcel. An order ships in as many parcels as it takes, so
	// every figure here is per LINE and the quantity box defaults to what is
	// still outstanding.
	KeyAdminQueueTracking = key("admin.queue.tracking", Message{
		ZhHant: "查詢編號",
		En:     "Tracking number",
	})
	KeyAdminQueueRemaining = key("admin.queue.remaining", Message{
		ZhHant: "%s(剩 %s)",
		En:     "%s (%s left)",
	})
	// A line holding less stock than it owes cannot be fully dispatched: part of
	// the hold went back on the shelf, and shipping it would post no movement at
	// all. Said on the form rather than left to fail at the write.
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
	// orders_check_transition is what decides the list, not this page.
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
	// The 統一發票 panel. 加值中心 is the certified e-invoice intermediary the
	// document is filed through — ECPay here — and the English names the role
	// rather than transliterating it, so a reader knows what is missing.
	KeyAdminQueueNoInvoicing = key("admin.queue.noinvoicing", Message{
		ZhHant: "尚未設定加值中心,無法開立發票。設定 GOEN_ECPAY_MERCHANT_ID 後才會開放。",
		En: "No e-invoice provider is configured, so nothing can be issued. Setting " +
			"GOEN_ECPAY_MERCHANT_ID is what turns this on.",
	})
	KeyAdminQueueVoided = key("admin.queue.voided", Message{ZhHant: "(已作廢)", En: "(voided)"})
	// The four digits printed beside the invoice number, which a customer needs
	// to look the document up on the tax authority's platform and a void needs
	// alongside the number.
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
	// The reason is filed with the provider, so it is not a private note.
	KeyAdminQueueVoidHint = key("admin.queue.void.hint", Message{
		ZhHant: "發票不能修改,只能作廢後重開。原因會一併申報。",
		En: "A tax invoice cannot be edited — the only correction is to void it and issue a new " +
			"one. The reason is filed along with it.",
	})
	// Issuing for a checkout nobody paid for files a tax document for a sale
	// that did not happen, and undoing that is a correction with the tax
	// authority rather than a delete.
	KeyAdminQueueNotCommitted = key("admin.queue.notcommitted", Message{
		ZhHant: "訂單成立後才能開立發票。",
		En:     "An invoice can only be issued once the order is committed.",
	})
)

var (
	// The stock list, and the per-row forms in it.
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
	// Three numbers in one column, in the order the cell prints them.
	KeyAdminQueueColStock = key("admin.queue.col.stock", Message{
		ZhHant: "庫存 / 安全 / 可售",
		En:     "Stock / safety / sellable",
	})
	KeyAdminQueueColPrice    = key("admin.queue.col.price", Message{ZhHant: "價格", En: "Price"})
	KeyAdminQueueViewProduct = key("admin.queue.viewproduct", Message{
		ZhHant: "看商品頁",
		En:     "See the product page",
	})
	// A variant nobody may buy. Separate from the campaign's and the coupon's
	// 已停用 for the reason those two are separate from each other: the subject
	// is what a reader has to recover from the badge.
	KeyAdminQueueVariantOff = key("admin.queue.variantoff", Message{
		ZhHant: "已停用",
		En:     "Switched off",
	})
	// What the customer pays, and what it is shown struck through beside. The
	// second is optional and the first is not.
	KeyAdminQueuePrice        = key("admin.queue.price", Message{ZhHant: "售價", En: "Selling price"})
	KeyAdminQueueComparePrice = key("admin.queue.compareprice", Message{
		ZhHant: "原價",
		En:     "Compare-at price",
	})
	KeyAdminQueuePriceSave = key("admin.queue.price.save", Message{
		ZhHant: "改價",
		En:     "Change the price",
	})
	// The column heading and the button under it are one act.
	KeyAdminQueueAdjust = key("admin.queue.adjust", Message{ZhHant: "調整", En: "Adjust"})
	// A DELTA rather than a new figure — record_inventory_movement is the only
	// writer of stock_quantity, and it takes a movement.
	KeyAdminQueueAdjustQty = key("admin.queue.adjust.qty", Message{
		ZhHant: "調整數量",
		En:     "Adjustment quantity",
	})
)
