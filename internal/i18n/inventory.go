package i18n

var (
	KeyAdminColWhen = key("admin.col.when", Message{ZhHant: "時間", En: "When"})

	KeyAdminColChange = key("admin.col.change", Message{ZhHant: "異動", En: "Change"})

	KeyAdminColActor = key("admin.col.actor", Message{ZhHant: "操作者", En: "By"})

	KeyAdminColBalance = key("admin.col.balance", Message{ZhHant: "結存", En: "Balance"})

	KeyAdminPageStockList = key("admin.page.stocklist", Message{ZhHant: "庫存管理", En: "Stock management"})

	KeyAdminQueueStockLead = key("admin.queue.stock.lead", Message{
		ZhHant: "庫存只能透過調整寫入,每次調整都會留下一筆異動紀錄。",
		En: "Stock can only be written through an adjustment, and every adjustment leaves a " +
			"movement in the ledger.",
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
		ZhHant: "· 目前 %s 件,安全庫存 %s",
		En:     "· %s in stock, safety level %s",
	})

	KeyAdminLedgerEmpty = key("admin.ledger.empty", Message{
		ZhHant: "這個規格還沒有任何異動。新規格的庫存是 0,所有的量都從這張帳本進來。",
		En: "Nothing has moved for this variant yet. A new variant starts at zero, and every unit it " +
			"ever holds arrives through this ledger.",
	})

	KeyAdminLedgerFoot = key("admin.ledger.foot", Message{
		ZhHant: "最近 50 筆。結存是從帳本開頭累加到那一筆的數字,所以就算只看這一頁也是對的。",
		En: "The last 50 movements. The balance accumulates from the start of the ledger rather than " +
			"from this page, so these rows are still true on their own.",
	})

	KeyAdminReceiveQty = key("admin.receive.qty", Message{ZhHant: "進貨數量", En: "Quantity received"})

	KeyAdminReceiveButton = key("admin.receive.button", Message{ZhHant: "登記進貨", En: "Record receipt"})

	KeyAdminReceiveHint = key("admin.receive.hint", Message{
		ZhHant: "這會在帳本上記一筆「進貨」。數字算錯要往回修的話請用庫存頁的「調整」—— 東西進來和數字算錯是兩件事,帳本要分得出來。",
		En: "This writes a goods receipt to the ledger. To correct a count downward use Adjust on the " +
			"stock page — goods arriving and a number being wrong are two different things, and the " +
			"ledger has to keep them apart.",
	})

	KeyAdminStockOf = key("admin.stock.of", Message{
		ZhHant: "%d 件(可售 %d)",
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
		ZhHant: "進貨已入庫,帳本上記的是「進貨」而不是「人工調整」。",
		En:     "Received. The ledger records this as a goods receipt, not as a manual correction.",
	})

	KeyAdminNoticeBadQty = key("admin.notice.badqty", Message{
		ZhHant: "進貨數量要是正整數。要往下修正數字請用「調整」—— 進貨是有東西進來,調整是數字算錯了,帳本分得出這兩件事。",
		En: "A receipt quantity is a positive whole number. To correct a count downward use Adjust — " +
			"a receipt is goods arriving and an adjustment is a number that was wrong, and the ledger keeps them apart.",
	})
)
