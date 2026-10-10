package i18n

var (
	// KeyInStock is the one sentence goen has for "there is stock": the
	// product page wears it as a badge and the comparison table prints it in
	// a cell, so the name says the fact and not the surface.
	KeyInStock = key("stock.instock", Message{ZhHant: "有貨", En: "In stock"})

	KeyAdminColWhen = key("admin.col.when", Message{ZhHant: "時間", En: "When"})

	KeyAdminColChange = key("admin.col.change", Message{ZhHant: "異動", En: "Change"})

	KeyAdminColActor = key("admin.col.actor", Message{ZhHant: "操作者", En: "By"})

	KeyAdminColBalance = key("admin.col.balance", Message{ZhHant: "結存", En: "Balance"})

	KeyAdminPageStockList = key("admin.page.stocklist", Message{ZhHant: "庫存管理", En: "Stock management"})

	KeyAdminQueueStockLead = key("admin.queue.stock.lead", Message{
		ZhHant: "庫存只能透過調整寫入，每次調整都會留下一筆異動紀錄。",
		En: "Stock can only be written through an adjustment, and every adjustment leaves a " +
			"movement in the ledger.",
	})

	KeyAdminStockList = key("admin.stock.list", Message{ZhHant: "庫存品項清單", En: "Stock items"})

	KeyAdminStockSearch = key("admin.stock.search", Message{ZhHant: "搜尋庫存品項", En: "Search stock items"})

	KeyAdminStockSearchPlaceholder = key("admin.stock.searchplaceholder", Message{
		ZhHant: "SKU 或商品名稱",
		En:     "SKU or product name",
	})

	KeyAdminQueueColStock = key("admin.queue.col.stock", Message{
		ZhHant: "庫存 / 安全 / 可售",
		En:     "Stock / safety / sellable",
	})

	KeyAdminQueueColPrice = key("admin.queue.col.price", Message{ZhHant: "價格", En: "Price"})

	KeyAdminQueueViewProduct = key("admin.queue.viewproduct", Message{
		ZhHant: "看商品頁",
		En:     "See the product page",
	})

	KeyAdminQueueVariantOff = key("admin.queue.variantoff", Message{
		ZhHant: "已停用",
		En:     "Switched off",
	})

	KeyAdminQueuePrice = key("admin.queue.price", Message{ZhHant: "售價", En: "Selling price"})

	KeyAdminQueueComparePrice = key("admin.queue.compareprice", Message{
		ZhHant: "原價",
		En:     "Compare-at price",
	})

	KeyAdminQueuePriceSave = key("admin.queue.price.save", Message{
		ZhHant: "改價",
		En:     "Change the price",
	})

	KeyAdminQueueAdjust = key("admin.queue.adjust", Message{ZhHant: "調整", En: "Adjust"})

	KeyAdminQueueAdjustQty = key("admin.queue.adjust.qty", Message{
		ZhHant: "調整數量",
		En:     "Adjustment quantity",
	})

	KeyAdminStockLink = key("admin.stock.link", Message{ZhHant: "庫存", En: "Stock"})

	KeyAdminStockNowSafe = key("admin.stock.nowsafe", Message{
		ZhHant: "· 目前 %s 件，安全庫存 %s",
		En:     "· %s in stock, safety stock %s",
	})

	KeyAdminLedgerEmpty = key("admin.ledger.empty", Message{
		ZhHant: "這個規格還沒有任何異動。新規格的庫存是 0，所有的量都從這張帳本進來。",
		En: "Nothing has moved for this variant yet. A new variant starts at zero, and every unit it " +
			"ever holds arrives through this ledger.",
	})

	KeyAdminLedgerFoot = key("admin.ledger.foot", Message{
		ZhHant: "最近 50 筆。結存是從帳本第一筆累加到該筆的數量。",
		En:     "The last 50 movements. Each balance counts from the first movement in the ledger.",
	})

	KeyAdminStockLineStock = key("admin.stock.line.stock", Message{ZhHant: "庫存", En: "In stock"})

	KeyAdminStockLineSafety = key("admin.stock.line.safety", Message{ZhHant: "安全庫存", En: "Safety stock"})

	KeyAdminStockLineReceived = key("admin.stock.line.received", Message{ZhHant: "進貨", En: "Received"})

	// The caption is the three sentences below it, each counted on its own.
	KeyAdminStockLineCaption = key("admin.stock.line.caption", Message{ZhHant: "%s；%s，%s。", En: "%s; %s, %s."})

	KeyAdminStockLineNow = key("admin.stock.line.now", Message{ZhHant: "目前 %d 件", En: "%d in stock"})

	KeyAdminStockLineReceipts = countKey("admin.stock.line.receipts",
		"%[2]d 天裡進貨 %[1]d 次",
		"received %[1]d time in %[2]d days", "received %[1]d times in %[2]d days")

	KeyAdminStockLineHeld = countKey("admin.stock.line.held",
		"有 %d 天不高於安全庫存",
		"at or below the safety stock on %d day", "at or below the safety stock on %d days")

	KeyAdminStockLineLow = key("admin.stock.line.low", Message{ZhHant: "最低到過 %d 件", En: "never below %d"})

	KeyAdminStockLineNote = key("admin.stock.line.note", Message{
		ZhHant: "每天結束時的庫存，由現在的庫存依異動帳本往回推算。",
		En:     "The stock at the close of each day, worked back from the current count through the movement ledger.",
	})

	KeyAdminStockLineNone = countKey("admin.stock.line.none",
		"近 %d 天沒有庫存異動。",
		"No stock movement in the last %d day.", "No stock movement in the last %d days.")

	KeyAdminStockLineOne = countKey("admin.stock.line.one",
		"近 %d 天只有 1 筆庫存異動，還畫不出庫存線。",
		"Only one stock movement in the last %d day, too few to draw a line.",
		"Only one stock movement in the last %d days, too few to draw a line.")

	KeyAdminReceiveQty = key("admin.receive.qty", Message{ZhHant: "進貨數量", En: "Quantity received"})

	KeyAdminReceiveButton = key("admin.receive.button", Message{ZhHant: "登記進貨", En: "Record receipt"})

	KeyAdminReceiveHint = key("admin.receive.hint", Message{
		ZhHant: "這會在帳本記一筆「進貨」。數量算錯要往下修正時，請到庫存頁用「調整」。",
		En:     "This records a goods receipt in the ledger. To correct a count downward, use “Adjust” on the stock page.",
	})

	KeyAdminStockOf = key("admin.stock.of", Message{
		ZhHant: "%d 件（可售 %d）",
		En:     "%d in stock (%d sellable)",
	})

	KeyAdminMoveReceipt = key("admin.move.receipt", Message{ZhHant: "進貨", En: "Goods receipt"})

	KeyAdminMoveHold = key("admin.move.hold", Message{ZhHant: "結帳保留", En: "Checkout hold"})

	KeyAdminMoveSale = key("admin.move.sale", Message{ZhHant: "出貨扣除", En: "Dispatched"})

	KeyAdminMoveRelease = key("admin.move.release", Message{ZhHant: "釋放回架", En: "Hold released"})

	KeyAdminMoveReturn = key("admin.move.return", Message{ZhHant: "退貨入庫", En: "Returned to stock"})

	KeyAdminMoveAdjustment = key("admin.move.adjustment", Message{ZhHant: "人工調整", En: "Manual correction"})
)

var (
	KeyAdminNoticeReceived = key("admin.notice.received", Message{
		ZhHant: "進貨已入庫，帳本上記的是「進貨」而不是「人工調整」。",
		En:     "Received. The ledger records this as a goods receipt, not as a manual correction.",
	})

	KeyAdminReceiptConflict = key("admin.receipt.conflict", Message{
		ZhHant: "這筆收貨與已記錄的庫存異動不符。請先確認異動紀錄，再收貨。",
		En:     "This receipt conflicts with an already recorded stock movement. Review the ledger before receiving more stock.",
	})

	KeyAdminNoticeBadQty = key("admin.notice.badqty", Message{
		ZhHant: "請輸入 1 到 10,000 的整數。要往下修正數量，請用「調整」。",
		En:     "Enter a whole number from 1 to 10,000. To correct a count downward, use “Adjust”.",
	})
)
