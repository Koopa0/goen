package i18n

var (
	KeyAdminPickingSlips     = key("admin.picking.slips", Message{ZhHant: "揀貨表與裝箱單", En: "Pick list and packing slips"})
	KeyAdminPickingTotals    = key("admin.picking.totals", Message{ZhHant: "待揀貨總表", En: "Outstanding pick list"})
	KeyAdminPickingScope     = key("admin.picking.scope", Message{ZhHant: "揀貨總數涵蓋所有尚有商品未出貨的訂單。裝箱單每頁最多 %d 張，列印後請使用下一頁繼續。", En: "Pick quantities cover every order with items still to ship. Each batch contains up to %d slips; print it, then continue to the next page."})
	KeyAdminPickingNone      = key("admin.picking.none", Message{ZhHant: "目前沒有要揀貨的訂單。", En: "Nothing to pick right now."})
	KeyAdminPickingRemaining = key("admin.picking.remaining", Message{ZhHant: "尚未出貨數量", En: "Not yet dispatched"})

	KeyAdminBatchPrint     = key("admin.batch.print", Message{ZhHant: "只印選取的訂單", En: "Print the selected orders only"})
	KeyAdminBatchHintStart = key("admin.batch.hint.start", Message{ZhHant: "勾選要開始備貨的訂單，再按「開始備貨」。", En: "Tick the orders to start packing, then press Start packing."})
	KeyAdminBatchHintPrint = key("admin.batch.hint.print", Message{ZhHant: "勾選訂單後按「只印選取的訂單」；「揀貨表與裝箱單」印所有還有商品未出貨的訂單。", En: "Tick orders and press Print the selected orders only; Pick list and packing slips prints every order with items still to ship."})
	KeyAdminBatchColumn    = key("admin.batch.column", Message{ZhHant: "選取", En: "Select"})
	KeyAdminBatchSelect    = key("admin.batch.select", Message{ZhHant: "選取 %s", En: "Select %s"})
	KeyAdminBatchNone      = key("admin.batch.none", Message{ZhHant: "沒有選取任何訂單。", En: "No orders were selected."})
	KeyAdminBatchStarted   = key("admin.batch.started", Message{ZhHant: "已開始備貨，選取的訂單在下面。", En: "Packing has started; the selected orders are below."})

	KeyAdminBatchRefused         = key("admin.batch.refused", Message{ZhHant: "這些訂單沒有開始備貨：", En: "These orders did not start packing:"})
	KeyAdminBatchRefusalLine     = key("admin.batch.refusalline", Message{ZhHant: "%s：%s", En: "%s: %s"})
	KeyAdminBatchReasonCancelled = key("admin.batch.reason.cancelled", Message{ZhHant: "已取消", En: "Cancelled"})
	KeyAdminBatchReasonUnpaid    = key("admin.batch.reason.unpaid", Message{ZhHant: "還沒付清", En: "Not paid yet"})
	KeyAdminBatchReasonChanged   = key("admin.batch.reason.changed", Message{ZhHant: "狀態已經變了", En: "Its status has changed"})

	KeyAdminBatchOthersStarted = countKey("admin.batch.othersstarted",
		"其他 %d 筆已開始備貨。",
		"The other %d order has started packing.",
		"The other %d orders have started packing.")

	KeyAdminSlipsSelected = countKey("admin.picking.selected",
		"選取的 %d 筆",
		"%d selected order",
		"%d selected orders")
	KeyAdminSlipsSelectedGone = countKey("admin.picking.selectedgone",
		"選取的 %[2]d 筆中有 %[1]d 筆已沒有待出貨的商品，沒有列出。",
		"%[1]d of the %[2]d selected orders has nothing left to ship and is not listed.",
		"%[1]d of the %[2]d selected orders have nothing left to ship and are not listed.")
)
